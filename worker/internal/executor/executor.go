package executor

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
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
	Type           AgentEventType
	Content        string
	AgentSessionID string
	Metadata       map[string]string
}

type AgentInteractionRequest struct {
	InteractionID  string
	Kind           domain.TaskInteractionKind
	Title          string
	Body           string
	RawPayload     string
	AgentSessionID string
}

type AgentInteractionResponse struct {
	InteractionID string
	TaskID        string
	Decision      domain.TaskInteractionDecision
	Message       string
	Payload       string
}

type AgentInput struct {
	Task               protocol.TaskPayload
	Project            protocol.ProjectPayload
	WorktreeDir        string
	Env                map[string]string
	RequestInteraction func(context.Context, AgentInteractionRequest) (AgentInteractionResponse, error)
}

type AgentContinuationInput struct {
	Task               protocol.TaskPayload
	Project            protocol.ProjectPayload
	WorktreeDir        string
	Env                map[string]string
	Message            string
	AgentSessionID     string
	RequestInteraction func(context.Context, AgentInteractionRequest) (AgentInteractionResponse, error)
}

type Agent interface {
	Run(context.Context, AgentInput, func(AgentEvent)) error
}

type ContinuableAgent interface {
	Continue(context.Context, AgentContinuationInput, func(AgentEvent)) error
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
	running map[string]*taskRun
}

type taskRun struct {
	cancel       context.CancelFunc
	interactions map[string]chan protocol.TaskInteractionResponsePayload
}

func NewExecutor(config Config) *Executor {
	if config.WorkDir == "" {
		config.WorkDir = filepath.Join(os.TempDir(), "block-play-table-worker")
	}
	if config.Reporter == nil {
		config.Reporter = ReporterFunc(func(context.Context, protocol.WorkerEvent) error { return nil })
	}
	agents := map[domain.AgentType]Agent{
		domain.AgentCodex:  newCodexAppServerAgent("codex"),
		domain.AgentClaude: newClaudeAgent("claude", func() string { return uuid.NewString() }),
	}
	for k, v := range config.Agents {
		agents[k] = v
	}
	return &Executor{
		workerID: config.WorkerID,
		workDir:  config.WorkDir,
		agents:   agents,
		reporter: config.Reporter,
		running:  map[string]*taskRun{},
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
	e.registerRun(payload.Task.ID, cancel)
	defer func() {
		cancel()
		e.unregisterRun(payload.Task.ID)
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
	finalSessionID := ""
	err = agent.Run(ctx, AgentInput{
		Task:        payload.Task,
		Project:     payload.Project,
		WorktreeDir: worktree,
		Env:         env,
		RequestInteraction: func(ctx context.Context, request AgentInteractionRequest) (AgentInteractionResponse, error) {
			return e.requestInteraction(ctx, payload.Task.ID, request, redact)
		},
	}, func(event AgentEvent) {
		switch event.Type {
		case AgentEventStdout:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stdout", Content: redact(event.Content)})
		case AgentEventStderr:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stderr", Content: redact(event.Content)})
		case AgentEventConversation:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskConversation, TaskID: payload.Task.ID, Content: redact(event.Content), AgentSessionID: event.AgentSessionID, Metadata: event.Metadata})
		case AgentEventWaitingInput:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskWaitingInput, TaskID: payload.Task.ID, Content: redact(event.Content)})
		case AgentEventCompleted:
			finalResult = redact(event.Content)
			finalSessionID = event.AgentSessionID
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
	if err := e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskResult, TaskID: payload.Task.ID, Result: finalResult, AgentSessionID: finalSessionID}); err != nil {
		return err
	}
	return e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskCompleted, TaskID: payload.Task.ID, Result: finalResult, AgentSessionID: finalSessionID})
}

