package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
	"github.com/tangxusc/block-play-table/worker/internal/review"
)

func TestRunA2ACompactionLoopRunsPeriodicallyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fixedNow := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	const interval = 10 * time.Millisecond
	const retention = 90 * 24 * time.Hour
	cutoffs := make(chan time.Time, 4)
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runA2ACompactionLoop(ctx, interval, retention, func() time.Time { return fixedNow }, func(_ context.Context, cutoff time.Time) (a2astore.CompactionStats, error) {
			calls.Add(1)
			cutoffs <- cutoff
			return a2astore.CompactionStats{Events: 1}, nil
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	select {
	case cutoff := <-cutoffs:
		if want := fixedNow.Add(-retention); !cutoff.Equal(want) {
			t.Fatalf("压缩截止时间 = %s，期望 %s", cutoff, want)
		}
	case <-time.After(time.Second):
		t.Fatal("周期压缩未执行")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消 Worker context 后压缩循环未停止")
	}
	stoppedAt := calls.Load()
	time.Sleep(3 * interval)
	if calls.Load() != stoppedAt {
		t.Fatalf("压缩循环停止后仍执行: before=%d after=%d", stoppedAt, calls.Load())
	}
}

func TestWorkerEnvironmentAndCapabilityHelpers(t *testing.T) {
	t.Setenv("BPT_TEST_VALUE", "")
	if got := getenv("BPT_TEST_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("空环境变量=%q", got)
	}
	t.Setenv("BPT_TEST_VALUE", "configured")
	if got := getenv("BPT_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("配置环境变量=%q", got)
	}

	for _, testCase := range []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"", true, true}, {"", false, false}, {"false", true, false}, {" NO ", true, false},
		{"0", true, false}, {"off", true, false}, {"yes", false, true}, {"1", false, true},
	} {
		t.Setenv("BPT_TEST_BOOL", testCase.value)
		if got := parseBoolEnv("BPT_TEST_BOOL", testCase.fallback); got != testCase.want {
			t.Fatalf("parseBoolEnv(%q,%v)=%v，期望 %v", testCase.value, testCase.fallback, got, testCase.want)
		}
	}
	for _, testCase := range []struct {
		value string
		want  int
		ok    bool
	}{
		{"", 123, true}, {"0", 0, true}, {"65535", 65535, true},
		{"-1", 0, false}, {"65536", 0, false}, {"bad", 0, false},
	} {
		t.Setenv("BPT_TEST_PORT", testCase.value)
		got, err := parsePortEnv("BPT_TEST_PORT", 123)
		if (err == nil) != testCase.ok || got != testCase.want {
			t.Fatalf("parsePortEnv(%q)=(%d,%v)，期望 (%d,ok=%v)", testCase.value, got, err, testCase.want, testCase.ok)
		}
	}

	merged := mergeCapabilities(map[string]string{"shared": "first", "a": "1"}, nil, map[string]string{"shared": "last", "b": "2"})
	if len(merged) != 3 || merged["shared"] != "last" || merged["a"] != "1" || merged["b"] != "2" {
		t.Fatalf("capability 合并错误: %+v", merged)
	}
}

func TestWorkerRejectsLegacyManagerListAndBuildsStorePrincipal(t *testing.T) {
	legacyKey := "MANAGER_WS_URLS"
	t.Setenv(legacyKey, "")
	if err := rejectLegacyManagerList(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(legacyKey, "ws://one,ws://two")
	if err := rejectLegacyManagerList(); err == nil {
		t.Fatal("旧多 Manager 配置未被拒绝")
	}
	if ctx := authenticatedStoreContext(context.Background(), "worker-1"); ctx == nil {
		t.Fatal("认证 Store context 不能为空")
	}

	recorder := reviewRecorder{}
	if token := recorder.BeginTurn(context.Background(), "task", "/tmp/worktree", "main", "main"); token != "" {
		t.Fatalf("nil review server 返回 token=%q", token)
	}
	recorder.EndTurn(context.Background(), "task", "token")
}

func TestProbeRequiredAdaptersRequiresBothCLIs(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agent-version")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'agent-test 1.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_BINARY", binary)
	t.Setenv("CLAUDE_BINARY", binary)
	adapters, versions, err := probeRequiredAdapters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) != 2 || versions["codex_version"] != "agent-test 1.0" || versions["claude_version"] != "agent-test 1.0" {
		t.Fatalf("Adapter 探测错误: adapters=%d versions=%+v", len(adapters), versions)
	}
	t.Setenv("CLAUDE_BINARY", filepath.Join(t.TempDir(), "missing"))
	if _, _, err := probeRequiredAdapters(context.Background()); err == nil {
		t.Fatal("缺少 Claude CLI 时探测未失败")
	}
}

func TestRunA2ACompactionLoopHandlesInvalidConfigAndErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runA2ACompactionLoop(nil, time.Millisecond, time.Hour, time.Now, nil, logger)
	runA2ACompactionLoop(context.Background(), 0, time.Hour, time.Now, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runA2ACompactionLoop(ctx, time.Millisecond, time.Hour, time.Now, func(context.Context, time.Time) (a2astore.CompactionStats, error) {
			if calls.Add(1) == 1 {
				return a2astore.CompactionStats{}, errors.New("temporary")
			}
			cancel()
			return a2astore.CompactionStats{Tasks: 1}, nil
		}, nil)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("压缩错误恢复循环未停止")
	}
	if calls.Load() < 2 {
		t.Fatalf("压缩错误后未重试: calls=%d", calls.Load())
	}
}

