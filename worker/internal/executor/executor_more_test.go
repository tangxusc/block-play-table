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
		Task:    protocol.TaskPayload{ID: "task-1", AgentType: domain.AgentCodex, PreCommands: []string{"exit 7"}},
		Project: protocol.ProjectPayload{ID: "project-1", WorktreeNamePrefix: "p"},
	})
	if err == nil {
		t.Fatal("failing pre command should fail")
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

func TestCodexCommandAgentCapturesSessionAndResumes(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "args.txt")
	agentPath := filepath.Join(root, "fake-codex")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsFile+"'\ncase \"$*\" in\n  *resume*) printf '%s\\n' '{\"session_id\":\"codex-session\",\"message\":\"continued reply\"}' ;;\n  *) printf '%s\\n' '{\"session_id\":\"codex-session\",\"message\":\"first reply\"}' ;;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := newCodexAgent(agentPath)

	var events []AgentEvent
	if err := agent.Run(context.Background(), AgentInput{Task: protocol.TaskPayload{ID: "task-1", Title: "first prompt"}, WorktreeDir: root}, func(event AgentEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	last := events[len(events)-1]
	if last.Type != AgentEventCompleted || last.AgentSessionID != "codex-session" || last.Content != "first reply" {
		t.Fatalf("codex run events = %+v", events)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(args); !strings.Contains(got, "exec\n--skip-git-repo-check\n--json\nfirst prompt\n") {
		t.Fatalf("codex run args = %q", got)
	}

	events = nil
	if err := agent.Continue(context.Background(), AgentContinuationInput{Task: protocol.TaskPayload{ID: "task-1", AgentType: domain.AgentCodex}, WorktreeDir: root, Message: "follow up", AgentSessionID: "codex-session"}, func(event AgentEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}
	last = events[len(events)-1]
	if last.Type != AgentEventCompleted || last.AgentSessionID != "codex-session" || last.Content != "continued reply" {
		t.Fatalf("codex continue events = %+v", events)
	}
	args, err = os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(args); !strings.Contains(got, "exec\nresume\n--skip-git-repo-check\n--json\ncodex-session\nfollow up\n") {
		t.Fatalf("codex resume args = %q", got)
	}
}

func TestCodexCommandAgentAppliesExecutionConfig(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "args.txt")
	agentPath := filepath.Join(root, "fake-codex-config")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsFile+"'\nprintf '%s\\n' '{\"session_id\":\"codex-session\",\"message\":\"configured reply\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := newCodexAgent(agentPath)

	task := protocol.TaskPayload{
		ID:    "task-1",
		Title: "configured prompt",
		AgentConfig: domain.AgentExecutionConfig{
			WorkMode: domain.AgentWorkModeImplement,
			Codex: domain.CodexExecutionConfig{
				Model:           "gpt-5.4",
				ReasoningEffort: domain.CodexReasoningHigh,
				SandboxMode:     domain.CodexSandboxWorkspaceWrite,
				ApprovalPolicy:  domain.CodexApprovalNever,
				FullAuto:        true,
			},
		},
	}
	if err := agent.Run(context.Background(), AgentInput{Task: task, WorktreeDir: root}, func(event AgentEvent) {}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{
		"--model\ngpt-5.4\n",
		"-c\nmodel_reasoning_effort=high\n",
		"--sandbox\nworkspace-write\n",
		"--ask-for-approval\nnever\n",
		"--full-auto\n",
		"exec\n--skip-git-repo-check\n--json\n",
		"Work mode: implement. Complete the requested implementation and verify the result.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("codex configured args missing %q in %q", want, got)
		}
	}
}

func TestParseAgentJSONLineReadsCodexThreadAndAgentMessage(t *testing.T) {
	sessionID, message := parseAgentJSONLine(`{"type":"thread.started","thread_id":"codex-thread"}`)
	if sessionID != "codex-thread" || message != "" {
		t.Fatalf("thread.started parsed session=%q message=%q", sessionID, message)
	}

	sessionID, message = parseAgentJSONLine(`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"codex reply"}}`)
	if sessionID != "" || message != "codex reply" {
		t.Fatalf("item.completed parsed session=%q message=%q", sessionID, message)
	}
}

func TestClaudeCommandAgentUsesGeneratedSessionAndResumes(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "args.txt")
	agentPath := filepath.Join(root, "fake-claude")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsFile+"'\ncase \"$*\" in\n  *--resume*) printf '%s\\n' '{\"type\":\"assistant\",\"session_id\":\"claude-session\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"continued reply\"}]}}' ;;\n  *) printf '%s\\n' '{\"type\":\"assistant\",\"session_id\":\"claude-session\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"first reply\"}]}}' ;;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := newClaudeAgent(agentPath, func() string { return "claude-session" })

	var events []AgentEvent
	if err := agent.Run(context.Background(), AgentInput{Task: protocol.TaskPayload{ID: "task-1", Title: "first prompt"}, WorktreeDir: root}, func(event AgentEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	last := events[len(events)-1]
	if last.Type != AgentEventCompleted || last.AgentSessionID != "claude-session" || last.Content != "first reply" {
		t.Fatalf("claude run events = %+v", events)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(args); !strings.Contains(got, "-p\n--output-format=stream-json\n--verbose\n--session-id\nclaude-session\nfirst prompt\n") {
		t.Fatalf("claude run args = %q", got)
	}

	events = nil
	if err := agent.Continue(context.Background(), AgentContinuationInput{Task: protocol.TaskPayload{ID: "task-1", AgentType: domain.AgentClaude}, WorktreeDir: root, Message: "follow up", AgentSessionID: "claude-session"}, func(event AgentEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}
	last = events[len(events)-1]
	if last.Type != AgentEventCompleted || last.AgentSessionID != "claude-session" || last.Content != "continued reply" {
		t.Fatalf("claude continue events = %+v", events)
	}
	args, err = os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(args); !strings.Contains(got, "-p\n--output-format=stream-json\n--verbose\n--resume\nclaude-session\nfollow up\n") {
		t.Fatalf("claude resume args = %q", got)
	}
}

func TestClaudeCommandAgentAppliesExecutionConfig(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "args.txt")
	agentPath := filepath.Join(root, "fake-claude-config")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsFile+"'\nprintf '%s\\n' '{\"type\":\"assistant\",\"session_id\":\"claude-session\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"configured reply\"}]}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := newClaudeAgent(agentPath, func() string { return "claude-session" })
	task := protocol.TaskPayload{
		ID:    "task-1",
		Title: "configured prompt",
		AgentConfig: domain.AgentExecutionConfig{
			WorkMode: domain.AgentWorkModeReview,
			Claude: domain.ClaudeExecutionConfig{
				Model:          "sonnet",
				Effort:         domain.ClaudeEffortMax,
				PermissionMode: domain.ClaudePermissionPlan,
			},
		},
	}
	if err := agent.Run(context.Background(), AgentInput{Task: task, WorktreeDir: root}, func(event AgentEvent) {}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{
		"-p\n--model\nsonnet\n",
		"--effort\nmax\n",
		"--permission-mode\nplan\n",
		"--output-format=stream-json\n--verbose\n--session-id\nclaude-session\n",
		"Work mode: review. Inspect the relevant code and report findings with evidence.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("claude configured args missing %q in %q", want, got)
		}
	}
}

func TestSessionCommandAgentFailsWithoutSessionID(t *testing.T) {
	root := t.TempDir()
	agentPath := filepath.Join(root, "fake-codex-no-session")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\nprintf '%s\\n' '{\"message\":\"reply without session\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := newCodexAgent(agentPath)
	if err := agent.Run(context.Background(), AgentInput{Task: protocol.TaskPayload{ID: "task-1", Title: "prompt"}, WorktreeDir: root}, func(event AgentEvent) {}); err == nil {
		t.Fatal("Run should fail when CLI output has no session id")
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
		Task:    protocol.TaskPayload{ID: "task-1"},
		Project: protocol.ProjectPayload{GitURL: repo, DefaultBranch: "main", WorktreeNamePrefix: "p"},
	})
	if err != nil {
		t.Fatalf("prepareWorktree returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatalf("cloned README missing: %v", err)
	}
	runGit(t, worktree, "rev-parse", "--is-inside-work-tree")
	if branch := gitOutput(t, worktree, "branch", "--show-current"); branch != "task/task-1" {
		t.Fatalf("worktree branch = %q, want task/task-1", branch)
	}
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
	agent, ok := exec.agents[domain.AgentCodex].(*SessionCommandAgent)
	if !ok {
		t.Fatalf("default codex agent type = %T", exec.agents[domain.AgentCodex])
	}
	if agent.agentType != domain.AgentCodex {
		t.Fatalf("default codex agent type = %s, want codex", agent.agentType)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