func (e *Executor) Continue(ctx context.Context, payload protocol.TaskContinuePayload) error {
	agent, ok := e.agents[payload.Task.AgentType]
	if !ok {
		err := fmt.Errorf("agent %s is not configured", payload.Task.AgentType)
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error(), AgentSessionID: payload.AgentSessionID})
		return err
	}
	continuable, ok := agent.(ContinuableAgent)
	if !ok {
		err := fmt.Errorf("agent %s does not support continuation", payload.Task.AgentType)
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error(), AgentSessionID: payload.AgentSessionID})
		return err
	}
	if strings.TrimSpace(payload.WorktreePath) == "" {
		err := fmt.Errorf("worktree path is required to continue task %s", payload.Task.ID)
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error(), AgentSessionID: payload.AgentSessionID})
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	e.registerRun(payload.Task.ID, cancel)
	defer func() {
		cancel()
		e.unregisterRun(payload.Task.ID)
	}()

	env := runtimeEnv(payload.AgentRuntimeEnv)
	redact := redactor(payload.AgentRuntimeEnv)
	if err := e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskStarted, TaskID: payload.Task.ID, Content: payload.WorktreePath, AgentSessionID: payload.AgentSessionID}); err != nil {
		return err
	}

	finalResult := ""
	agentFailed := ""
	finalSessionID := payload.AgentSessionID
	err := continuable.Continue(ctx, AgentContinuationInput{
		Task:           payload.Task,
		Project:        payload.Project,
		WorktreeDir:    payload.WorktreePath,
		Env:            env,
		Message:        payload.Message,
		AgentSessionID: payload.AgentSessionID,
		RequestInteraction: func(ctx context.Context, request AgentInteractionRequest) (AgentInteractionResponse, error) {
			if request.AgentSessionID == "" {
				request.AgentSessionID = payload.AgentSessionID
			}
			return e.requestInteraction(ctx, payload.Task.ID, request, redact)
		},
	}, func(event AgentEvent) {
		switch event.Type {
		case AgentEventStdout:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stdout", Content: redact(event.Content), AgentSessionID: event.AgentSessionID})
		case AgentEventStderr:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: payload.Task.ID, Stream: "stderr", Content: redact(event.Content), AgentSessionID: event.AgentSessionID})
		case AgentEventConversation:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskConversation, TaskID: payload.Task.ID, Content: redact(event.Content), AgentSessionID: event.AgentSessionID, Metadata: event.Metadata})
		case AgentEventWaitingInput:
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskWaitingInput, TaskID: payload.Task.ID, Content: redact(event.Content), AgentSessionID: event.AgentSessionID})
		case AgentEventCompleted:
			finalResult = redact(event.Content)
			if event.AgentSessionID != "" {
				finalSessionID = event.AgentSessionID
			}
		case AgentEventFailed:
			agentFailed = redact(event.Content)
		}
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_ = e.report(context.Background(), protocol.WorkerEvent{Type: protocol.MessageTaskInterrupted, TaskID: payload.Task.ID, Result: "interrupted", AgentSessionID: finalSessionID})
		} else {
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: err.Error(), AgentSessionID: finalSessionID})
		}
		return err
	}
	if agentFailed != "" {
		_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskFailed, TaskID: payload.Task.ID, Result: agentFailed, AgentSessionID: finalSessionID})
		return fmt.Errorf("agent failed: %s", agentFailed)
	}
	if finalResult == "" {
		finalResult = "completed"
	}
	if err := e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskResult, TaskID: payload.Task.ID, Result: finalResult, AgentSessionID: finalSessionID}); err != nil {
		return err
	}
	return e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskCompleted, TaskID: payload.Task.ID, Result: finalResult, AgentSessionID: finalSessionID})
}

func (e *Executor) Interrupt(taskID string) {
	e.mu.Lock()
	run := e.running[taskID]
	e.mu.Unlock()
	if run != nil && run.cancel != nil {
		run.cancel()
	}
}

func (e *Executor) HandleTaskInteractionResponse(ctx context.Context, payload protocol.TaskInteractionResponsePayload) error {
	if payload.InteractionID == "" {
		return fmt.Errorf("interaction id is required")
	}
	taskID := payload.TaskID
	var ch chan protocol.TaskInteractionResponsePayload
	e.mu.Lock()
	if taskID != "" {
		if run := e.running[taskID]; run != nil {
			ch = run.interactions[payload.InteractionID]
		}
	} else {
		for id, run := range e.running {
			if candidate := run.interactions[payload.InteractionID]; candidate != nil {
				taskID = id
				ch = candidate
				break
			}
		}
	}
	e.mu.Unlock()
	if ch == nil {
		err := fmt.Errorf("no running interaction %s for task %s", payload.InteractionID, taskID)
		if taskID != "" {
			_ = e.report(ctx, protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: taskID, Stream: "stderr", Content: err.Error()})
		}
		return err
	}
	select {
	case ch <- payload:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Executor) registerRun(taskID string, cancel context.CancelFunc) {
	e.mu.Lock()
	e.running[taskID] = &taskRun{cancel: cancel, interactions: map[string]chan protocol.TaskInteractionResponsePayload{}}
	e.mu.Unlock()
}

