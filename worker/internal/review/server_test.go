package review

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestServerDiffStageDiscardRestoreAndLastTurn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "task-worktree")
	initGitRepo(t, repo)

	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	server.RegisterTask(TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})

	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := requestDiff(t, server, "task-1", ScopeUncommitted, repo)
	if !diff.HasFile("tracked.txt") || !diff.HasFile("new.txt") {
		t.Fatalf("uncommitted files = %+v, want tracked.txt and new.txt", diff.Files)
	}
	branch := requestDiff(t, server, "task-1", ScopeBranch, repo)
	if !branch.HasFile("tracked.txt") || branch.BaseRef == "" || branch.HeadRef == "" {
		t.Fatalf("branch diff = %+v, want tracked.txt with base/head refs", branch)
	}

	requestAction(t, server, "task-1", "stage", ActionRequest{Paths: []string{"tracked.txt"}, WorktreePath: repo})
	staged := requestDiff(t, server, "task-1", ScopeUncommitted, repo)
	tracked := staged.File("tracked.txt")
	if tracked == nil || !tracked.Staged || !strings.Contains(tracked.Patch, "+changed") {
		t.Fatalf("tracked staged diff = %+v, want staged patch with +changed", tracked)
	}
	stagedOnly := requestDiffWithStaged(t, server, "task-1", ScopeUncommitted, repo, boolPtr(true))
	if !stagedOnly.HasFile("tracked.txt") || stagedOnly.HasFile("new.txt") {
		t.Fatalf("staged-only diff = %+v, want tracked.txt without untracked new.txt", stagedOnly.Files)
	}
	unstagedOnly := requestDiffWithStaged(t, server, "task-1", ScopeUncommitted, repo, boolPtr(false))
	if unstagedOnly.HasFile("tracked.txt") || !unstagedOnly.HasFile("new.txt") {
		t.Fatalf("unstaged-only diff = %+v, want untracked new.txt without tracked.txt", unstagedOnly.Files)
	}
	requestAction(t, server, "task-1", "unstage", ActionRequest{Paths: []string{"tracked.txt"}, WorktreePath: repo})
	afterUnstage := requestDiffWithStaged(t, server, "task-1", ScopeUncommitted, repo, boolPtr(true))
	if afterUnstage.HasFile("tracked.txt") {
		t.Fatalf("staged diff after unstage = %+v, want tracked.txt removed", afterUnstage.Files)
	}

	backup := requestAction(t, server, "task-1", "discard", ActionRequest{Paths: []string{"tracked.txt", "new.txt"}, WorktreePath: repo})
	if backup.Backup == nil || backup.Backup.ID == "" {
		t.Fatalf("discard backup = %+v, want backup id", backup.Backup)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt after discard err = %v, want removed", err)
	}
	afterDiscard, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterDiscard) != "base\n" {
		t.Fatalf("tracked after discard = %q, want base", afterDiscard)
	}

	requestAction(t, server, "task-1", "restore", ActionRequest{BackupID: backup.Backup.ID, WorktreePath: repo})
	restored, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "base\nchanged\n" {
		t.Fatalf("tracked after restore = %q, want changed content", restored)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatalf("new.txt after restore err = %v, want restored", err)
	}

	token := server.BeginTurn(ctx, TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\nchanged\nlast turn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server.EndTurn(ctx, "task-1", token)
	lastTurn := requestDiff(t, server, "task-1", ScopeLastTurn, repo)
	last := lastTurn.File("tracked.txt")
	if last == nil || !strings.Contains(last.Patch, "+last turn") {
		t.Fatalf("last-turn diff = %+v, want +last turn", last)
	}
}

