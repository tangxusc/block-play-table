package a2aruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aadapter"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestRuntimeConfigReservationAndMemoryStateBranches(t *testing.T) {
	store, err := a2astore.Open(context.Background(), a2astore.Config{
		Path:          filepath.Join(t.TempDir(), "runtime.db"),
		Authenticator: func(context.Context) (string, error) { return "test", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := func() time.Time { return time.Unix(1, 0).UTC() }
	runtime, err := New(Config{
		Context: context.Background(), WorkerID: "worker", WorkDir: t.TempDir(), Store: store, Now: now,
		Adapters: map[a2aext.AgentType]a2aadapter.Adapter{a2aext.AgentCodex: runtimeAdapterFunc(func(context.Context, a2aadapter.Input, func(a2aadapter.Event)) error { return nil })},
	})
	if err != nil || runtime.now().Unix() != 1 {
		t.Fatalf("显式 Now 配置错误: runtime=%+v err=%v", runtime, err)
	}
	if _, err := New(Config{
		Context: context.Background(), WorkerID: "worker", WorkDir: t.TempDir(), Store: store,
		Adapters: map[a2aext.AgentType]a2aadapter.Adapter{a2aext.AgentCodex: nil},
	}); err == nil {
		t.Fatal("nil Adapter 应失败")
	}

	runtime.pendingBegins = nil
	if !runtime.reserveBegin("pending") || runtime.reserveBegin("pending") {
		t.Fatal("pending execution 预留幂等错误")
	}
	runtime.releaseBegin("pending")
	active := &execution{done: make(chan struct{})}
	runtime.executions["active"] = active
	if runtime.reserveBegin("active") {
		t.Fatal("活动 execution 不应重复预留")
	}
	close(active.done)
	if !runtime.reserveBegin("active") {
		t.Fatal("已结束 execution 应允许重新预留")
	}
	runtime.releaseBegin("active")

	request := runtimeTestRequest()
	ctx, cancel := context.WithCancelCause(context.Background())
	item := &execution{
		ctx: ctx, cancel: cancel, request: request, taskID: "task", contextID: "context",
		updates: make(chan Update, 8), done: make(chan struct{}), logChunkIndex: map[a2aext.LogStream]int64{},
		logRedactors: map[a2aext.LogStream]*a2aext.Redactor{
			a2aext.LogStdout: a2aext.NewRedactor(nil), a2aext.LogStderr: a2aext.NewRedactor(nil), a2aext.LogSystem: a2aext.NewRedactor(nil),
		},
	}
	runtime.executions["memory"] = item
	if err := runtime.Cancel(context.Background(), "memory"); err != nil || !channelClosed(item.done) {
		t.Fatalf("未启动 execution 取消错误: %v", err)
	}
	runtime.Abort("memory", errors.New("explicit abort"))
	startedCtx, startedCancel := context.WithCancelCause(context.Background())
	started := &execution{ctx: startedCtx, cancel: startedCancel, done: make(chan struct{}), updates: make(chan Update), startRequested: true}
	runtime.executions["started"] = started
	runtime.Abort("started", errors.New("initialization failed"))
	if channelClosed(started.done) || context.Cause(started.ctx) == nil {
		t.Fatal("已请求启动的 execution 应只取消 context，由运行协程负责 finish")
	}
	started.finish()

	sequenceItem := &execution{sequence: 5}
	sequenceItem.rollbackSequence(4)
	if sequenceItem.sequence != 5 {
		t.Fatal("非尾序号不应回滚")
	}
	sequenceItem.rollbackSequence(5)
	if sequenceItem.sequence != 4 {
		t.Fatal("尾序号应回滚")
	}
	if sequenceItem.runtimeInfo() != nil {
		t.Fatal("空 Runtime 信息应返回 nil")
	}
	sequenceItem.branch = "task/branch"
	if info := sequenceItem.runtimeInfo(); info == nil || info.Branch != "task/branch" {
		t.Fatalf("Runtime 信息=%+v", info)
	}
}

func TestRuntimeInteractionDeliveryRaceBranches(t *testing.T) {
	runtime := &Runtime{executions: map[string]*execution{}}
	if _, err := runtime.ResumeInteraction(context.Background(), runtimeTestRequest()); err == nil {
		t.Fatal("不存在的交互 Runtime 应失败")
	}
	request := runtimeTestRequest()
	request.Interaction = &a2aext.Interaction{ID: "expected", Decision: a2aext.DecisionRespond}
	if _, err := runtime.ResumeInteraction(context.Background(), request); err == nil {
		t.Fatal("参数完整但 execution 不存在时应失败")
	}
	itemCtx, itemCancel := context.WithCancelCause(context.Background())
	item := &execution{ctx: itemCtx, cancel: itemCancel, done: make(chan struct{}), updates: make(chan Update)}
	runtime.executions[request.Scope.ExecutionID] = item
	if _, err := runtime.ResumeInteraction(context.Background(), request); err == nil {
		t.Fatal("没有 pending interaction 应失败")
	}
	item.pending = &pendingInteraction{request: a2aadapter.InteractionRequest{ID: "other"}, response: make(chan a2aadapter.InteractionResponse)}
	if _, err := runtime.ResumeInteraction(context.Background(), request); err == nil {
		t.Fatal("交互 ID 不匹配应失败")
	}
	item.pending = &pendingInteraction{request: a2aadapter.InteractionRequest{ID: "expected"}, response: make(chan a2aadapter.InteractionResponse)}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.ResumeInteraction(canceled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("交互投递 context 取消=%v", err)
	}
	close(item.done)
	if _, err := runtime.ResumeInteraction(context.Background(), request); err == nil {
		t.Fatal("已结束 Runtime 应拒绝交互")
	}

	otherPending := &pendingInteraction{}
	runtime.clearPendingInteraction(item, otherPending)
	if item.pending == nil {
		t.Fatal("不同 pending 不应被清理")
	}
	runtime.clearPendingInteraction(item, item.pending)
	if item.pending != nil {
		t.Fatal("相同 pending 应被清理")
	}
}

func TestRuntimeEventAndErrorAlternativeBranches(t *testing.T) {
	request := runtimeTestRequest()
	request.Environment.Variables = []a2aext.EnvironmentVariable{
		{Key: "SECRET", Value: "token", Sensitive: true},
		{Key: "EMPTY", Value: "", Sensitive: true},
		{Key: "PUBLIC", Value: "value", Sensitive: false},
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	item := &execution{
		ctx: ctx, cancel: cancel, request: request, updates: make(chan Update, 16), done: make(chan struct{}),
		logChunkIndex: map[a2aext.LogStream]int64{}, logRedactors: map[a2aext.LogStream]*a2aext.Redactor{
			a2aext.LogStdout: a2aext.NewRedactor([]string{"token"}), a2aext.LogStderr: a2aext.NewRedactor([]string{"token"}), a2aext.LogSystem: a2aext.NewRedactor([]string{"token"}),
		},
	}
	runtime := &Runtime{workerID: request.Scope.ExpectedWorkerID, now: time.Now}
	runtime.handleAdapterEvent(item, a2aadapter.Event{Type: a2aadapter.EventStderr, Content: "token error"})
	runtime.handleAdapterEvent(item, a2aadapter.Event{Type: a2aadapter.EventConversation, Content: ""})
	runtime.handleAdapterEvent(item, a2aadapter.Event{Type: a2aadapter.EventCompleted, Content: "token completed"})
	runtime.handleAdapterEvent(item, a2aadapter.Event{Type: a2aadapter.EventFailed, Content: "token failed"})
	if item.result != "[REDACTED] failed" {
		t.Fatalf("失败结果未脱敏: %q", item.result)
	}
	if got := sensitiveValues(request); len(got) != 1 || got[0] != "token" {
		t.Fatalf("敏感值筛选=%q", got)
	}
	if got := environmentMap(request); got["PUBLIC"] != "value" || got["EMPTY"] != "" {
		t.Fatalf("环境变量映射=%v", got)
	}

	item.cancelRequested = true
	runtime.finishWithError(item, errors.New("ignored"))
	item.cancelRequested = false
	runtime.finishWithError(item, context.Canceled)
	if len(item.updates) != 1 {
		t.Fatalf("取消错误不应产生终态，updates=%d", len(item.updates))
	}
	if err := runtime.runShell(item, nil, " "); err != nil {
		t.Fatalf("空 shell 命令应忽略: %v", err)
	}
}

func TestRuntimeRequestInteractionRejectsConcurrentPendingAndHandlesReplacement(t *testing.T) {
	request := runtimeTestRequest()
	ctx, cancel := context.WithCancelCause(context.Background())
	item := &execution{
		ctx: ctx, cancel: cancel, request: request, updates: make(chan Update, 4), done: make(chan struct{}),
		pending: &pendingInteraction{request: a2aadapter.InteractionRequest{ID: "existing"}},
	}
	runtime := &Runtime{workerID: request.Scope.ExpectedWorkerID, now: time.Now}
	if _, err := runtime.requestInteraction(context.Background(), item, a2aadapter.InteractionRequest{ID: "new", Kind: a2aext.InteractionUserInput}); err == nil {
		t.Fatal("已有待处理交互时应拒绝第二个请求")
	}

	item.pending = nil
	result := make(chan error, 1)
	go func() {
		_, err := runtime.requestInteraction(context.Background(), item, a2aadapter.InteractionRequest{ID: "replace", Kind: a2aext.InteractionUserInput})
		result <- err
	}()
	var original *pendingInteraction
	deadline := time.After(time.Second)
	for original == nil {
		item.mu.Lock()
		original = item.pending
		item.mu.Unlock()
		select {
		case <-deadline:
			t.Fatal("等待交互进入 pending 超时")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	replacement := &pendingInteraction{request: a2aadapter.InteractionRequest{ID: "replacement"}}
	item.mu.Lock()
	item.pending = replacement
	item.mu.Unlock()
	original.response <- a2aadapter.InteractionResponse{Decision: a2aext.DecisionRespond}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.pending != replacement {
		t.Fatal("旧交互响应不应清理新的 pending")
	}
}

func TestRuntimeResumeSplitAndPathFallbackBranches(t *testing.T) {
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
	binding := &a2astore.RuntimeBinding{Attempt: 1, Turn: 1, ContextID: "context", AgentSessionID: "session", WorktreePath: worktree}
	for name, mutate := range map[string]func(*a2aext.ExecutionRequest, *a2astore.RuntimeBinding){
		"resume path": func(req *a2aext.ExecutionRequest, _ *a2astore.RuntimeBinding) {
			req.Resume.WorktreePath = filepath.Join(workDir, "..", "outside")
		},
		"binding path": func(_ *a2aext.ExecutionRequest, bound *a2astore.RuntimeBinding) {
			bound.WorktreePath = filepath.Join(workDir, "..", "outside")
		},
		"context": func(_ *a2aext.ExecutionRequest, bound *a2astore.RuntimeBinding) { bound.ContextID = "other" },
	} {
		reqCopy := *request
		resumeCopy := *request.Resume
		reqCopy.Resume = &resumeCopy
		boundCopy := *binding
		mutate(&reqCopy, &boundCopy)
		if err := validateResume(workDir, &reqCopy, &boundCopy, "context"); err == nil {
			t.Fatalf("%s 不一致应失败", name)
		}
	}
	request.Resume = nil
	if err := validateResume(workDir, request, binding, "context"); err == nil {
		t.Fatal("缺 resume 应失败")
	}

	invalidUTF8 := string([]byte{0x80, 0x80, 'x'})
	chunks := splitUTF8(invalidUTF8, 1)
	if len(chunks) != 3 {
		t.Fatalf("非法 UTF-8 兜底分块=%q", chunks)
	}
	if splitUTF8("", 1) != nil || splitUTF8("value", 0) != nil {
		t.Fatal("空值或非法上限应返回 nil")
	}
	open := make(chan struct{})
	if channelClosed(open) {
		t.Fatal("打开 channel 被误判关闭")
	}
	close(open)
	if !channelClosed(open) {
		t.Fatal("关闭 channel 未识别")
	}
	if resolveGitRef(context.Background(), workDir, "") != "HEAD" || resolveGitRef(context.Background(), workDir, "HEAD") != "HEAD" {
		t.Fatal("空 ref 和 HEAD 应直接回退 HEAD")
	}
}