func TestRunBuildsA2AWorkerStackBeforeConnectingManager(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agent-version")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'agent-test 1.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	managerRequests := make(chan struct{}, 1)
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case managerRequests <- struct{}{}:
		default:
		}
		cancel()
		http.Error(w, "test shutdown", http.StatusServiceUnavailable)
	}))
	defer manager.Close()

	workDir := t.TempDir()
	t.Setenv("WORKER_ID", "worker-main-test")
	t.Setenv("WORKER_NAME", "Worker Main Test")
	t.Setenv("WORKER_WORK_DIR", workDir)
	t.Setenv("WORKER_A2A_DB_PATH", filepath.Join(workDir, "a2a.db"))
	t.Setenv("WORKER_A2A_HOST", "127.0.0.1")
	t.Setenv("WORKER_A2A_PORT", "0")
	t.Setenv("WORKER_TERMINAL_ENABLED", "false")
	t.Setenv("WORKER_REVIEW_ENABLED", "false")
	t.Setenv("MANAGER_WS_URL", "ws"+strings.TrimPrefix(manager.URL, "http")+"/worker/ws")
	t.Setenv("MANAGER_WS_URLS", "")
	t.Setenv("CODEX_BINARY", binary)
	t.Setenv("CLAUDE_BINARY", binary)

	err := run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("Manager 测试端点关闭连接后 run 未返回错误")
	}
	select {
	case <-managerRequests:
	default:
		t.Fatalf("Worker 尚未连接 Manager 就已退出: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, "a2a.db")); statErr != nil {
		t.Fatalf("Worker A2A SQLite 未初始化: %v", statErr)
	}
}

func TestRunRecoversStoreBeforeAdapterProbe(t *testing.T) {
	workerID := "worker-recovery-before-probe"
	workDir := t.TempDir()
	databasePath := filepath.Join(workDir, "a2a.db")
	seed, err := a2astore.Open(context.Background(), a2astore.Config{
		Path: databasePath, Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})
	if err != nil {
		t.Fatal(err)
	}
	storeCtx := authenticatedStoreContext(context.Background(), workerID)
	task := &a2a.Task{ID: "task-before-probe", ContextID: "context-before-probe", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}
	if _, err := seed.Create(storeCtx, task); err != nil {
		t.Fatal(err)
	}
	if err := seed.SaveBinding(storeCtx, a2astore.RuntimeBinding{
		ExecutionID: "execution-before-probe", LocalTaskID: "local-before-probe", WorkerID: workerID,
		TaskID: string(task.ID), ContextID: task.ContextID, Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, WorktreePath: filepath.Join(workDir, "worktree"), State: "WORKING",
	}); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WORKER_ID", workerID)
	t.Setenv("WORKER_WORK_DIR", workDir)
	t.Setenv("WORKER_A2A_DB_PATH", databasePath)
	t.Setenv("MANAGER_WS_URLS", "")
	t.Setenv("CODEX_BINARY", filepath.Join(workDir, "missing-codex"))
	t.Setenv("CLAUDE_BINARY", filepath.Join(workDir, "missing-claude"))
	if err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("缺失 Agent CLI 时 run 应失败")
	}

	loaded, err := a2astore.Open(context.Background(), a2astore.Config{
		Path: databasePath, Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	stored, err := loaded.Get(storeCtx, task.ID)
	if err != nil || stored.Task.Status.State != a2a.TaskStateFailed {
		t.Fatalf("Adapter 探测失败前未恢复 Task: state=%v err=%v", stored.Task.Status.State, err)
	}
}

func TestRunAndReviewRecorderArgumentBranches(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := run(nil, logger); err == nil {
		t.Fatal("nil context 未失败")
	}
	if err := run(context.Background(), nil); err == nil {
		t.Fatal("nil logger 未失败")
	}

	server := review.NewServer(review.Config{Enabled: false})
	recorder := reviewRecorder{server: server}
	token := recorder.BeginTurn(context.Background(), "task", t.TempDir(), "main", "main")
	if token == "" {
		t.Fatal("非 nil Review Server 未创建 turn token")
	}
	recorder.EndTurn(context.Background(), "task", token)
}

func TestRunA2ACompactionLoopIgnoresCancellationAndZeroStats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runA2ACompactionLoop(ctx, time.Millisecond, time.Hour, time.Now, func(context.Context, time.Time) (a2astore.CompactionStats, error) {
			switch calls.Add(1) {
			case 1:
				return a2astore.CompactionStats{}, context.Canceled
			default:
				cancel()
				return a2astore.CompactionStats{}, nil
			}
		}, nil)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("压缩循环未在取消后停止")
	}
	if calls.Load() < 2 {
		t.Fatalf("取消错误后未继续到下一周期: %d", calls.Load())
	}
}