func (e *Executor) unregisterRun(taskID string) {
	e.mu.Lock()
	delete(e.running, taskID)
	e.mu.Unlock()
}

func (e *Executor) requestInteraction(ctx context.Context, taskID string, request AgentInteractionRequest, redact func(string) string) (AgentInteractionResponse, error) {
	if strings.TrimSpace(request.InteractionID) == "" {
		request.InteractionID = "interaction_" + uuid.NewString()
	}
	if !request.Kind.Valid() {
		return AgentInteractionResponse{}, fmt.Errorf("unsupported task interaction kind %q", request.Kind)
	}
	ch := make(chan protocol.TaskInteractionResponsePayload, 1)
	e.mu.Lock()
	run := e.running[taskID]
	if run == nil {
		e.mu.Unlock()
		return AgentInteractionResponse{}, fmt.Errorf("task %s is not running", taskID)
	}
	if _, exists := run.interactions[request.InteractionID]; exists {
		e.mu.Unlock()
		return AgentInteractionResponse{}, fmt.Errorf("interaction %s is already pending", request.InteractionID)
	}
	run.interactions[request.InteractionID] = ch
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if run := e.running[taskID]; run != nil {
			delete(run.interactions, request.InteractionID)
		}
		e.mu.Unlock()
	}()

	if err := e.report(ctx, protocol.WorkerEvent{
		Type:           protocol.MessageTaskInteractionRequest,
		TaskID:         taskID,
		InteractionID:  request.InteractionID,
		Kind:           request.Kind,
		Title:          redact(request.Title),
		Body:           redact(request.Body),
		RawPayload:     redact(request.RawPayload),
		AgentSessionID: request.AgentSessionID,
	}); err != nil {
		return AgentInteractionResponse{}, err
	}

	select {
	case payload := <-ch:
		if err := e.report(ctx, protocol.WorkerEvent{
			Type:          protocol.MessageTaskInteractionResolved,
			TaskID:        taskID,
			InteractionID: payload.InteractionID,
			Responded:     true,
			Decision:      payload.Decision,
			Message:       payload.Message,
			Payload:       payload.Payload,
		}); err != nil {
			return AgentInteractionResponse{}, err
		}
		return AgentInteractionResponse{
			InteractionID: payload.InteractionID,
			TaskID:        payload.TaskID,
			Decision:      payload.Decision,
			Message:       payload.Message,
			Payload:       payload.Payload,
		}, nil
	case <-ctx.Done():
		return AgentInteractionResponse{}, ctx.Err()
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
		branch := "task/" + payload.Task.ID
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
		if err := removeWorktreesForBranch(ctx, cacheDir, branch); err != nil {
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

type SessionCommandAgent struct {
	binary       string
	agentType    domain.AgentType
	newSessionID func() string
}

func newCodexAgent(binary string) *SessionCommandAgent {
	return &SessionCommandAgent{binary: binary, agentType: domain.AgentCodex}
}

func newClaudeAgent(binary string, newSessionID func() string) *SessionCommandAgent {
	if newSessionID == nil {
		newSessionID = func() string { return uuid.NewString() }
	}
	return &SessionCommandAgent{binary: binary, agentType: domain.AgentClaude, newSessionID: newSessionID}
}

func (a *SessionCommandAgent) Run(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
	prompt := promptForTask(input.Task)
	sessionID := ""
	var args []string
	switch a.agentType {
	case domain.AgentCodex:
		args = append(codexConfigArgs(input.Task.AgentConfig.Codex), "exec", "--skip-git-repo-check", "--json", prompt)
	case domain.AgentClaude:
		sessionID = a.newSessionID()
		args = append([]string{"-p"}, claudeConfigArgs(input.Task.AgentConfig.Claude)...)
		args = append(args, "--output-format=stream-json", "--verbose", "--session-id", sessionID, prompt)
	default:
		return fmt.Errorf("session command agent does not support %s", a.agentType)
	}
	return a.runSessionCommand(ctx, input.WorktreeDir, input.Env, args, sessionID, emit)
}

func (a *SessionCommandAgent) Continue(ctx context.Context, input AgentContinuationInput, emit func(AgentEvent)) error {
	if strings.TrimSpace(input.AgentSessionID) == "" {
		return fmt.Errorf("agent session id is required")
	}
	if strings.TrimSpace(input.Message) == "" {
		return fmt.Errorf("message is required")
	}
	var args []string
	switch a.agentType {
	case domain.AgentCodex:
		args = append(codexConfigArgs(input.Task.AgentConfig.Codex), "exec", "resume", "--skip-git-repo-check", "--json", input.AgentSessionID, input.Message)
	case domain.AgentClaude:
		args = append([]string{"-p"}, claudeConfigArgs(input.Task.AgentConfig.Claude)...)
		args = append(args, "--output-format=stream-json", "--verbose", "--resume", input.AgentSessionID, input.Message)
	default:
		return fmt.Errorf("session command agent does not support %s", a.agentType)
	}
	return a.runSessionCommand(ctx, input.WorktreeDir, input.Env, args, input.AgentSessionID, emit)
}

func (a *SessionCommandAgent) runSessionCommand(ctx context.Context, dir string, env map[string]string, args []string, initialSessionID string, emit func(AgentEvent)) error {
	cmd := exec.Command(a.binary, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
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
	var mu sync.Mutex
	sessionID := initialSessionID
	lastConversation := ""
	finalResult := ""
	sawConversation := false
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			emit(AgentEvent{Type: AgentEventStdout, Content: line})
			parsed := parseAgentJSONLine(line)
			mu.Lock()
			if parsed.SessionID != "" {
				sessionID = parsed.SessionID
			}
			currentSessionID := sessionID
			if parsed.Conversation != "" {
				lastConversation = parsed.Conversation
				sawConversation = true
			}
			if parsed.FinalResult != "" {
				finalResult = parsed.FinalResult
			}
			mu.Unlock()
			if parsed.Conversation != "" {
				emit(AgentEvent{Type: AgentEventConversation, Content: parsed.Conversation, AgentSessionID: currentSessionID, Metadata: map[string]string{"agentSessionId": currentSessionID}})
			}
		}
	}()
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
		if err != nil {
			return err
		}
	case <-ctx.Done():
		killCommandProcessGroup(cmd)
		<-done
		return ctx.Err()
	}

	mu.Lock()
	completedSessionID := sessionID
	completedContent := finalResult
	if completedContent == "" {
		completedContent = lastConversation
	}
	shouldEmitFallbackConversation := !sawConversation && finalResult != ""
	mu.Unlock()
	if completedSessionID == "" {
		return fmt.Errorf("agent session id was not reported")
	}
	if shouldEmitFallbackConversation {
		emit(AgentEvent{Type: AgentEventConversation, Content: completedContent, AgentSessionID: completedSessionID, Metadata: map[string]string{"agentSessionId": completedSessionID}})
	}
	emit(AgentEvent{Type: AgentEventCompleted, Content: completedContent, AgentSessionID: completedSessionID})
	return nil
}

