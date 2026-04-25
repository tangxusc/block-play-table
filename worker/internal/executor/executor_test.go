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

func TestExecutorRunsSetupCommandsAgentAndPostCommands(t *testing.T) {
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
			TargetBranch: "task/task-1",
			PreCommands:  []string{"printf pre > pre.txt"},
			PostCommands: []string{"printf post > post.txt"},
		},
		Project: protocol.ProjectPayload{
			ID:                 "project-1",
			GitURL:             repo,
			DefaultBranch:      "main",
			WorktreeNamePrefix: "block-play-table",
			SetupCommands:      []string{"printf setup > setup.txt"},
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
			Task:    protocol.TaskPayload{ID: "task-1", AgentType: domain.AgentCodex, TargetBranch: "task/t"},
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
