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