func promptForTask(task protocol.TaskPayload) string {
	prompt := task.Description
	if strings.TrimSpace(prompt) == "" {
		prompt = task.Title
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Complete task " + task.ID
	}
	if prefix := workModePromptPrefix(task.AgentConfig.WorkMode); prefix != "" {
		prompt = prefix + "\n\n" + prompt
	}
	return prompt
}

func codexConfigArgs(config domain.CodexExecutionConfig) []string {
	var args []string
	if config.Model != "" {
		args = append(args, "--model", config.Model)
	}
	if config.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+string(config.ReasoningEffort))
	}
	if config.SandboxMode != "" {
		args = append(args, "--sandbox", string(config.SandboxMode))
	}
	if config.ApprovalPolicy != "" {
		args = append(args, "--ask-for-approval", string(config.ApprovalPolicy))
	}
	if config.FullAuto {
		args = append(args, "--full-auto")
	}
	if config.BypassApprovalsAndSandbox {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	return args
}

func claudeConfigArgs(config domain.ClaudeExecutionConfig) []string {
	var args []string
	if config.Model != "" {
		args = append(args, "--model", config.Model)
	}
	if config.Effort != "" {
		args = append(args, "--effort", string(config.Effort))
	}
	if config.PermissionMode != "" {
		args = append(args, "--permission-mode", string(config.PermissionMode))
	}
	return args
}

