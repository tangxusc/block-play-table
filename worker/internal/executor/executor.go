package executor

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

type AgentEventType string

const (
	AgentEventStdout       AgentEventType = "stdout"
	AgentEventStderr       AgentEventType = "stderr"
	AgentEventConversation AgentEventType = "conversation"
	AgentEventWaitingInput AgentEventType = "waiting_input"
	AgentEventCompleted    AgentEventType = "completed"
	AgentEventFailed       AgentEventType = "failed"
)

type AgentEvent struct {
	Type     AgentEventType
	Content  string
	Metadata map[string]string
}

type AgentInput struct {
	Task        protocol.TaskPayload
	Project     protocol.ProjectPayload
	WorktreeDir string
	Env         map[string]string
}

type Agent interface {
	Run(context.Context, AgentInput, func(AgentEvent)) error
}

type AgentFunc func(context.Context, AgentInput, func(AgentEvent)) error

func (fn AgentFunc) Run(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
	return fn(ctx, input, emit)
}

type Reporter interface {
	Report(context.Context, protocol.WorkerEvent) error
}

type ReporterFunc func(context.Context, protocol.WorkerEvent) error

func (fn ReporterFunc) Report(ctx context.Context, event protocol.WorkerEvent) error {
	return fn(ctx, event)
}

type Config struct {
	WorkerID string
	WorkDir  string
	Agents   map[domain.AgentType]Agent
	Reporter Reporter
}

type Executor struct {
	workerID string
	workDir  string
	agents   map[domain.AgentType]Agent
	reporter Reporter

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func NewExecutor(config Config) *Executor {
	if config.WorkDir == "" {
		config.WorkDir = filepath.Join(os.TempDir(), "block-play-table-worker")
	}
	if config.Reporter == nil {
		config.Reporter = ReporterFunc(func(context.Context, protocol.WorkerEvent) error { return nil })
	}
	agents := map[domain.AgentType]Agent{
		domain.AgentCodex:  NewCommandAgent("codex", []string{"exec", "--skip-git-repo-check"}),
		domain.AgentClaude: NewCommandAgent("claude", []string{"-p"}),
	}
	for k, v := range config.Agents {
		agents[k] = v
	}
	return &Executor{
		workerID: config.WorkerID,
		workDir:  config.WorkDir,
		agents:   agents,
		reporter: config.Reporter,
		running:  map[string]context.CancelFunc{},
	}
}

func (e *Executor) Execute(ctx context.Context, payload protocol.TaskStartPayload) error {
	agent, ok := e.agents[payload.Task.AgentType]
	if !ok {
		err := fmt.Errorf("agent %s is not configured", payload.Task.AgentType)
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error()})
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.running[payload.Task.ID] = cancel
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.running, payload.Task.ID)
		e.mu.Unlock()
	}()

	env := runtimeEnv(payload.AgentRuntimeEnv)
	redact := redactor(payload.AgentRuntimeEnv)
	worktree, err := e.prepareWorktree(ctx, payload)
	if err != nil {
		e.reportExecutionError(ctx, payload.Task.ID, err)
		return err
	}
	if err := e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskStarted, TaskID: payload.Task.ID, Content: worktree}); err != nil {
		return err
	}

	for _, command := range payload.Task.PreCommands {
		if err := e.runShell(ctx, payload.Task.ID, worktree, env, command, redact); err != nil {
			e.reportExecutionError(ctx, payload.Task.ID, err)
			return err
		}
	}

	finalResult := ""
	agentFailed := ""
	err = agent.Run(ctx, AgentInput{Task: payload.Task, Project: payload.Project, WorktreeDir: worktree, Env: env}, func(event AgentEvent) {
		switch event.Type {
		case AgentEventStdout:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stdout", Content: redact(event.Content)})
		case AgentEventStderr:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stderr", Content: redact(event.Content)})
		case AgentEventConversation:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskConversation, TaskID: payload.Task.ID, Content: redact(event.Content), Metadata: event.Metadata})
		case AgentEventWaitingInput:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskWaitingInput, TaskID: payload.Task.ID, Content: redact(event.Content)})
		case AgentEventCompleted:
			finalResult = redact(event.Content)
		case AgentEventFailed:
			agentFailed = redact(event.Content)
		}
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_ = e.report(context.Background(), protocol.WorkerEvent{Type: protocol.MessageTaskInterrupted, TaskID: payload.Task.ID, Result: "interrupted"})
		} else {
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error()})
		}
		return err
	}
	if agentFailed != "" {
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: agentFailed})
		return fmt.Errorf("agent failed: %s", agentFailed)
	}
	for _, command := range payload.Task.PostCommands {
		if err := e.runShell(ctx, payload.Task.ID, worktree, env, command, redact); err != nil {
			e.reportExecutionError(ctx, payload.Task.ID, err)
			return err
		}
	}
	if finalResult == "" {
		finalResult = "completed"
	}
	if err := e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskResult, TaskID: payload.Task.ID, Result: finalResult}); err != nil {
		return err
	}
	return e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskCompleted, TaskID: payload.Task.ID, Result: finalResult})
}

