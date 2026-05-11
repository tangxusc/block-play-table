package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

func TestExecutorRunsPreCommandsAgentAndPostCommands(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	command := protocol.TaskStartPayload{
		Task: protocol.TaskPayload{
			ID:           "task-1",
			Title:        "Task",
			AgentType:    domain.AgentCodex,
			BaseBranch:   "main",
			PreCommands:  []string{"printf pre > pre.txt"},
			PostCommands: []string{"printf post > post.txt"},
		},
		Project: protocol.ProjectPayload{
			ID:                 "project-1",
			GitURL:             repo,
			DefaultBranch:      "main",
			WorktreeNamePrefix: "block-play-table",
		},
		AgentRuntimeEnv: []protocol.RuntimeEnvVar{{Key: "BPT_TEST_ENV", Value: "present"}},
	}

	var events []protocol.WorkerEvent
	exec := NewExecutor(Config{
		WorkDir: root,
		Agents: map[domain.AgentType]Agent{
			domain.AgentCodex: AgentFunc(func(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
				if input.Env["BPT_TEST_ENV"] != "present" {
					t.Fatalf("agent env missing BPT_TEST_ENV")
				}
				emit(AgentEvent{Type: AgentEventStdout, Content: "agent output"})
				emit(AgentEvent{Type: AgentEventConversation, Content: "assistant reply"})
				emit(AgentEvent{Type: AgentEventCompleted, Content: "agent result"})
				return nil
			}),
		},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			events = append(events, event)
			return nil
		}),
	})

	if err := exec.Execute(context.Background(), command); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(events) < 5 {
		t.Fatalf("events = %d, want start/log/conversation/completed events", len(events))
	}
	if events[0].Type != protocol.MessageTaskStarted {
		t.Fatalf("first event type = %s", events[0].Type)
	}
	if events[len(events)-1].Type != protocol.MessageTaskCompleted {
		t.Fatalf("last event type = %s", events[len(events)-1].Type)
	}
	worktree := events[0].Content
	for _, name := range []string{"pre.txt", "post.txt"} {
		if _, err := os.Stat(filepath.Join(worktree, name)); err != nil {
			t.Fatalf("%s was not created in worktree %s: %v", name, worktree, err)
		}
	}
	var sawResult bool
	for _, event := range events {
		if event.Type == protocol.MessageTaskResult && event.Result == "agent result" {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("events did not include agent result: %+v", events)
	}
}