func TestServerStartsReviewRunWithStructuredFindings(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "task-worktree")
	initGitRepo(t, repo)
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	server.RegisterTask(TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main", AgentType: "codex"})
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(map[string]any{"scope": "UNCOMMITTED"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/review/tasks/task-1/runs", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runs response = %d %q", rec.Code, rec.Body.String())
	}
	var response ReviewRunResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Run.ID == "" || response.Run.Status != ReviewRunCompleted || response.Run.Scope != ScopeUncommitted || response.Run.AgentType != "codex" {
		t.Fatalf("review run = %+v", response.Run)
	}
	if len(response.Findings) != 1 || response.Findings[0].Path != "tracked.txt" || response.Findings[0].Status != FindingOpen {
		t.Fatalf("review findings = %+v", response.Findings)
	}
	if !strings.Contains(response.Run.RawResult, "tracked.txt") || response.Run.StartedAt == nil || response.Run.CompletedAt == nil {
		t.Fatalf("review run raw/times = %+v", response.Run)
	}
}

func TestServerGitCommandCommitPushAndFastForwardPublish(t *testing.T) {
	root := t.TempDir()
	remote, repo := initRemoteReviewWorktree(t, root, "task-1")
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	server.RegisterTask(TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})

	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\nreview change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "tracked.txt")

	commit := requestGitCommand(t, server, "task-1", GitCommandRequest{
		WorktreePath: repo,
		Command:      GitCommandCommit,
		Message:      "review commit",
	})
	if !commit.OK || commit.Command != GitCommandCommit || commit.HeadRef == "" || !strings.Contains(gitOutputTest(t, repo, "log", "-1", "--pretty=%s"), "review commit") {
		t.Fatalf("commit response = %+v", commit)
	}
	if status := gitOutputTest(t, repo, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("status after commit = %q, want clean", status)
	}

	push := requestGitCommand(t, server, "task-1", GitCommandRequest{
		WorktreePath: repo,
		Command:      GitCommandPushBranch,
	})
	if !push.OK || !strings.Contains(push.Output, "task/task-1") {
		t.Fatalf("push branch response = %+v", push)
	}

	publish := requestGitCommand(t, server, "task-1", GitCommandRequest{
		WorktreePath:    repo,
		Command:         GitCommandPublish,
		PublishStrategy: GitPublishFastForward,
	})
	if !publish.OK || publish.BaseRef == "" || publish.HeadRef == "" {
		t.Fatalf("publish response = %+v", publish)
	}
	if got := gitOutputTest(t, remote, "log", "-1", "--pretty=%s", "main"); got != "review commit" {
		t.Fatalf("remote main subject = %q, want review commit", got)
	}
}

func TestServerGitCommandRebaseMergeAndMergeCommitPublish(t *testing.T) {
	root := t.TempDir()
	remote, repo := initRemoteReviewWorktree(t, root, "task-1")
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	server.RegisterTask(TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})

	advanceRemoteMain(t, root, remote, "base.txt", "base side\n", "base side")
	fetch := requestGitCommand(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandFetch})
	if !fetch.OK || fetch.HeadRef == "" {
		t.Fatalf("fetch response = %+v", fetch)
	}
	rebase := requestGitCommand(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandRebase})
	if !rebase.OK || !strings.Contains(gitOutputTest(t, repo, "log", "--pretty=%s", "-1"), "base") {
		t.Fatalf("rebase response = %+v", rebase)
	}

	advanceRemoteMain(t, root, remote, "merge-base.txt", "merge base\n", "merge base side")
	merge := requestGitCommand(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandMergeBase})
	if !merge.OK || !strings.Contains(merge.Output, "merge") {
		t.Fatalf("merge base response = %+v", merge)
	}

	if err := os.WriteFile(filepath.Join(repo, "task.txt"), []byte("task side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "task.txt")
	requestGitCommand(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandCommit, Message: "task side"})
	advanceRemoteMain(t, root, remote, "remote.txt", "remote side\n", "remote side")

	failedPublish := requestGitCommandError(t, server, "task-1", GitCommandRequest{
		WorktreePath:    repo,
		Command:         GitCommandPublish,
		PublishStrategy: GitPublishFastForward,
	})
	if !strings.Contains(failedPublish, "fast-forward") {
		t.Fatalf("fast-forward publish error = %q, want fast-forward", failedPublish)
	}

	publish := requestGitCommand(t, server, "task-1", GitCommandRequest{
		WorktreePath:    repo,
		Command:         GitCommandPublish,
		PublishStrategy: GitPublishMergeCommit,
	})
	if !publish.OK || !strings.Contains(gitOutputTest(t, remote, "show", "main:task.txt"), "task side") || !strings.Contains(gitOutputTest(t, remote, "show", "main:remote.txt"), "remote side") {
		t.Fatalf("merge commit publish response = %+v", publish)
	}
}

