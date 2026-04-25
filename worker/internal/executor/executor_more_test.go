package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

func TestExecutorReportsFailureForUnsupportedAgentAndFailingCommand(t *testing.T) {
	exec := NewExecutor(Config{WorkDir: t.TempDir(), Agents: map[domain.AgentType]Agent{}})
	if err := exec.Execute(context.Background(), protocol.TaskStartPayload{Task: protocol.TaskPayload{ID: "task-1", AgentType: domain.AgentType("bad")}}); err == nil {
		t.Fatal("unsupported agent should fail")
	}

	var events []protocol.WorkerEvent
	exec = NewExecutor(Config{
		WorkDir: t.TempDir(),
		Agents:  map[domain.AgentType]Agent{domain.AgentCodex: AgentFunc(func(context.Context, AgentInput, func(AgentEvent)) error { return nil })},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			events = append(events, event)
			return nil
		}),
	})
	err := exec.Execute(context.Background(), protocol.TaskStartPayload{
		Task:    protocol.TaskPayload{ID: "task-1", AgentType: domain.AgentCodex},
		Project: protocol.ProjectPayload{ID: "project-1", WorktreeNamePrefix: "p", SetupCommands: []string{"exit 7"}},
	})
	if err == nil {
		t.Fatal("failing setup command should fail")
	}
	if events[len(events)-1].Type != protocol.MessageTaskFailed {
		t.Fatalf("last event = %s, want failed", events[len(events)-1].Type)
	}
}

func TestCommandAgentRunsProcessAndEmitsOutput(t *testing.T) {
	agent := NewCommandAgent("sh", []string{"-c", "printf out; printf err >&2"})
	var events []AgentEvent
	err := agent.Run(context.Background(), AgentInput{Task: protocol.TaskPayload{ID: "task-1", Title: "ignored"}, WorktreeDir: t.TempDir()}, func(event AgentEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected stdout/stderr events")
	}
}

func TestPrepareWorktreeClonesGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "init")

	exec := NewExecutor(Config{WorkDir: filepath.Join(root, "worker")})
	worktree, err := exec.prepareWorktree(context.Background(), protocol.TaskStartPayload{
		Task:    protocol.TaskPayload{ID: "task-1", TargetBranch: "task/t"},
		Project: protocol.ProjectPayload{GitURL: repo, DefaultBranch: "main", WorktreeNamePrefix: "p"},
	})
	if err != nil {
		t.Fatalf("prepareWorktree returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatalf("cloned README missing: %v", err)
	}
}

func TestReporterFuncAndAgentFuncPropagateErrors(t *testing.T) {
	want := errors.New("boom")
	if err := ReporterFunc(func(context.Context, protocol.WorkerEvent) error { return want }).Report(context.Background(), protocol.WorkerEvent{}); !errors.Is(err, want) {
		t.Fatalf("ReporterFunc error = %v", err)
	}
	if err := AgentFunc(func(context.Context, AgentInput, func(AgentEvent)) error { return want }).Run(context.Background(), AgentInput{}, func(AgentEvent) {}); !errors.Is(err, want) {
		t.Fatalf("AgentFunc error = %v", err)
	}
}

func TestDefaultCodexAgentAllowsNonGitWorkdir(t *testing.T) {
	exec := NewExecutor(Config{WorkDir: t.TempDir()})
	agent, ok := exec.agents[domain.AgentCodex].(*CommandAgent)
	if !ok {
		t.Fatalf("default codex agent type = %T", exec.agents[domain.AgentCodex])
	}
	for _, arg := range agent.args {
		if arg == "--skip-git-repo-check" {
			return
		}
	}
	t.Fatalf("codex args = %v, want --skip-git-repo-check", agent.args)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
}