func TestExecutorMirrorsConversationEventsToAssistantLogs(t *testing.T) {
	root := t.TempDir()
	exec := NewExecutor(Config{
		WorkDir: root,
		Agents: map[domain.AgentType]Agent{
			domain.AgentCodex: AgentFunc(func(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
				emit(AgentEvent{Type: AgentEventConversation, Content: "assistant-only output"})
				emit(AgentEvent{Type: AgentEventCompleted, Content: "done"})
				return nil
			}),
		},
	})

	var events []protocol.WorkerEvent
	exec.reporter = ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
		events = append(events, event)
		return nil
	})
	if err := exec.Execute(context.Background(), protocol.TaskStartPayload{
		Task:    protocol.TaskPayload{ID: "task-conversation-log", AgentType: domain.AgentCodex},
		Project: protocol.ProjectPayload{ID: "project-1", DefaultBranch: "main", WorktreeNamePrefix: "p"},
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var sawConversation bool
	var assistantLogs []string
	for _, event := range events {
		switch event.Type {
		case protocol.MessageTaskConversation:
			if event.Content == "assistant-only output" {
				sawConversation = true
			}
		case protocol.MessageTaskLog:
			if event.Stream == "assistant" {
				assistantLogs = append(assistantLogs, event.Content)
			}
		}
	}
	if !sawConversation {
		t.Fatalf("events did not include conversation: %+v", events)
	}
	if len(assistantLogs) != 1 || assistantLogs[0] != "assistant-only output" {
		t.Fatalf("assistant logs = %#v, want conversation mirror", assistantLogs)
	}
}

func TestExecutorRecordsReviewTurnAroundAgentWork(t *testing.T) {
	root := t.TempDir()
	recorder := &recordingReviewRecorder{}
	exec := NewExecutor(Config{
		WorkDir:        root,
		ReviewRecorder: recorder,
		Agents: map[domain.AgentType]Agent{
			domain.AgentCodex: AgentFunc(func(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
				if recorder.beginWorktree == "" || recorder.beginTaskID != input.Task.ID {
					t.Fatalf("review turn was not begun before agent run: %+v", recorder)
				}
				emit(AgentEvent{Type: AgentEventCompleted, Content: "done"})
				return nil
			}),
		},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error { return nil }),
	})

	if err := exec.Execute(context.Background(), protocol.TaskStartPayload{
		Task: protocol.TaskPayload{ID: "task-review-turn", AgentType: domain.AgentCodex, BaseBranch: "main"},
		Project: protocol.ProjectPayload{
			ID:                 "project-1",
			DefaultBranch:      "main",
			WorktreeNamePrefix: "p",
		},
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if recorder.endTaskID != "task-review-turn" || recorder.endToken != "token-1" {
		t.Fatalf("review recorder end = task %q token %q, want task-review-turn token-1", recorder.endTaskID, recorder.endToken)
	}
}

func TestExecutorInterruptStopsLongRunningAgent(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	exec := NewExecutor(Config{
		WorkDir: root,
		Agents: map[domain.AgentType]Agent{
			domain.AgentCodex: AgentFunc(func(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}),
		},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error { return nil }),
	})

	done := make(chan error, 1)
	go func() {
		done <- exec.Execute(context.Background(), protocol.TaskStartPayload{
			Task:    protocol.TaskPayload{ID: "task-1", AgentType: domain.AgentCodex},
			Project: protocol.ProjectPayload{ID: "project-1", GitURL: root, DefaultBranch: "main", WorktreeNamePrefix: "p"},
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("agent did not start")
	}
	exec.Interrupt("task-1")
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("Execute should return interruption error")
		}
	case <-time.After(time.Second):
		t.Fatal("executor did not stop after interrupt")
	}
}

type recordingReviewRecorder struct {
	beginTaskID   string
	beginWorktree string
	beginBase     string
	beginDefault  string
	endTaskID     string
	endToken      string
}

func (r *recordingReviewRecorder) BeginTurn(ctx context.Context, taskID, worktreePath, baseBranch, defaultBranch string) string {
	r.beginTaskID = taskID
	r.beginWorktree = worktreePath
	r.beginBase = baseBranch
	r.beginDefault = defaultBranch
	return "token-1"
}

func (r *recordingReviewRecorder) EndTurn(ctx context.Context, taskID, token string) {
	r.endTaskID = taskID
	r.endToken = token
}

func TestExecutorContinuesExistingAgentSessionWithoutCommandsOrWorktree(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "existing-worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := &recordingContinuationAgent{t: t}
	var events []protocol.WorkerEvent
	exec := NewExecutor(Config{
		WorkerID: "worker-1",
		WorkDir:  root,
		Agents: map[domain.AgentType]Agent{
			domain.AgentCodex: agent,
		},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			events = append(events, event)
			return nil
		}),
	})

	err := exec.Continue(context.Background(), protocol.TaskContinuePayload{
		Task:           protocol.TaskPayload{ID: "task-continue", Title: "T", AgentType: domain.AgentCodex, PreCommands: []string{"printf pre > pre.txt"}, PostCommands: []string{"printf post > post.txt"}},
		Message:        "follow up",
		AgentSessionID: "session-1",
		WorktreePath:   worktree,
	})
	if err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}
	if !agent.continued {
		t.Fatal("agent Continue was not called")
	}
	for _, name := range []string{"pre.txt", "post.txt"} {
		if _, err := os.Stat(filepath.Join(worktree, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should not be created during continuation: %v", name, err)
		}
	}
	if len(events) < 2 || events[0].Type != protocol.MessageTaskStarted || events[0].Content != worktree {
		t.Fatalf("events should start existing worktree, got %+v", events)
	}
	var assistantLogs []string
	for _, event := range events {
		if event.Type == protocol.MessageTaskLog && event.Stream == "assistant" {
			assistantLogs = append(assistantLogs, event.Content)
		}
	}
	if len(assistantLogs) != 1 || assistantLogs[0] != "continued reply" {
		t.Fatalf("assistant logs = %#v, want continued conversation mirror", assistantLogs)
	}
	last := events[len(events)-1]
	if last.Type != protocol.MessageTaskCompleted || last.Result != "continued result" || last.AgentSessionID != "session-1" {
		t.Fatalf("last event = %+v", last)
	}
}

type recordingContinuationAgent struct {
	t         *testing.T
	continued bool
}

func (a *recordingContinuationAgent) Run(context.Context, AgentInput, func(AgentEvent)) error {
	a.t.Fatal("Run should not be called for continuation")
	return nil
}

func (a *recordingContinuationAgent) Continue(ctx context.Context, input AgentContinuationInput, emit func(AgentEvent)) error {
	a.continued = true
	if input.AgentSessionID != "session-1" || input.Message != "follow up" || input.WorktreeDir == "" {
		a.t.Fatalf("continuation input = %+v", input)
	}
	emit(AgentEvent{Type: AgentEventConversation, Content: "continued reply", AgentSessionID: input.AgentSessionID})
	emit(AgentEvent{Type: AgentEventCompleted, Content: "continued result", AgentSessionID: input.AgentSessionID})
	return nil
}

func TestExecutorInterruptStopsCommandAgentProcessTree(t *testing.T) {
	root := t.TempDir()
	agentPath := filepath.Join(root, "fake-agent")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\necho started\nsleep 30\necho done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	var events []protocol.WorkerEvent
	exec := NewExecutor(Config{
		WorkDir: root,
		Agents: map[domain.AgentType]Agent{
			domain.AgentCodex: NewCommandAgent(agentPath, nil),
		},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			events = append(events, event)
			if event.Type == protocol.MessageTaskLog && event.Content == "started" {
				select {
				case <-started:
				default:
					close(started)
				}
			}
			return nil
		}),
	})

	done := make(chan error, 1)
	go func() {
		done <- exec.Execute(context.Background(), protocol.TaskStartPayload{
			Task:    protocol.TaskPayload{ID: "task-process-tree", Title: "T", AgentType: domain.AgentCodex},
			Project: protocol.ProjectPayload{ID: "project-1", WorktreeNamePrefix: "p"},
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("command agent did not start")
	}
	exec.Interrupt("task-process-tree")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Execute should return interruption error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not stop command process tree after interrupt")
	}
	if events[len(events)-1].Type != protocol.MessageTaskInterrupted {
		t.Fatalf("last event = %s, want interrupted", events[len(events)-1].Type)
	}
}