func TestServerGitCommandPullAndValidation(t *testing.T) {
	root := t.TempDir()
	_, repo := initRemoteReviewWorktree(t, root, "task-1")
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	server.RegisterTask(TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})

	pull := requestGitCommand(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandPull})
	if !pull.OK || pull.HeadRef == "" {
		t.Fatalf("pull response = %+v", pull)
	}
	if got := requestGitCommandError(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandFetch, Remote: "origin;rm"}); !strings.Contains(got, "invalid remote") {
		t.Fatalf("invalid remote response = %q", got)
	}
	if got := requestGitCommandError(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandRebase, Branch: "../main"}); !strings.Contains(got, "invalid branch") {
		t.Fatalf("invalid branch response = %q", got)
	}
	if got := requestGitCommandError(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandCommit, Message: "   "}); !strings.Contains(got, "commit message is required") {
		t.Fatalf("empty commit message response = %q", got)
	}
	if got := requestGitCommandError(t, server, "task-1", GitCommandRequest{WorktreePath: repo, Command: GitCommandCommit, Message: "empty"}); !strings.Contains(got, "no staged changes") {
		t.Fatalf("empty commit response = %q", got)
	}
}

func TestServerLifecycleCapabilitiesAndHTTPNotFound(t *testing.T) {
	disabled := NewServer(Config{})
	if err := disabled.Start(context.Background()); err != nil {
		t.Fatalf("disabled Start returned error: %v", err)
	}
	if err := disabled.Close(); err != nil {
		t.Fatalf("disabled Close returned error: %v", err)
	}
	if caps := disabled.Capabilities(); caps["review_enabled"] != "false" {
		t.Fatalf("disabled capabilities = %+v", caps)
	}

	missingWorkDir := NewServer(Config{Enabled: true})
	if err := missingWorkDir.Start(context.Background()); err == nil {
		t.Fatal("Start should require work dir when review server is enabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(Config{Enabled: true, WorkDir: t.TempDir(), Host: "127.0.0.1"})
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()
	if server.Addr() == "" || server.Port() == 0 {
		t.Fatalf("server address = %q port = %d", server.Addr(), server.Port())
	}
	caps := server.Capabilities()
	if caps["review_enabled"] != "true" || caps["review_host"] != "127.0.0.1" || caps["review_port"] != strconv.Itoa(server.Port()) {
		t.Fatalf("enabled capabilities = %+v", caps)
	}
	resp, err := http.Get("http://" + server.Addr() + "/review/tasks/task-1")
	if err != nil {
		t.Fatalf("GET started server returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("not found response = %d", resp.StatusCode)
	}
}

func TestServerRejectsMalformedReviewRequestsAndFailedRun(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "task-worktree")
	initGitRepo(t, repo)
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	server.RegisterTask(TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
		want   string
	}{
		{name: "bad staged filter", method: http.MethodGet, path: "/review/tasks/task-1/diff?scope=UNCOMMITTED&staged=maybe&cwd=" + repo, status: http.StatusBadRequest, want: "staged"},
		{name: "method not allowed", method: http.MethodPost, path: "/review/tasks/task-1/diff", body: `{}`, status: http.StatusMethodNotAllowed, want: "method not allowed"},
		{name: "unsupported scope", method: http.MethodGet, path: "/review/tasks/task-1/diff?scope=BAD&cwd=" + repo, status: http.StatusBadRequest, want: "unsupported diff scope"},
		{name: "missing last turn", method: http.MethodGet, path: "/review/tasks/task-1/diff?scope=LAST_TURN&cwd=" + repo, status: http.StatusConflict, want: "last-turn diff is not available"},
		{name: "bad action json", method: http.MethodPost, path: "/review/tasks/task-1/stage", body: `{`, status: http.StatusBadRequest, want: "unexpected EOF"},
		{name: "bad run json", method: http.MethodPost, path: "/review/tasks/task-1/runs", body: `{`, status: http.StatusBadRequest, want: "unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("response = %d %q, want %d containing %q", rec.Code, rec.Body.String(), tc.status, tc.want)
			}
		})
	}

	raw, err := json.Marshal(ReviewRunRequest{Scope: ScopeLastTurn})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/review/tasks/task-1/runs?cwd="+repo, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("failed run response = %d %q", rec.Code, rec.Body.String())
	}
	var response ReviewRunResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Run.Status != ReviewRunFailed || !strings.Contains(response.Run.Error, "last-turn diff is not available") || len(response.Findings) != 0 {
		t.Fatalf("failed review run = %+v findings %+v", response.Run, response.Findings)
	}
}

