package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestExecutorReportsAgentFailureDefaultResultPostFailureAndCanceledErrors(t *testing.T) {
	var events []protocol.WorkerEvent
	runner := NewExecutor(Config{
		WorkerID: "worker-1",
		WorkDir:  t.TempDir(),
		Agents: map[domain.AgentType]Agent{domain.AgentCodex: AgentFunc(func(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
			emit(AgentEvent{Type: AgentEventFailed, Content: "failed with secret-token"})
			return nil
		})},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			events = append(events, event)
			return nil
		}),
	})
	err := runner.Execute(context.Background(), protocol.TaskStartPayload{
		Task:            protocol.TaskPayload{ID: "task-agent-failed", AgentType: domain.AgentCodex},
		Project:         protocol.ProjectPayload{ID: "project-1", WorktreeNamePrefix: "p"},
		AgentRuntimeEnv: []protocol.RuntimeEnvVar{{Key: "TOKEN", Value: "secret-token", Sensitive: true}},
	})
	if err == nil {
		t.Fatal("agent failed event should fail execution")
	}
	if events[len(events)-1].Type != protocol.MessageTaskFailed || events[len(events)-1].Result != "failed with ********" {
		t.Fatalf("agent failed event = %+v", events[len(events)-1])
	}

	events = nil
	runner = NewExecutor(Config{
		WorkerID: "worker-1",
		WorkDir:  t.TempDir(),
		Agents: map[domain.AgentType]Agent{domain.AgentCodex: AgentFunc(func(context.Context, AgentInput, func(AgentEvent)) error {
			return nil
		})},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			events = append(events, event)
			return nil
		}),
	})
	if err := runner.Execute(context.Background(), protocol.TaskStartPayload{
		Task:    protocol.TaskPayload{ID: "task-default-result", AgentType: domain.AgentCodex},
		Project: protocol.ProjectPayload{ID: "project-1", WorktreeNamePrefix: "p"},
	}); err != nil {
		t.Fatalf("default result execution returned error: %v", err)
	}
	if events[len(events)-1].Type != protocol.MessageTaskCompleted || events[len(events)-1].Result != "completed" {
		t.Fatalf("default completion event = %+v", events[len(events)-1])
	}

	events = nil
	runner = NewExecutor(Config{
		WorkerID: "worker-1",
		WorkDir:  t.TempDir(),
		Agents: map[domain.AgentType]Agent{domain.AgentCodex: AgentFunc(func(context.Context, AgentInput, func(AgentEvent)) error {
			return nil
		})},
		Reporter: ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			events = append(events, event)
			return nil
		}),
	})
	err = runner.Execute(context.Background(), protocol.TaskStartPayload{
		Task:    protocol.TaskPayload{ID: "task-post-failed", AgentType: domain.AgentCodex, PostCommands: []string{"exit 9"}},
		Project: protocol.ProjectPayload{ID: "project-1", WorktreeNamePrefix: "p"},
	})
	if err == nil {
		t.Fatal("failing post command should fail execution")
	}
	if events[len(events)-1].Type != protocol.MessageTaskFailed {
		t.Fatalf("post command failure event = %+v", events[len(events)-1])
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events = nil
	runner.reportExecutionError(ctx, "task-canceled", errors.New("boom"))
	if len(events) != 1 || events[0].Type != protocol.MessageTaskInterrupted {
		t.Fatalf("canceled report event = %+v", events)
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

func TestExecutorHelperFallbacksAndCancellation(t *testing.T) {
	env := runtimeEnv([]protocol.RuntimeEnvVar{{Key: " "}, {Key: "TOKEN", Value: "secret"}})
	if len(env) != 1 || env["TOKEN"] != "secret" {
		t.Fatalf("runtimeEnv = %#v", env)
	}
	redact := redactor([]protocol.RuntimeEnvVar{
		{Key: "TOKEN", Value: "secret", Sensitive: true},
		{Key: "PUBLIC", Value: "visible"},
		{Key: "EMPTY", Sensitive: true},
	})
	if got := redact("secret visible"); got != "******** visible" {
		t.Fatalf("redacted = %q", got)
	}
	if !looksLikeGitRepo("https://example.com/repo.git") {
		t.Fatal("https git URL should look like a git repo")
	}
	localRepo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(localRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !looksLikeGitRepo(localRepo) {
		t.Fatal("local directory with .git should look like a git repo")
	}
	if name := worktreeName(protocol.TaskStartPayload{Task: protocol.TaskPayload{ID: "task with spaces"}}); name == "" || name == "task with spaces" {
		t.Fatalf("worktreeName = %q", name)
	}
	if name := repositoryCacheName(protocol.ProjectPayload{GitURL: "git://example/repo"}); name == "" {
		t.Fatal("repositoryCacheName should not be empty")
	}
	if got := safePathPart("..."); got != "item" {
		t.Fatalf("empty safe path = %q", got)
	}
	if got := safePathPart(strings.Repeat("a", 100)); len(got) > 80 {
		t.Fatalf("safe path length = %d, want <= 80", len(got))
	}
	var wg sync.WaitGroup
	wg.Add(1)
	scanPipe(&wg, 42, func(string) { t.Fatal("non-reader should not emit") })
	wg.Wait()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := combinedOutput(ctx, exec.Command("sh", "-c", "sleep 1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("combinedOutput canceled error = %v", err)
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
	runGit(t, worktree, "rev-parse", "--is-inside-work-tree")
	runGit(t, worktree, "branch", "--show-current")
}

func TestResolveGitRefAndRepositoryCacheErrors(t *testing.T) {
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
	runGit(t, repo, "update-ref", "refs/remotes/origin/feature", "HEAD")

	if got := resolveGitRef(context.Background(), repo, ""); got != "HEAD" {
		t.Fatalf("empty ref = %q", got)
	}
	if got := resolveGitRef(context.Background(), repo, "main"); got != "main" {
		t.Fatalf("main ref = %q", got)
	}
	if got := resolveGitRef(context.Background(), repo, "feature"); got != "origin/feature" {
		t.Fatalf("remote ref = %q", got)
	}
	if got := resolveGitRef(context.Background(), repo, "missing"); got != "missing" {
		t.Fatalf("missing ref = %q", got)
	}

	cacheDir := filepath.Join(root, "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := NewExecutor(Config{WorkDir: root})
	if err := runner.ensureRepositoryCache(context.Background(), "git://example/repo", cacheDir); err == nil {
		t.Fatal("existing non-git cache directory should fail")
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
