package a2aserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aruntime"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestExecutorRejectsEachMissingDependency(t *testing.T) {
	message := serverTestMessage(serverTestRequest(a2aext.OperationStart))
	cases := []struct {
		name     string
		executor *AgentExecutor
		execCtx  *a2asrv.ExecutorContext
	}{
		{"nil executor", nil, &a2asrv.ExecutorContext{Message: message}},
		{"nil store runtime", &AgentExecutor{}, &a2asrv.ExecutorContext{Message: message}},
		{"nil runtime", &AgentExecutor{store: newExecutorTestStore(t)}, &a2asrv.ExecutorContext{Message: message}},
		{"nil context", &AgentExecutor{store: newExecutorTestStore(t), runtime: &cancelRuntimeStub{}}, nil},
		{"nil message", &AgentExecutor{store: newExecutorTestStore(t), runtime: &cancelRuntimeStub{}}, &a2asrv.ExecutorContext{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var got error
			for _, err := range testCase.executor.Execute(context.Background(), testCase.execCtx) {
				got = err
			}
			if got == nil {
				t.Fatal("缺失依赖应失败")
			}
		})
	}
	parseExecutor := NewAgentExecutor("worker-1", newExecutorTestStore(t), &a2aruntime.Runtime{})
	var parseErr error
	for _, err := range parseExecutor.Execute(context.Background(), &a2asrv.ExecutorContext{TaskID: "task", ContextID: "context", Message: &a2a.Message{}}) {
		parseErr = err
	}
	if parseErr == nil {
		t.Fatal("依赖完整但 Message 非法时应返回解析错误")
	}
}