func TestServerRejectsWorktreeOutsideWorkerDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	req := httptest.NewRequest(http.MethodGet, "/review/tasks/task-1/diff?scope=UNCOMMITTED&cwd="+outside, nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "within worker work dir") {
		t.Fatalf("outside worktree response = %d %q, want 400 within worker work dir", rec.Code, rec.Body.String())
	}
}

func TestServerRejectsPatchPathEscape(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "task-worktree")
	initGitRepo(t, repo)
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	server.RegisterTask(TaskContext{TaskID: "task-1", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})

	patch := strings.Join([]string{
		"diff --git a/../evil.txt b/../evil.txt",
		"--- a/../evil.txt",
		"+++ b/../evil.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"",
	}, "\n")

	raw, err := json.Marshal(ActionRequest{
		WorktreePath: repo,
		Paths:        []string{"tracked.txt"},
		Patch:        patch,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/review/tasks/task-1/stage", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid") {
		t.Fatalf("patch escape response = %d %q, want 400 invalid path", rec.Code, rec.Body.String())
	}
}

func TestReviewHelpersCoverEdgeCases(t *testing.T) {
	if line := firstAddedLine("context\n+new\n"); line != 1 {
		t.Fatalf("firstAddedLine without hunk = %d, want 1", line)
	}
	if line := firstAddedLine("@@ -1 +4 @@\n context\n+new\n"); line != 5 {
		t.Fatalf("firstAddedLine with context = %d, want 5", line)
	}
	if line := firstAddedLine("@@ -1 +bad @@\n+new\n"); line != 1 {
		t.Fatalf("firstAddedLine bad hunk = %d, want 1", line)
	}
	status, path, oldPath := parseNameStatus("R100\told.txt\tnew.txt")
	if status != "R" || path != "new.txt" || oldPath != "old.txt" {
		t.Fatalf("rename status = %q %q %q", status, path, oldPath)
	}
	status, path, oldPath = parseNameStatus("M")
	if status != "M" || path != "" || oldPath != "" {
		t.Fatalf("short status = %q %q %q", status, path, oldPath)
	}
	add, del := patchStats("--- a/file\n+++ b/file\n-old\n+new\n context\n")
	if add != 1 || del != 1 {
		t.Fatalf("patchStats = %d %d, want 1 1", add, del)
	}
	files := []DiffFile{
		{Path: "big.txt", Patch: strings.Repeat("x", 5)},
		{Path: "later.txt", Patch: strings.Repeat("y", 5)},
	}
	if !truncateDiffFiles(files, 5, 3) || files[0].Patch != "xxx" || files[1].Patch != "" || !files[0].Truncated || !files[1].Truncated {
		t.Fatalf("truncated files = %+v", files)
	}
	if _, err := cleanPaths("/tmp", nil); err == nil {
		t.Fatal("cleanPaths should reject empty paths")
	}
	if _, err := cleanPaths("/tmp", []string{"/abs"}); err == nil {
		t.Fatal("cleanPaths should reject absolute paths")
	}
	paths, err := cleanPaths("/tmp", []string{"./a/../b.txt"})
	if err != nil || len(paths) != 1 || paths[0] != "b.txt" {
		t.Fatalf("cleanPaths valid = %+v, %v", paths, err)
	}
	patchPaths := patchPaths("diff --git a/old.txt b/new.txt\n--- /dev/null\n+++ b/new.txt\n")
	if len(patchPaths) != 2 || patchPaths[0] != "old.txt" || patchPaths[1] != "new.txt" {
		t.Fatalf("patchPaths = %+v", patchPaths)
	}
	unique := uniqueStrings([]string{"", "a", " a ", "b"})
	if len(unique) != 2 || unique[0] != "a" || unique[1] != "b" {
		t.Fatalf("uniqueStrings = %+v", unique)
	}
	if trimPatchPath("/dev/null") != "" || trimPatchPath("a/file.txt") != "file.txt" || trimPatchPath("b/file.txt") != "file.txt" {
		t.Fatal("trimPatchPath returned unexpected value")
	}
}

func initGitRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.email", "bpt@example.test")
	runGitTest(t, repo, "config", "user.name", "Block Play Table")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "base")
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOutputTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRemoteReviewWorktree(t *testing.T, root, taskID string) (string, string) {
	t.Helper()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "task-worktree")
	runGitTest(t, root, "init", "--bare", remote)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, seed, "init", "-b", "main")
	runGitTest(t, seed, "config", "user.email", "bpt@example.test")
	runGitTest(t, seed, "config", "user.name", "Block Play Table")
	if err := os.WriteFile(filepath.Join(seed, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, seed, "add", ".")
	runGitTest(t, seed, "commit", "-m", "base")
	runGitTest(t, seed, "remote", "add", "origin", remote)
	runGitTest(t, seed, "push", "origin", "main")
	runGitTest(t, root, "clone", remote, repo)
	runGitTest(t, repo, "config", "user.email", "bpt@example.test")
	runGitTest(t, repo, "config", "user.name", "Block Play Table")
	runGitTest(t, repo, "checkout", "-b", "task/"+taskID, "origin/main")
	return remote, repo
}

func advanceRemoteMain(t *testing.T, root, remote, name, content, message string) {
	t.Helper()
	clone := filepath.Join(root, "advance-"+strings.ReplaceAll(name, "/", "-"))
	runGitTest(t, root, "clone", remote, clone)
	runGitTest(t, clone, "config", "user.email", "bpt@example.test")
	runGitTest(t, clone, "config", "user.name", "Block Play Table")
	runGitTest(t, clone, "checkout", "main")
	if err := os.WriteFile(filepath.Join(clone, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, clone, "add", name)
	runGitTest(t, clone, "commit", "-m", message)
	runGitTest(t, clone, "push", "origin", "main")
}

func requestDiff(t *testing.T, server *Server, taskID string, scope DiffScope, cwd string) DiffResponse {
	return requestDiffWithStaged(t, server, taskID, scope, cwd, nil)
}

func requestDiffWithStaged(t *testing.T, server *Server, taskID string, scope DiffScope, cwd string, staged *bool) DiffResponse {
	t.Helper()
	path := "/review/tasks/" + taskID + "/diff?scope=" + string(scope) + "&cwd=" + cwd
	if staged != nil {
		path += "&staged=" + strconv.FormatBool(*staged)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff response = %d %q", rec.Code, rec.Body.String())
	}
	var response DiffResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func boolPtr(value bool) *bool {
	return &value
}

func requestAction(t *testing.T, server *Server, taskID, action string, input ActionRequest) ActionResponse {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/review/tasks/"+taskID+"/"+action, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s response = %d %q", action, rec.Code, rec.Body.String())
	}
	var response ActionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func requestGitCommand(t *testing.T, server *Server, taskID string, input GitCommandRequest) GitCommandResponse {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/review/tasks/"+taskID+"/git-command", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("git-command response = %d %q", rec.Code, rec.Body.String())
	}
	var response GitCommandResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func requestGitCommandError(t *testing.T, server *Server, taskID string, input GitCommandRequest) string {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/review/tasks/"+taskID+"/git-command", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("git-command response = %d %q, want error", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}
