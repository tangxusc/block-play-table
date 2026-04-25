package executor

import (
	"bufio"
	"context"
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
		return fmt.Errorf("agent %s is not configured", payload.Task.AgentType)
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
	worktree, err := e.prepareWorktree(ctx, payload)
	if err != nil {
		e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error()})
		return err
	}
	if err := e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskStarted, TaskID: payload.Task.ID, Content: worktree}); err != nil {
		return err
	}

	for _, command := range payload.Project.SetupCommands {
		if err := e.runShell(ctx, payload.Task.ID, worktree, env, command); err != nil {
			e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error()})
			return err
		}
	}
	for _, command := range payload.Task.PreCommands {
		if err := e.runShell(ctx, payload.Task.ID, worktree, env, command); err != nil {
			e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error()})
			return err
		}
	}

	err = agent.Run(ctx, AgentInput{Task: payload.Task, Project: payload.Project, WorktreeDir: worktree, Env: env}, func(event AgentEvent) {
		switch event.Type {
		case AgentEventStdout:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stdout", Content: event.Content})
		case AgentEventStderr:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stderr", Content: event.Content})
		case AgentEventConversation:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskConversation, TaskID: payload.Task.ID, Content: event.Content, Metadata: event.Metadata})
		case AgentEventWaitingInput:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskWaitingInput, TaskID: payload.Task.ID, Content: event.Content})
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
	for _, command := range payload.Task.PostCommands {
		if err := e.runShell(ctx, payload.Task.ID, worktree, env, command); err != nil {
			e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error()})
			return err
		}
	}
	return e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskCompleted, TaskID: payload.Task.ID, Result: "completed"})
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
	name := fmt.Sprintf("%s-%s-%s", payload.Project.WorktreeNamePrefix, payload.Task.ID, time.Now().Format("01021504"))
	name = strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(name)
	target := filepath.Join(e.workDir, name)
	if payload.Project.GitURL != "" && looksLikeGitRepo(payload.Project.GitURL) {
		if err := runCommand(ctx, "", nil, "git", "clone", "--no-hardlinks", payload.Project.GitURL, target); err != nil {
			return "", err
		}
		branch := payload.Task.TargetBranch
		if branch == "" {
			branch = payload.Project.DefaultBranch
		}
		_ = runCommand(ctx, target, nil, "git", "checkout", "-B", branch)
		return target, nil
	}
	return target, os.MkdirAll(target, 0o755)
}

func (e *Executor) runShell(ctx context.Context, taskID, dir string, env map[string]string, command string) error {
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
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: taskID, Stream: "stdout", Content: string(out)})
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
	cmd := exec.CommandContext(ctx, a.binary, args...)
	cmd.Dir = input.WorktreeDir
	cmd.Env = mergeEnv(input.Env)
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
	wg.Wait()
	return cmd.Wait()
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
		env[item.Key] = item.Value
	}
	return env
}

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func looksLikeGitRepo(path string) bool {
	if strings.HasPrefix(path, "git@") || strings.HasSuffix(path, ".git") || strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "ssh://") {
		return true
	}
	if stat, err := os.Stat(filepath.Join(path, ".git")); err == nil && (stat.IsDir() || !stat.IsDir()) {
		return true
	}
	return false
}

func runCommand(ctx context.Context, dir string, env map[string]string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}
