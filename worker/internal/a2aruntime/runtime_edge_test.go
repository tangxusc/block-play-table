package a2aruntime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aadapter"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestRuntimePublicValidationAndAbort(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("空 Runtime 配置应失败")
	}
	invalidAdapterConfig := Config{
		Context: context.Background(), WorkerID: "worker-1", WorkDir: t.TempDir(),
		Adapters: map[a2aext.AgentType]a2aadapter.Adapter{a2aext.AgentType("unknown"): runtimeAdapterFunc(nil)},
	}
	if _, err := New(invalidAdapterConfig); err == nil {
		t.Fatal("未知 Adapter 类型应失败")
	}

	harness := newRuntimeHarness(t, runtimeAdapterFunc(func(context.Context, a2aadapter.Input, func(a2aadapter.Event)) error { return nil }), nil)
	if err := harness.runtime.Start("missing"); err == nil {
		t.Fatal("启动不存在 execution 应失败")
	}
	if err := harness.runtime.Cancel(context.Background(), "missing"); err == nil {
		t.Fatal("取消不存在 execution 应失败")
	}
	if _, err := harness.runtime.LastSequence("missing"); err == nil {
		t.Fatal("查询不存在 execution 应失败")
	}
	if _, err := harness.runtime.ResumeInteraction(nil, nil); err == nil {
		t.Fatal("空交互回复应失败")
	}
	if _, err := harness.runtime.Begin(nil, nil, "", "", ""); err == nil {
		t.Fatal("空 Begin 参数应失败")
	}

	unsupported := runtimeTestRequest()
	unsupported.Project.GitURL = harness.repository
	unsupported.Agent.Type = a2aext.AgentClaude
	if _, err := harness.runtime.Begin(context.Background(), unsupported, "", "task-unsupported", "context-unsupported"); err == nil {
		t.Fatal("未配置的 Agent 应失败")
	}
	interaction := runtimeTestRequest()
	interaction.Project.GitURL = harness.repository
	interaction.Command.Operation = a2aext.OperationInteractionResponse
	interaction.Worktree.Mode = a2aext.WorktreeResume
	interaction.Resume = &a2aext.Resume{AgentSessionID: "session", WorktreePath: filepath.Join(harness.workDir, "worktree")}
	interaction.Interaction = &a2aext.Interaction{ID: "interaction", Decision: a2aext.DecisionRespond}
	if _, err := harness.runtime.Begin(context.Background(), interaction, "", "task-interaction", "context-interaction"); err == nil {
		t.Fatal("INTERACTION_RESPONSE 应走 ResumeInteraction")
	}

	request := runtimeTestRequest()
	request.Project.GitURL = harness.repository
	turn, err := harness.runtime.Begin(context.Background(), request, "", "task-abort", "context-abort")
	if err != nil {
		t.Fatal(err)
	}
	if sequence, err := harness.runtime.LastSequence(request.Scope.ExecutionID); err != nil || sequence != 1 {
		t.Fatalf("Begin 后 sequence=%d, err=%v", sequence, err)
	}
	harness.runtime.Abort(request.Scope.ExecutionID, nil)
	select {
	case _, open := <-turn.Updates:
		if open {
			t.Fatal("Abort 后更新流仍打开")
		}
	case <-time.After(time.Second):
		t.Fatal("Abort 后更新流未关闭")
	}
	harness.runtime.Abort("missing", errors.New("ignored"))
}

func TestRuntimeBeginRejectsActiveExecutionWithoutOverwritingBinding(t *testing.T) {
	harness := newRuntimeHarness(t, runtimeAdapterFunc(func(context.Context, a2aadapter.Input, func(a2aadapter.Event)) error { return nil }), nil)
	request := runtimeTestRequest()
	request.Project.GitURL = harness.repository
	turn, err := harness.runtime.Begin(context.Background(), request, "首次执行", "task-original", "context-original")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { harness.runtime.Abort(request.Scope.ExecutionID, nil) })
	before, err := harness.store.GetBinding(context.Background(), request.Scope.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.runtime.Begin(context.Background(), request, "重复执行", "task-replacement", "context-original"); err == nil {
		t.Fatal("活动 execution 的重复 Begin 应失败")
	}
	after, err := harness.store.GetBinding(context.Background(), request.Scope.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before {
		t.Fatalf("重复 Begin 改写了 binding: before=%+v after=%+v", before, after)
	}
	harness.runtime.Abort(request.Scope.ExecutionID, nil)
	select {
	case _, open := <-turn.Updates:
		if open {
			t.Fatal("Abort 后更新流仍打开")
		}
	case <-time.After(time.Second):
		t.Fatal("Abort 后更新流未关闭")
	}
}

func TestRuntimeInteractionHonorsAdapterContextCancellation(t *testing.T) {
	cancelInteraction := make(chan struct{})
	adapter := runtimeAdapterFunc(func(ctx context.Context, input a2aadapter.Input, _ func(a2aadapter.Event)) error {
		interactionCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-cancelInteraction:
				cancel()
			case <-interactionCtx.Done():
			}
		}()
		_, err := input.RequestInteraction(interactionCtx, a2aadapter.InteractionRequest{
			ID: "interaction-cancel", Kind: a2aext.InteractionUserInput, Title: "输入", Body: "请回复",
		})
		return err
	})
	harness := newRuntimeHarness(t, adapter, nil)
	request := runtimeTestRequest()
	request.Project.GitURL = harness.repository
	turn := harness.beginTurn(t, request, "a2a-task-interaction-cancel", "a2a-context-interaction-cancel", "交互取消")

	for {
		select {
		case update, open := <-turn.Updates:
			if !open {
				t.Fatal("取消前 Runtime 更新流已关闭")
			}
			if update.Event.Event.Type == a2aext.EventInteractionRequested {
				close(cancelInteraction)
				goto waitClosed
			}
		case <-time.After(2 * time.Second):
			t.Fatal("未收到交互请求事件")
		}
	}