func (e *Executor) Interrupt(taskID string) {
	e.mu.Lock()
	cancel := e.running[taskID]
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Executor) prepareWorktree(ctx context.Context, payload protocol.TaskStartPayload) (string, error) {
	if err := os.MkdirAll(e.workDir, 0o755); err != nil {
		return "", err
	}
	name := worktreeName(payload)
	target := filepath.Join(e.workDir, name)
	if payload.Project.GitURL != "" && looksLikeGitRepo(payload.Project.GitURL) {
		cacheDir := filepath.Join(e.workDir, ".repos", repositoryCacheName(payload.Project))
		if err := e.ensureRepositoryCache(ctx, payload.Project.GitURL, cacheDir); err != nil {
			return "", err
		}
		branch := payload.Task.TargetBranch
		if branch == "" {
			branch = "task/" + payload.Task.ID
		}
		base := payload.Task.BaseBranch
		if base == "" {
			base = payload.Project.DefaultBranch
		}
		if base == "" {
			base = "HEAD"
		}
		if err := runCommand(ctx, cacheDir, nil, "git", "worktree", "prune"); err != nil {
			return "", err
		}
		if err := runCommand(ctx, cacheDir, nil, "git", "worktree", "add", "-B", branch, target, resolveGitRef(ctx, cacheDir, base)); err != nil {
			return "", err
		}
		return target, nil
	}
	return target, os.MkdirAll(target, 0o755)
}

func (e *Executor) ensureRepositoryCache(ctx context.Context, gitURL, cacheDir string) error {
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); err == nil {
		return runCommand(ctx, cacheDir, nil, "git", "fetch", "--all", "--prune")
	}
	if stat, err := os.Stat(cacheDir); err == nil && stat.IsDir() {
		return fmt.Errorf("repository cache exists but is not a git repository: %s", cacheDir)
	}
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return err
	}
	return runCommand(ctx, "", nil, "git", "clone", gitURL, cacheDir)
}

func (e *Executor) runShell(ctx context.Context, taskID, dir string, env map[string]string, command string, redact func(string) string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: taskID, Stream: "system", Content: "$ " + command})
	shell := "sh"
	args := []string{"-c", command}
	if runtime.GOOS == "windows" {
		shell = "cmd"
		args = []string{"/C", command}
	}
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	out, err := combinedOutput(ctx, cmd)
	if len(out) > 0 {
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: taskID, Stream: "stdout", Content: redact(string(out))})
	}
	if err != nil {
		return fmt.Errorf("command %q failed: %w", command, err)
	}
	return nil
}

func (e *Executor) report(ctx context.Context, event protocol.WorkerEvent) error {
	if event.MessageID == "" {
		event.MessageID = "msg_" + uuid.NewString()
	}
	if event.WorkerID == "" {
		event.WorkerID = e.workerID
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	return e.reporter.Report(ctx, event)
}

func (e *Executor) reportExecutionError(ctx context.Context, taskID string, err error) {
	if errors.Is(ctx.Err(), context.Canceled) {
		_ = e.report(context.Background(), protocol.WorkerEvent{Type: protocol.MessageTaskInterrupted, TaskID: taskID, Result: "interrupted"})
		return
	}
	_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: taskID, Result: err.Error()})
}

type CommandAgent struct {
	binary string
	args   []string
}

func NewCommandAgent(binary string, args []string) *CommandAgent {
	return &CommandAgent{binary: binary, args: append([]string(nil), args...)}
}

