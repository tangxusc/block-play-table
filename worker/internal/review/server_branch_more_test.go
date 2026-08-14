package review

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestServerExplicitConfigRegistrationAndResolutionBranches(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(Config{
		Enabled: true, Host: " 127.0.0.2 ", WorkDir: t.TempDir(),
		MaxDiffBytes: 123, MaxFileDiffBytes: 45, Logger: logger,
	})
	if server.host != "127.0.0.2" || server.maxDiffBytes != 123 || server.maxFileDiffBytes != 45 || server.logger != logger {
		t.Fatalf("显式 Review 配置被覆盖: %+v", server)
	}
	if caps := server.Capabilities(); caps["review_port"] != "" || caps["review_enabled"] != "true" {
		t.Fatalf("未启动 capabilities=%v", caps)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}

	server.RegisterTask(TaskContext{})
	server.RegisterTask(TaskContext{TaskID: "task", WorktreePath: ""})
	worktree := filepath.Join(server.workDir, "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	server.RegisterTask(TaskContext{TaskID: "task", WorktreePath: worktree, BaseBranch: "main", DefaultBranch: "develop", AgentType: string(domain.AgentCodex)})
	server.RegisterTask(TaskContext{TaskID: "task", WorktreePath: worktree})
	registered := server.tasks["task"]
	if registered.BaseBranch != "main" || registered.DefaultBranch != "develop" || registered.AgentType != string(domain.AgentCodex) {
		t.Fatalf("重新注册未继承字段: %+v", registered)
	}

	resolved, err := server.resolveTask("task", "", "feature", "release")
	if err != nil || resolved.BaseBranch != "feature" || resolved.DefaultBranch != "release" {
		t.Fatalf("resolveTask 显式分支=%+v err=%v", resolved, err)
	}
	resolved, err = server.resolveTask("unknown", worktree, "", "")
	if err != nil || resolved.WorktreePath == "" || resolved.BaseBranch != "" || resolved.DefaultBranch != "" {
		t.Fatalf("临时 task 解析=%+v err=%v", resolved, err)
	}
	if _, err := server.resolveTask("missing", "", "", ""); err == nil {
		t.Fatal("没有注册且没有 cwd 的 task 应失败")
	}

	server.EndTurn(context.Background(), "missing", "missing")
	token := server.BeginTurn(context.Background(), TaskContext{TaskID: "outside", WorktreePath: t.TempDir()})
	if token == "" || server.pending[token].BeforeTree != "" {
		t.Fatalf("非法 worktree BeginTurn=%+v", server.pending[token])
	}
	server.EndTurn(context.Background(), "outside", token)
}

func TestServerHTTPDefaultAndErrorResponseBranches(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initGitRepo(t, repo)
	server := NewServer(Config{Enabled: true, WorkDir: root})
	server.RegisterTask(TaskContext{TaskID: "task", WorktreePath: repo, BaseBranch: "main", DefaultBranch: "main"})

	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/review/tasks/task/diff?cwd=" + repo, "", http.StatusOK},
		{http.MethodGet, "/review/tasks/task/git-status?cwd=" + t.TempDir(), "", http.StatusBadRequest},
		{http.MethodPost, "/review/tasks/task/stage?cwd=" + t.TempDir(), `{}`, http.StatusBadRequest},
		{http.MethodPost, "/review/tasks/task/git-command", `{`, http.StatusBadRequest},
		{http.MethodPost, "/review/tasks/task/git-command?cwd=" + t.TempDir(), `{"command":"UNKNOWN"}`, http.StatusBadRequest},
		{http.MethodGet, "/review/tasks/", "", http.StatusNotFound},
		{http.MethodGet, "/review/tasks/diff", "", http.StatusNotFound},
	}
	for _, testCase := range tests {
		req := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, req)
		if recorder.Code != testCase.status {
			t.Fatalf("%s %s status=%d body=%q", testCase.method, testCase.path, recorder.Code, recorder.Body.String())
		}
	}

	if err := server.stage(context.Background(), TaskContext{WorktreePath: repo}, ActionRequest{Paths: []string{"tracked.txt"}, Patch: "diff --git a/../bad b/../bad\n"}); err == nil {
		t.Fatal("stage patch 逃逸应失败")
	}
	if err := server.unstage(context.Background(), TaskContext{WorktreePath: repo}, ActionRequest{Paths: []string{"tracked.txt"}, Patch: "diff --git a/../bad b/../bad\n"}); err == nil {
		t.Fatal("unstage patch 逃逸应失败")
	}
	if _, err := server.discard(context.Background(), TaskContext{WorktreePath: repo}, ActionRequest{}); err == nil {
		t.Fatal("discard 空 paths 应失败")
	}
	if err := server.restore(context.Background(), TaskContext{TaskID: "task", WorktreePath: repo}, ActionRequest{}); err == nil {
		t.Fatal("restore 缺 backup id 应失败")
	}
	if err := server.restore(context.Background(), TaskContext{TaskID: "task", WorktreePath: repo}, ActionRequest{BackupID: "missing"}); err == nil {
		t.Fatal("restore 未知 backup 应失败")
	}
	emptyPatch := filepath.Join(t.TempDir(), "empty.patch")
	if err := os.WriteFile(emptyPatch, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server.backups["empty"] = TaskGitBackup{ID: "empty", TaskID: "task", PatchPath: emptyPatch}
	if err := server.restore(context.Background(), TaskContext{TaskID: "task", WorktreePath: repo}, ActionRequest{BackupID: "empty"}); err != nil {
		t.Fatalf("空 backup patch 应幂等成功: %v", err)
	}
	missingRoot := NewServer(Config{Enabled: true, WorkDir: filepath.Join(root, "missing-root")})
	if _, err := missingRoot.validateWorktree(repo); err == nil {
		t.Fatal("不存在的 Worker work dir 应失败")
	}
	if _, err := server.validateWorktree(filepath.Join(root, "missing")); err == nil {
		t.Fatal("不存在的 worktree 应失败")
	}
	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := server.validateWorktree(filePath); err == nil {
		t.Fatal("普通文件不应作为 worktree")
	}
	if _, err := server.discard(context.Background(), TaskContext{TaskID: "task", WorktreePath: repo}, ActionRequest{
		Paths: []string{"tracked.txt"}, Patch: "invalid patch",
	}); err == nil {
		t.Fatal("非法 discard patch 应失败")
	}
}