func TestExecutorYieldUpdatesCoversStateAndStopBranches(t *testing.T) {
	executor := &AgentExecutor{}
	execCtx := &a2asrv.ExecutorContext{TaskID: "task", ContextID: "context"}

	updates := make(chan a2aruntime.Update, 2)
	updates <- a2aruntime.Update{Event: serverBranchEvent(t, a2aext.EventLogChunk), Role: a2aext.ArtifactLog}
	updates <- a2aruntime.Update{Event: serverBranchEvent(t, a2aext.EventLogChunk), Role: a2aext.ArtifactLog, State: a2a.TaskStateWorking}
	close(updates)
	var artifacts, statuses int
	executor.yieldUpdates(context.Background(), execCtx, updates, func(event a2a.Event, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		switch event.(type) {
		case *a2a.TaskArtifactUpdateEvent:
			artifacts++
		case *a2a.TaskStatusUpdateEvent:
			statuses++
		}
		return true
	})
	if artifacts != 2 || statuses != 1 {
		t.Fatalf("更新流 artifacts=%d statuses=%d", artifacts, statuses)
	}

	stopAfterArtifact := make(chan a2aruntime.Update, 1)
	stopAfterArtifact <- a2aruntime.Update{Event: serverBranchEvent(t, a2aext.EventLogChunk), Role: a2aext.ArtifactLog, State: a2a.TaskStateWorking}
	close(stopAfterArtifact)
	calls := 0
	executor.yieldUpdates(context.Background(), execCtx, stopAfterArtifact, func(a2a.Event, error) bool { calls++; return false })
	if calls != 1 {
		t.Fatalf("yield false 后调用次数=%d", calls)
	}

	inputRequired := make(chan a2aruntime.Update, 2)
	inputRequired <- a2aruntime.Update{Event: serverBranchEvent(t, a2aext.EventLogChunk), Role: a2aext.ArtifactLog, State: a2a.TaskStateInputRequired}
	inputRequired <- a2aruntime.Update{Event: serverBranchEvent(t, a2aext.EventLogChunk), Role: a2aext.ArtifactLog}
	count := 0
	executor.yieldUpdates(context.Background(), execCtx, inputRequired, func(a2a.Event, error) bool { count++; return true })
	if count != 2 {
		t.Fatalf("INPUT_REQUIRED 应在 Artifact+Status 后停止，count=%d", count)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	executor.yieldUpdates(canceled, execCtx, make(chan a2aruntime.Update), func(a2a.Event, error) bool {
		t.Fatal("已取消 context 不应 yield")
		return true
	})
}

func TestExecutorDrainSequenceReplayAndCleanupBranches(t *testing.T) {
	store := newExecutorTestStore(t)
	runtimeStub := &interactionRuntimeStub{}
	executor := NewAgentExecutor("worker-1", store, nil)
	executor.runtime = runtimeStub
	executor.now = time.Now
	if err := executor.waitDrain(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
	drain := executor.beginDrain("task")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := executor.waitDrain(ctx, "task"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待活动 drain=%v", err)
	}
	executor.endDrain("task", make(chan struct{}))
	if executor.drains["task"] != drain {
		t.Fatal("不同 drain 不应删除当前记录")
	}
	executor.endDrain("task", drain)
	if err := executor.waitDrain(context.Background(), "task"); err != nil {
		t.Fatal(err)
	}

	binding := a2astore.RuntimeBinding{
		ExecutionID: "execution", LocalTaskID: "local", WorkerID: "worker-1", TaskID: "task", ContextID: "context",
		Attempt: 1, Turn: 1, AgentType: a2aext.AgentCodex, State: "CANCELED", LastSequence: 1,
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	sequenceCtx, sequenceCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer sequenceCancel()
	if err := executor.waitBindingSequence(sequenceCtx, binding.ExecutionID, 2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待 sequence=%v", err)
	}
	var replayErr error
	executor.replayCanceled(context.Background(), &a2asrv.ExecutorContext{TaskID: "task", ContextID: "context"}, &binding, func(_ a2a.Event, err error) bool {
		replayErr = err
		return true
	})
	if replayErr == nil {
		t.Fatal("缺失取消 journal 应失败")
	}

	request := serverTestRequest(a2aext.OperationStart)
	execCtx := &a2asrv.ExecutorContext{TaskID: "task-cleanup", ContextID: "context", Message: serverTestMessage(request)}
	executor.Cleanup(context.Background(), execCtx, nil, errors.New("execute failed"))
	if runtimeStub.aborts != 1 {
		t.Fatalf("Cleanup Abort 次数=%d", runtimeStub.aborts)
	}
	executor.Cleanup(context.Background(), &a2asrv.ExecutorContext{Message: &a2a.Message{}}, nil, errors.New("parse failed"))
	executor.Cleanup(context.Background(), execCtx, nil, nil)
	executor.abandon(context.Background(), nil)
	executor.abandon(context.Background(), request)
}

func TestIdempotentHandlerReplayConflictAndWaitBranches(t *testing.T) {
	store := newExecutorTestStore(t)
	handler := &idempotentHandler{store: store, workerID: "worker-1"}
	request := serverTestRequest(a2aext.OperationStart)
	message := &a2a.SendMessageRequest{Message: serverTestMessage(request)}
	if _, _, err := handler.prepare(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	modified := serverTestRequest(a2aext.OperationStart)
	modified.Command.ID = request.Command.ID
	modified.Task.Description = "different"
	if _, _, err := handler.prepare(context.Background(), &a2a.SendMessageRequest{Message: serverTestMessage(modified)}); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("command 内容冲突=%v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if _, err := handler.waitForBoundCommand(waitCtx, request.Command.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待 command 绑定=%v", err)
	}
	taskCtx, taskCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer taskCancel()
	if _, err := handler.waitForTask(taskCtx, "missing"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待 Task=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.waitForBoundCommand(context.Background(), request.Command.ID); err == nil {
		t.Fatal("关闭 Store 后等待 command 应失败")
	}
	if _, err := handler.waitForTask(context.Background(), "missing"); err == nil {
		t.Fatal("关闭 Store 后等待 Task 应失败")
	}
}

func TestExecutorJSONAndMessageHelperErrorBranches(t *testing.T) {
	if _, err := jsonObject(make(chan int), "channel"); err == nil {
		t.Fatal("不可编码对象应失败")
	}
	message := &a2a.Message{Parts: a2a.ContentParts{nil, a2a.NewTextPart(" ")}}
	if messageText(message) != "" {
		t.Fatal("nil 与空白 Part 不应产生消息文本")
	}
}

func serverBranchEvent(t *testing.T, eventType a2aext.EventType) *a2aext.ExecutionEvent {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event:   a2aext.EventHeader{ID: id.String(), Sequence: 1, Type: eventType, OccurredAt: time.Now().UTC()},
		Scope:   a2aext.EventScope{LocalTaskID: "local", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker-1"},
		Payload: map[string]any{"stream": "stdout", "content": "line", "chunkIndex": float64(0)},
	}
}