func workModePromptPrefix(mode domain.AgentWorkMode) string {
	switch mode {
	case domain.AgentWorkModePlan:
		return "Work mode: plan. Analyze the task and produce a concrete implementation plan before making changes."
	case domain.AgentWorkModeImplement:
		return "Work mode: implement. Complete the requested implementation and verify the result."
	case domain.AgentWorkModeReview:
		return "Work mode: review. Inspect the relevant code and report findings with evidence."
	default:
		return ""
	}
}

type agentLineParse struct {
	SessionID    string
	Conversation string
	FinalResult  string
}

func parseAgentJSONLine(line string) agentLineParse {
	var value map[string]any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return agentLineParse{}
	}
	parsed := agentLineParse{SessionID: directSessionID(value)}
	switch stringField(value, "type") {
	case "system":
		return parsed
	case "assistant":
		parsed.Conversation = claudeAssistantText(value)
		return parsed
	case "result":
		parsed.FinalResult = stringField(value, "result")
		return parsed
	case "thread.started":
		if parsed.SessionID == "" {
			parsed.SessionID = stringField(value, "thread_id")
		}
		return parsed
	case "item.completed":
		if item, ok := objectField(value, "item"); ok && stringField(item, "type") == "agent_message" {
			parsed.Conversation = stringField(item, "text")
		}
		return parsed
	case "":
		parsed.Conversation = stringField(value, "message")
		return parsed
	default:
		return parsed
	}
}

func directSessionID(value map[string]any) string {
	for _, key := range []string{"session_id", "sessionId", "conversation_id", "conversationId", "thread_id", "threadId"} {
		if text := stringField(value, key); text != "" {
			return text
		}
	}
	return ""
}

func claudeAssistantText(value map[string]any) string {
	message, ok := objectField(value, "message")
	if !ok {
		return ""
	}
	content, ok := message["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range content {
		part, ok := item.(map[string]any)
		if !ok || stringField(part, "type") != "text" {
			continue
		}
		if text := stringField(part, "text"); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func objectField(value map[string]any, key string) (map[string]any, bool) {
	child, ok := value[key].(map[string]any)
	return child, ok
}

func stringField(value map[string]any, key string) string {
	text, ok := value[key].(string)
	if !ok {
		return ""
	}
	return text
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

type gitWorktreeEntry struct {
	Path   string
	Branch string
}

func removeWorktreesForBranch(ctx context.Context, repoDir, branch string) error {
	out, err := runCommandOutput(ctx, repoDir, nil, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	targetBranch := "refs/heads/" + branch
	for _, entry := range parseGitWorktreeList(out) {
		if entry.Branch != targetBranch {
			continue
		}
		if samePath(entry.Path, repoDir) {
			return fmt.Errorf("task branch %s is checked out in repository cache %s", branch, repoDir)
		}
		if err := runCommand(ctx, repoDir, nil, "git", "worktree", "remove", "--force", entry.Path); err != nil {
			return err
		}
	}
	return nil
}

func parseGitWorktreeList(out []byte) []gitWorktreeEntry {
	var entries []gitWorktreeEntry
	var current gitWorktreeEntry
	flush := func() {
		if current.Path != "" {
			entries = append(entries, current)
		}
		current = gitWorktreeEntry{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "worktree":
			if current.Path != "" {
				flush()
			}
			current.Path = value
		case "branch":
			current.Branch = value
		}
	}
	flush()
	return entries
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	if errA != nil {
		absA = a
	}
	absB, errB := filepath.Abs(b)
	if errB != nil {
		absB = b
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func runCommand(ctx context.Context, dir string, env map[string]string, name string, args ...string) error {
	_, err := runCommandOutput(ctx, dir, env, name, args...)
	return err
}

func runCommandOutput(ctx context.Context, dir string, env map[string]string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	out, err := combinedOutput(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, string(out))
	}
	return out, nil
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