func TestReviewPureHelperAlternativeBranches(t *testing.T) {
	for _, patch := range []string{
		"@@ malformed\n+line\n",
		"@@ -1 +4\n--- a/file\n+++ b/file\n+line\n",
		"@@ -1 +4,2 @@\n-old\n context\n+line\n",
		"no additions\n",
	} {
		_ = firstAddedLine(patch)
	}
	for _, value := range []string{"UPPER", "name.with_parts-1", "", "-bad", "bad..name", "bad@{name", "bad name"} {
		_ = safeGitName(value)
	}
	if remote, err := safeGitRemote(""); err != nil || remote != "origin" {
		t.Fatalf("默认 remote=%q err=%v", remote, err)
	}
	if _, err := safeGitRemote("team/origin"); err == nil {
		t.Fatal("带斜杠 remote 应失败")
	}
	for _, branch := range []string{"", "/main", "main/", "feature//x", "bad branch"} {
		if _, err := safeGitBranch(branch); err == nil {
			t.Fatalf("非法 branch %q 未拒绝", branch)
		}
	}
	if branch, err := safeGitBranch("Feature_1/test"); err != nil || branch == "" {
		t.Fatalf("合法 branch=%q err=%v", branch, err)
	}

	if paths := patchPaths("diff --git short\n+++ a/file\n--- /dev/null\n"); len(paths) != 1 || paths[0] != "file" {
		t.Fatalf("patchPaths 边界=%v", paths)
	}
	if got := trimPatchPath(" "); got != "" {
		t.Fatalf("空 patch path=%q", got)
	}
	files := []DiffFile{{Path: "a", Patch: "x"}}
	if truncateDiffFiles(files, 10, 10) {
		t.Fatal("限制内 diff 不应截断")
	}
	response := DiffResponse{Files: []DiffFile{{Path: "a"}}}
	if response.File("a") == nil || response.File("missing") != nil || !response.HasFile("a") || response.HasFile("missing") {
		t.Fatal("DiffResponse 文件查询错误")
	}
	if got := firstNonEmpty(" ", " value "); got != "value" || firstNonEmpty("", " ") != "" {
		t.Fatal("firstNonEmpty 边界错误")
	}
	if status, path, old := parseNameStatus("M\tfile.txt"); status != "M" || path != "file.txt" || old != "" {
		t.Fatalf("普通 name-status=%q,%q,%q", status, path, old)
	}
}

func TestGitOutputParsingAndExitBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 git 脚本仅用于 Unix 测试")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$GIT_TEST_OUTPUT" = "head" ]; then echo HEAD; exit 0; fi
if [ "$GIT_TEST_OUTPUT" = "empty" ]; then echo ''; exit 0; fi
case "$*" in
  *bad-fields*) echo one ;;
  *bad-ahead*) echo 'x 1' ;;
  *bad-behind*) echo '1 x' ;;
  *allowed*) echo allowed; exit 7 ;;
  *denied*) echo denied; exit 8 ;;
  *) echo '2 3' ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if out, err := gitOutputAllowExit(context.Background(), "", nil, []int{7}, "allowed"); err != nil || !strings.Contains(out, "allowed") {
		t.Fatalf("允许退出码 output=%q err=%v", out, err)
	}
	if _, err := gitOutputAllowExit(context.Background(), "", nil, []int{7}, "denied"); err == nil {
		t.Fatal("未允许退出码应失败")
	}
	if ahead, behind, err := aheadBehind(context.Background(), "", "left", "right"); err != nil || ahead != 2 || behind != 3 {
		t.Fatalf("ahead/behind=%d/%d err=%v", ahead, behind, err)
	}
	for _, command := range []string{"bad-fields", "bad-ahead", "bad-behind"} {
		if _, _, err := aheadBehind(context.Background(), "", command, "right"); err == nil {
			t.Fatalf("%s 输出应失败", command)
		}
	}
	t.Setenv("GIT_TEST_OUTPUT", "head")
	if _, err := currentBranch(context.Background(), ""); err == nil {
		t.Fatal("detached HEAD 应失败")
	}
	t.Setenv("GIT_TEST_OUTPUT", "empty")
	if _, err := currentBranch(context.Background(), ""); err == nil {
		t.Fatal("空 branch 应失败")
	}
	t.Setenv("GIT_TEST_OUTPUT", "")
	server := NewServer(Config{Enabled: true, WorkDir: t.TempDir()})
	response, err := server.runGitCommand(context.Background(), TaskContext{TaskID: "task", WorktreePath: "", BaseBranch: "main"}, GitCommandRequest{Command: GitCommandFetch})
	if err != nil || !response.OK || response.Output == "" {
		t.Fatalf("伪 git fetch response=%+v err=%v", response, err)
	}
	if err := server.publishGitCommand(context.Background(), TaskContext{WorktreePath: ""}, "origin", "main", "", &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "clean worktree") {
		t.Fatalf("默认 publish strategy 应继续校验工作树: %v", err)
	}
	if err := server.publishGitCommand(context.Background(), TaskContext{WorktreePath: ""}, "origin", "main", GitPublishStrategy("invalid"), &strings.Builder{}); err == nil {
		t.Fatal("非法 publish strategy 应失败")
	}
	if base, err := mergeBase(context.Background(), "", ""); err != nil || base == "" {
		t.Fatalf("空 merge base=%q err=%v", base, err)
	}
}

func TestReviewEmptyDiffAndNonExitFailureBranches(t *testing.T) {
	if files, err := treeDiff(context.Background(), t.TempDir(), "base", "head", false); err == nil || files != nil {
		t.Fatalf("非 Git 目录 treeDiff = %+v, %v", files, err)
	}
	if files, err := untrackedDiff(context.Background(), t.TempDir()); err == nil || files != nil {
		t.Fatalf("非 Git 目录 untrackedDiff = %+v, %v", files, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := gitOutputAllowExit(context.Background(), "", nil, []int{0}, "status"); err == nil {
		t.Fatal("git 不存在时未返回非 ExitError")
	}
	server := NewServer(Config{Enabled: false})
	if err := server.Start(nil); err != nil {
		t.Fatal(err)
	}
	if got := mergeEnv(map[string]string{"BPT_REVIEW_TEST": "value"}); len(got) == 0 {
		t.Fatal("mergeEnv 返回空环境")
	}
}