func (a *CommandAgent) Run(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
	prompt := input.Task.Description
	if strings.TrimSpace(prompt) == "" {
		prompt = input.Task.Title
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Complete task " + input.Task.ID
	}
	args := append([]string(nil), a.args...)
	args = append(args, prompt)
	cmd := exec.Command(a.binary, args...)
	cmd.Dir = input.WorktreeDir
	cmd.Env = mergeEnv(input.Env)
	configureCommandForCancel(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go scanPipe(&wg, stdout, func(line string) {
		emit(AgentEvent{Type: AgentEventStdout, Content: line})
		emit(AgentEvent{Type: AgentEventConversation, Content: line})
	})
	go scanPipe(&wg, stderr, func(line string) {
		emit(AgentEvent{Type: AgentEventStderr, Content: line})
	})
	done := make(chan error, 1)
	go func() {
		wg.Wait()
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killCommandProcessGroup(cmd)
		<-done
		return ctx.Err()
	}
}

func scanPipe(wg *sync.WaitGroup, pipe any, emit func(string)) {
	defer wg.Done()
	reader, ok := pipe.(interface{ Read([]byte) (int, error) })
	if !ok {
		return
	}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		emit(scanner.Text())
	}
}

func runtimeEnv(vars []protocol.RuntimeEnvVar) map[string]string {
	env := map[string]string{}
	for _, item := range vars {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		env[item.Key] = item.Value
	}
	return env
}

func redactor(vars []protocol.RuntimeEnvVar) func(string) string {
	values := make([]string, 0, len(vars))
	for _, item := range vars {
		if item.Sensitive && item.Value != "" {
			values = append(values, item.Value)
		}
	}
	return func(value string) string {
		for _, secret := range values {
			value = strings.ReplaceAll(value, secret, "********")
		}
		return value
	}
}

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func looksLikeGitRepo(path string) bool {
	if strings.HasPrefix(path, "git@") || strings.HasSuffix(path, ".git") || strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "ssh://") || strings.HasPrefix(path, "git://") || strings.HasPrefix(path, "file://") {
		return true
	}
	if stat, err := os.Stat(filepath.Join(path, ".git")); err == nil && (stat.IsDir() || !stat.IsDir()) {
		return true
	}
	return false
}

func worktreeName(payload protocol.TaskStartPayload) string {
	prefix := payload.Project.WorktreeNamePrefix
	if prefix == "" {
		prefix = payload.Project.ID
	}
	if prefix == "" {
		prefix = "task"
	}
	return safePathPart(fmt.Sprintf("%s-%s-%s", prefix, payload.Task.ID, time.Now().Format("01021504")))
}

func repositoryCacheName(project protocol.ProjectPayload) string {
	label := project.WorktreeNamePrefix
	if label == "" {
		label = project.ID
	}
	if label == "" {
		label = "project"
	}
	sum := sha1.Sum([]byte(project.ID + "\x00" + project.GitURL))
	return safePathPart(label) + "-" + hex.EncodeToString(sum[:])[:12]
}

func safePathPart(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	builder.Grow(len(value))
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(builder.String(), "-.")
	if out == "" {
		return "item"
	}
	if len(out) > 80 {
		return out[:80]
	}
	return out
}

func resolveGitRef(ctx context.Context, repoDir, ref string) string {
	if ref == "" || ref == "HEAD" {
		return "HEAD"
	}
	if runCommand(ctx, repoDir, nil, "git", "rev-parse", "--verify", ref+"^{commit}") == nil {
		return ref
	}
	remoteRef := "origin/" + ref
	if runCommand(ctx, repoDir, nil, "git", "rev-parse", "--verify", remoteRef+"^{commit}") == nil {
		return remoteRef
	}
	return ref
}

func runCommand(ctx context.Context, dir string, env map[string]string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	out, err := combinedOutput(ctx, cmd)
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}

func combinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	configureCommandForCancel(cmd)
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		done <- result{out: out, err: err}
	}()
	select {
	case res := <-done:
		return res.out, res.err
	case <-ctx.Done():
		killCommandProcessGroup(cmd)
		res := <-done
		if ctx.Err() != nil {
			return res.out, ctx.Err()
		}
		return res.out, res.err
	}
}