waitClosed:
	for {
		select {
		case _, open := <-turn.Updates:
			if open {
				continue
			}
			item := harness.runtime.lookup(request.Scope.ExecutionID)
			item.mu.Lock()
			pending := item.pending
			item.mu.Unlock()
			if pending != nil {
				t.Fatalf("交互上下文取消后 pending 未清理: %+v", pending.request)
			}
			return
		case <-time.After(2 * time.Second):
			t.Fatal("交互上下文取消后 Runtime 未退出")
		}
	}
}

func TestWorkspaceLocalModesAndPathHelpers(t *testing.T) {
	workDir := t.TempDir()
	runtime := &Runtime{workDir: workDir}
	request := runtimeTestRequest()
	request.Project.GitURL = ""
	path, branch, err := runtime.prepareWorkspace(context.Background(), request)
	if err != nil || path == "" || branch == "" {
		t.Fatalf("本地 CREATE worktree = %q %q, %v", path, branch, err)
	}
	if _, _, err := runtime.prepareWorkspace(context.Background(), request); err == nil {
		t.Fatal("CREATE 不应覆盖既有 worktree")
	}
	marker := filepath.Join(path, "stale")
	if err := os.WriteFile(marker, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	request.Worktree.Mode = a2aext.WorktreeRecreate
	if recreated, _, err := runtime.prepareWorkspace(context.Background(), request); err != nil || recreated != path {
		t.Fatalf("本地 RECREATE worktree = %q, %v", recreated, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RECREATE 未清理旧内容: %v", err)
	}
	request.Worktree.Mode = a2aext.WorktreeResume
	request.Resume = &a2aext.Resume{AgentSessionID: "session", WorktreePath: path}
	if resumed, _, err := runtime.prepareWorkspace(context.Background(), request); err != nil || resumed != path {
		t.Fatalf("本地 RESUME worktree = %q, %v", resumed, err)
	}
	request.Resume.WorktreePath = filepath.Join(workDir, "missing")
	if _, _, err := runtime.prepareWorkspace(context.Background(), request); err == nil {
		t.Fatal("RESUME 不存在目录应失败")
	}
	request.Resume.WorktreePath = filepath.Join(workDir, "..", "outside")
	if _, _, err := runtime.prepareWorkspace(context.Background(), request); err == nil {
		t.Fatal("RESUME 逃逸路径应失败")
	}

	if safePathPart("***") != "item" || len(safePathPart(strings.Repeat("a", 100))) != 80 || safePathPart("a / b") != "a-b" {
		t.Fatal("safePathPart 边界处理错误")
	}
	if !samePath(path, filepath.Join(path, ".")) || samePath(path, workDir) {
		t.Fatal("samePath 判断错误")
	}
	if firstNonEmpty("", " value ", "fallback") != " value " || firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty 判断错误")
	}
	entries := parseGitWorktreeList([]byte("worktree /tmp/one\nHEAD abc\nbranch refs/heads/main\n\nworktree /tmp/two\ndetached\n"))
	if len(entries) != 2 || entries[0].branch != "refs/heads/main" || entries[1].path != "/tmp/two" {
		t.Fatalf("worktree porcelain 解析 = %+v", entries)
	}
}

func TestGitWorkspaceCacheResolutionAndRemoval(t *testing.T) {
	repository := createRuntimeTestRepository(t)
	runtime := &Runtime{}
	cache := filepath.Join(t.TempDir(), "cache")
	if err := runtime.ensureRepositoryCache(context.Background(), repository, cache); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ensureRepositoryCache(context.Background(), repository, cache); err != nil {
		t.Fatalf("更新既有仓库缓存: %v", err)
	}
	if got := resolveGitRef(context.Background(), cache, "main"); got != "main" {
		t.Fatalf("解析本地 ref = %q", got)
	}
	if got := resolveGitRef(context.Background(), cache, "missing"); got != "missing" {
		t.Fatalf("缺失 ref 回退 = %q", got)
	}
	if err := removeWorktreesForBranch(context.Background(), repository, "main"); err == nil {
		t.Fatal("不应移除仓库主 checkout")
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	command := exec.Command("git", "-C", repository, "worktree", "add", "-b", "task/remove", checkout, "main")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("创建测试 worktree: %v: %s", err, output)
	}
	if err := removeWorktreesForBranch(context.Background(), repository, "task/remove"); err != nil {
		t.Fatalf("移除任务 worktree: %v", err)
	}
	if _, err := os.Stat(checkout); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("任务 worktree 仍存在: %v", err)
	}
}

