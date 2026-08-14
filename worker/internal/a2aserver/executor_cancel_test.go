package a2aserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aruntime"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestAgentExecutorCancelPersistsTerminalAndReplaysIdempotently(t *testing.T) {
	store := newExecutorTestStore(t)
	binding := a2astore.RuntimeBinding{
		ExecutionID: "execution-cancel", LocalTaskID: "local-task", WorkerID: "worker-1",
		TaskID: "remote-task", ContextID: "context", Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, AgentSessionID: "session", WorktreePath: "/tmp/worktree", State: "WORKING",
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	runtime := &cancelRuntimeStub{}
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	executor := &AgentExecutor{
		workerID: "worker-1", store: store, runtime: runtime, now: func() time.Time { return now },
		drains: map[string]chan struct{}{}, cancelLocks: map[string]*sync.Mutex{},
	}
	execCtx := &a2asrv.ExecutorContext{TaskID: a2a.TaskID(binding.TaskID), ContextID: binding.ContextID}

	assertCanceled := func() {
		t.Helper()
		var artifacts, statuses int
		for event, err := range executor.Cancel(context.Background(), execCtx) {
			if err != nil {
				t.Fatal(err)
			}
			switch value := event.(type) {
			case *a2a.TaskArtifactUpdateEvent:
				artifacts++
				if value.Artifact == nil || value.Artifact.Name != string(a2aext.ArtifactManifest) {
					t.Fatalf("取消 Artifact=%+v", value)
				}
			case *a2a.TaskStatusUpdateEvent:
				statuses++
				if value.Status.State != a2a.TaskStateCanceled {
					t.Fatalf("取消状态=%s", value.Status.State)
				}
			default:
				t.Fatalf("未知取消事件 %T", event)
			}
		}
		if artifacts != 1 || statuses != 1 {
			t.Fatalf("取消事件数量 artifacts=%d statuses=%d", artifacts, statuses)
		}
	}
	assertCanceled()
	assertCanceled()
	if runtime.cancelCalls != 1 || runtime.sequenceCalls != 1 {
		t.Fatalf("重复取消再次调用 Runtime: cancel=%d sequence=%d", runtime.cancelCalls, runtime.sequenceCalls)
	}
	stored, err := store.GetBinding(context.Background(), binding.ExecutionID)
	if err != nil || stored.State != "CANCELED" || stored.LastSequence != 1 {
		t.Fatalf("取消 binding=%+v err=%v", stored, err)
	}
	events, err := store.ListEvents(context.Background(), binding.ExecutionID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Event.Type != a2aext.EventExecutionTerminal {
		t.Fatalf("取消 journal=%+v err=%v", events, err)
	}
}

func TestAgentExecutorCancelRejectsDependenciesAndTerminalTasks(t *testing.T) {
	var got error
	for _, err := range (&AgentExecutor{}).Cancel(context.Background(), nil) {
		got = err
	}
	if got == nil {
		t.Fatal("空 Cancel 依赖未失败")
	}

	store := newExecutorTestStore(t)
	for _, state := range []string{"COMPLETED", "FAILED"} {
		t.Run(state, func(t *testing.T) {
			binding := a2astore.RuntimeBinding{
				ExecutionID: "execution-" + state, LocalTaskID: "local-" + state, WorkerID: "worker-1",
				TaskID: "task-" + state, ContextID: "context-" + state, Attempt: 1, Turn: 1,
				AgentType: a2aext.AgentCodex, State: state,
			}
			if err := store.SaveBinding(context.Background(), binding); err != nil {
				t.Fatal(err)
			}
			runtime := &cancelRuntimeStub{}
			executor := &AgentExecutor{store: store, runtime: runtime, now: time.Now, drains: map[string]chan struct{}{}, cancelLocks: map[string]*sync.Mutex{}}
			var cancelErr error
			for _, err := range executor.Cancel(context.Background(), &a2asrv.ExecutorContext{TaskID: a2a.TaskID(binding.TaskID), ContextID: binding.ContextID}) {
				cancelErr = err
			}
			if !errors.Is(cancelErr, a2a.ErrTaskNotCancelable) || runtime.cancelCalls != 1 {
				t.Fatalf("终态取消 err=%v runtime calls=%d", cancelErr, runtime.cancelCalls)
			}
		})
	}
}

func TestAgentExecutorCancelPropagatesRuntimeDrainAndSequenceErrors(t *testing.T) {
	newExecutor := func(t *testing.T, suffix string, runtime *cancelRuntimeStub) (*AgentExecutor, *a2asrv.ExecutorContext) {
		t.Helper()
		storage := newExecutorTestStore(t)
		binding := a2astore.RuntimeBinding{
			ExecutionID: "execution-" + suffix, LocalTaskID: "local-" + suffix, WorkerID: "worker-1",
			TaskID: "task-" + suffix, ContextID: "context-" + suffix, Attempt: 1, Turn: 1,
			AgentType: a2aext.AgentCodex, State: "WORKING",
		}
		if err := storage.SaveBinding(context.Background(), binding); err != nil {
			t.Fatal(err)
		}
		return &AgentExecutor{
			workerID: "worker-1", store: storage, runtime: runtime, now: time.Now,
			drains: map[string]chan struct{}{}, cancelLocks: map[string]*sync.Mutex{},
		}, &a2asrv.ExecutorContext{TaskID: a2a.TaskID(binding.TaskID), ContextID: binding.ContextID}
	}
	collectError := func(ctx context.Context, executor *AgentExecutor, execCtx *a2asrv.ExecutorContext) error {
		var got error
		for _, err := range executor.Cancel(ctx, execCtx) {
			got = err
		}
		return got
	}

	missing := &AgentExecutor{
		store: newExecutorTestStore(t), runtime: &cancelRuntimeStub{}, now: time.Now,
		drains: map[string]chan struct{}{}, cancelLocks: map[string]*sync.Mutex{},
	}
	if err := collectError(context.Background(), missing, &a2asrv.ExecutorContext{TaskID: "missing"}); err == nil {
		t.Fatal("缺失取消 binding 未失败")
	}

	runtimeFailure, runtimeFailureCtx := newExecutor(t, "runtime-error", &cancelRuntimeStub{cancelErr: errors.New("cancel failed")})
	if err := collectError(context.Background(), runtimeFailure, runtimeFailureCtx); err == nil {
		t.Fatal("Runtime Cancel 错误未透传")
	}

	drainFailure, drainFailureCtx := newExecutor(t, "drain-timeout", &cancelRuntimeStub{})
	drainFailure.drains[string(drainFailureCtx.TaskID)] = make(chan struct{})
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer drainCancel()
	if err := collectError(drainCtx, drainFailure, drainFailureCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待 Runtime drain 错误=%v", err)
	}

	sequenceFailure, sequenceFailureCtx := newExecutor(t, "sequence-error", &cancelRuntimeStub{sequenceErr: errors.New("sequence failed")})
	if err := collectError(context.Background(), sequenceFailure, sequenceFailureCtx); err == nil {
		t.Fatal("LastSequence 错误未透传")
	}

	sequenceTimeout, sequenceTimeoutCtx := newExecutor(t, "sequence-timeout", &cancelRuntimeStub{sequence: 1})
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if err := collectError(waitCtx, sequenceTimeout, sequenceTimeoutCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待持久化 sequence 错误=%v", err)
	}

	stopEarly, stopEarlyCtx := newExecutor(t, "stop-early", &cancelRuntimeStub{})
	yields := 0
	for _, err := range stopEarly.Cancel(context.Background(), stopEarlyCtx) {
		if err != nil {
			t.Fatal(err)
		}
		yields++
		break
	}
	if yields != 1 {
		t.Fatalf("提前停止取消流 yields=%d", yields)
	}
}

func TestExecutorEventConversionHelpers(t *testing.T) {
	now := time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	event := &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event:   a2aext.EventHeader{ID: id.String(), Sequence: 7, Type: a2aext.EventLogChunk, OccurredAt: now},
		Scope:   a2aext.EventScope{LocalTaskID: "local", ExecutionID: "execution", Attempt: 1, Turn: 2, WorkerID: "worker"},
		Payload: map[string]any{"stream": "stdout", "content": "line", "chunkIndex": float64(3)},
	}
	execCtx := &a2asrv.ExecutorContext{TaskID: "task", ContextID: "context"}
	artifactUpdate, err := artifactEvent(execCtx, event, a2aext.ArtifactLog)
	if err != nil || artifactUpdate.Artifact == nil || artifactUpdate.Artifact.Name != string(a2aext.ArtifactLog) {
		t.Fatalf("artifactEvent=%+v err=%v", artifactUpdate, err)
	}
	status, err := statusEvent(execCtx, event, a2a.TaskStateWorking)
	if err != nil || status.Status.State != a2a.TaskStateWorking || status.Status.Message == nil {
		t.Fatalf("statusEvent=%+v err=%v", status, err)
	}
	if value, err := eventJSONValue(event); err != nil || value["kind"] != a2aext.EventKind {
		t.Fatalf("eventJSONValue=%+v err=%v", value, err)
	}
	if _, err := eventJSONValue(&a2aext.ExecutionEvent{}); err == nil {
		t.Fatal("非法 event 未被拒绝")
	}
	if got := payloadString(event.Payload, "stream"); got != "stdout" {
		t.Fatalf("payloadString=%q", got)
	}
	for input, want := range map[any]int64{int64(1): 1, int(2): 2, float64(3): 3, "bad": 0} {
		if got := payloadInt64(map[string]any{"value": input}, "value"); got != want {
			t.Fatalf("payloadInt64(%T)=%d，期望 %d", input, got, want)
		}
	}
	message := &a2a.Message{Parts: a2a.ContentParts{a2a.NewDataPart(map[string]any{"ignored": true}), a2a.NewTextPart(" prompt ")}}
	if got := messageText(message); got != " prompt " || messageText(nil) != "" {
		t.Fatalf("messageText=%q", got)
	}
	copy, err := jsonMessageCopy(message)
	if err != nil || copy == message || messageText(copy) != " prompt " {
		t.Fatalf("jsonMessageCopy=%+v err=%v", copy, err)
	}
	if _, err := jsonMessageCopy(nil); err == nil {
		t.Fatal("nil Message 未被拒绝")
	}
	if _, err := submittedTask(execCtx, message, nil); err == nil {
		t.Fatal("nil accepted event 未被拒绝")
	}
	if rejected, err := rejectedTask(execCtx, message, errors.New("denied"), now); err != nil || rejected.Status.State != a2a.TaskStateRejected {
		t.Fatalf("rejectedTask=%+v err=%v", rejected, err)
	}
}

type cancelRuntimeStub struct {
	cancelCalls   int
	sequenceCalls int
	cancelErr     error
	sequence      int64
	sequenceErr   error
}

func (*cancelRuntimeStub) Begin(context.Context, *a2aext.ExecutionRequest, string, string, string) (*a2aruntime.Turn, error) {
	return nil, errors.New("unexpected Begin")
}

func (*cancelRuntimeStub) Start(string) error { return errors.New("unexpected Start") }

func (*cancelRuntimeStub) ResumeInteraction(context.Context, *a2aext.ExecutionRequest) (<-chan a2aruntime.Update, error) {
	return nil, errors.New("unexpected ResumeInteraction")
}

func (r *cancelRuntimeStub) Cancel(context.Context, string) error {
	r.cancelCalls++
	return r.cancelErr
}

func (*cancelRuntimeStub) Abort(string, error) {}

func (r *cancelRuntimeStub) LastSequence(string) (int64, error) {
	r.sequenceCalls++
	return r.sequence, r.sequenceErr
}