func TestPrepareWorkspaceSerializesSharedRepositoryCache(t *testing.T) {
	repository := createRuntimeTestRepository(t)
	runtime := &Runtime{workDir: t.TempDir()}
	first := runtimeTestRequest()
	first.Project.GitURL = repository
	second := runtimeTestRequest()
	second.Project.GitURL = repository
	second.Scope.ExecutionID = "execution-concurrent-2"
	second.Scope.LocalTaskID = "local-task-concurrent-2"

	cacheDir := filepath.Join(runtime.workDir, ".repos", repositoryCacheName(first))
	if runtime.repositoryLock(cacheDir) != runtime.repositoryLock(filepath.Join(cacheDir, ".")) {
		t.Fatal("同一仓库缓存路径未复用互斥锁")
	}
	start := make(chan struct{})
	type result struct {
		path string
		err  error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, request := range []*a2aext.ExecutionRequest{first, second} {
		go func(request *a2aext.ExecutionRequest) {
			ready.Done()
			<-start
			path, _, err := runtime.prepareWorkspace(context.Background(), request)
			results <- result{path: path, err: err}
		}(request)
	}
	ready.Wait()
	close(start)
	one, two := <-results, <-results
	if one.err != nil || two.err != nil {
		t.Fatalf("并发准备共享仓库失败: first=%v second=%v", one.err, two.err)
	}
	if one.path == two.path || one.path == "" || two.path == "" {
		t.Fatalf("并发 worktree 路径非法: %q %q", one.path, two.path)
	}
}

func TestProcessAndShellCancellationKillProcessGroup(t *testing.T) {
	if stdruntime.GOOS == "windows" {
		t.Skip("进程组测试仅用于 Unix")
	}
	processCtx, cancelProcess := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancelProcess()
	}()
	started := time.Now()
	if _, err := runProcess(processCtx, "", nil, "sh", "-c", "sleep 5"); !errors.Is(err, context.Canceled) {
		t.Fatalf("runProcess 取消错误 = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("runProcess 未及时杀死进程组")
	}

	shellCtx, cancelShell := context.WithCancelCause(context.Background())
	request := runtimeTestRequest()
	item := &execution{
		ctx: shellCtx, cancel: cancelShell, request: request, taskID: "task", contextID: "context",
		worktreePath: t.TempDir(), updates: make(chan Update, runtimeUpdateBuffer),
		logChunkIndex: map[a2aext.LogStream]int64{}, logRedactors: map[a2aext.LogStream]*a2aext.Redactor{},
	}
	for _, stream := range []a2aext.LogStream{a2aext.LogStdout, a2aext.LogStderr, a2aext.LogSystem} {
		item.logRedactors[stream] = a2aext.NewRedactor(nil)
	}
	runtime := &Runtime{workerID: "worker-1", now: time.Now}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancelShell(context.Canceled)
	}()
	started = time.Now()
	if err := runtime.runShell(item, nil, "sleep 5"); !errors.Is(err, context.Canceled) {
		t.Fatalf("runShell 取消错误 = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("runShell 未及时杀死进程组")
	}
}

func TestValidateResumeRejectsMismatchedRuntime(t *testing.T) {
	workDir := t.TempDir()
	worktree := filepath.Join(workDir, "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	request := runtimeTestRequest()
	request.Command.Operation = a2aext.OperationContinue
	request.Worktree.Mode = a2aext.WorktreeResume
	request.Scope.Turn = 2
	request.Resume = &a2aext.Resume{AgentSessionID: "session", WorktreePath: worktree}
	binding := &a2astore.RuntimeBinding{
		ExecutionID: request.Scope.ExecutionID, Attempt: 1, Turn: 1,
		ContextID: "context", AgentSessionID: "session", WorktreePath: worktree,
	}
	if err := validateResume(workDir, request, binding, "context"); err != nil {
		t.Fatalf("合法 resume 被拒绝: %v", err)
	}
	request.Resume.AgentSessionID = "different"
	if err := validateResume(workDir, request, binding, "context"); !errors.Is(err, a2astore.ErrProtocolConflict) {
		t.Fatalf("session 不一致错误 = %v", err)
	}
	if err := validateResume(workDir, request, nil, "context"); err == nil {
		t.Fatal("空 binding 应失败")
	}
}
