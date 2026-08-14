package a2aserver

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aadapter"
	"github.com/tangxusc/block-play-table/worker/internal/a2aruntime"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestAgentExecutorStopsWaitingForRuntimeWhenSDKContextIsCanceled(t *testing.T) {
	store := newExecutorTestStore(t)
	request := serverTestRequest(a2aext.OperationStart)
	if _, created, err := store.ReserveCommand(context.Background(), request); err != nil || !created {
		t.Fatalf("预留 command: created=%v err=%v", created, err)
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	t.Cleanup(rootCancel)
	runtime, err := a2aruntime.New(a2aruntime.Config{
		Context: rootCtx, WorkerID: "worker-1", WorkDir: t.TempDir(), Store: store,
		Adapters: map[a2aext.AgentType]a2aadapter.Adapter{a2aext.AgentCodex: idleExecutorAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewAgentExecutor("worker-1", store, runtime)
	message := serverTestMessage(request)
	execCtx := &a2asrv.ExecutorContext{Message: message, TaskID: "task-sdk-cancel", ContextID: "context-sdk-cancel"}
	sdkCtx, sdkCancel := context.WithCancel(context.Background())
	t.Cleanup(sdkCancel)
	result := make(chan error, 1)
	go func() {
		seenTask := false
		for event, executeErr := range executor.Execute(sdkCtx, execCtx) {
			if executeErr != nil {
				result <- executeErr
				return
			}
			if _, ok := event.(*a2a.Task); ok {
				seenTask = true
				sdkCancel()
			}
		}
		if !seenTask {
			result <- errors.New("Executor 未产生首个 Task")
			return
		}
		result <- nil
	}()

	select {
	case executeErr := <-result:
		if executeErr != nil {
			t.Fatal(executeErr)
		}
	case <-time.After(500 * time.Millisecond):
		rootCancel()
		<-result
		t.Fatal("SDK context 取消后 Executor 仍等待 Runtime 更新")
	}
}

func TestAgentExecutorInteractionFailureRemainsRetryableWithoutAbortingRuntime(t *testing.T) {
	store := newExecutorTestStore(t)
	request := serverTestRequest(a2aext.OperationInteractionResponse)
	request.Command.ID = "command-interaction-retry"
	request.Worktree.Mode = a2aext.WorktreeResume
	request.Resume = &a2aext.Resume{AgentSessionID: "session-interaction", WorktreePath: "/tmp/worktree-interaction"}
	request.Interaction = &a2aext.Interaction{ID: "wrong-interaction", Decision: a2aext.DecisionRespond, Message: "回复"}
	task := &a2a.Task{
		ID: "task-interaction-retry", ContextID: "context-interaction-retry",
		Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired},
	}
	if _, err := store.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBinding(context.Background(), a2astore.RuntimeBinding{
		ExecutionID: request.Scope.ExecutionID, LocalTaskID: request.Scope.LocalTaskID, WorkerID: request.Scope.ExpectedWorkerID,
		TaskID: string(task.ID), ContextID: task.ContextID, Attempt: request.Scope.Attempt, Turn: request.Scope.Turn,
		AgentType: request.Agent.Type, AgentSessionID: request.Resume.AgentSessionID, WorktreePath: request.Resume.WorktreePath,
		State: "INPUT_REQUIRED",
	}); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.ReserveCommand(context.Background(), request); err != nil || !created {
		t.Fatalf("预留交互 command: created=%v err=%v", created, err)
	}
	runtime := &interactionRuntimeStub{resumeErr: errors.New("交互 ID 与当前等待项不匹配")}
	executor := &AgentExecutor{
		workerID: "worker-1", store: store, runtime: runtime, now: time.Now,
		drains: map[string]chan struct{}{}, cancelLocks: map[string]*sync.Mutex{},
	}
	message := serverTestMessage(request)
	message.TaskID = task.ID
	message.ContextID = task.ContextID
	execCtx := &a2asrv.ExecutorContext{Message: message, TaskID: task.ID, ContextID: task.ContextID}

	var executeErr error
	for _, err := range executor.Execute(context.Background(), execCtx) {
		executeErr = err
	}
	if executeErr == nil {
		t.Fatal("错误交互 ID 未返回失败")
	}
	executor.Cleanup(context.Background(), execCtx, nil, executeErr)
	if runtime.aborts != 0 {
		t.Fatalf("交互投递失败中止了原 Runtime: %d", runtime.aborts)
	}
	if _, err := store.LookupCommand(context.Background(), request.Command.ID); err == nil {
		t.Fatal("交互投递失败后仍保留 bound command")
	}

	if _, created, err := store.ReserveCommand(context.Background(), request); err != nil || !created {
		t.Fatalf("重新预留交互 command: created=%v err=%v", created, err)
	}
	runtime.resumeErr = nil
	runtime.updates = make(chan a2aruntime.Update)
	close(runtime.updates)
	for _, err := range executor.Execute(context.Background(), execCtx) {
		if err != nil {
			t.Fatalf("重试交互回复失败: %v", err)
		}
	}
	if runtime.resumeCalls != 2 || runtime.aborts != 0 {
		t.Fatalf("交互重试调用次数=%d，中止次数=%d", runtime.resumeCalls, runtime.aborts)
	}
}

func TestSubmittedTaskCreateDeduplicatesAcceptedEvent(t *testing.T) {
	store := newExecutorTestStore(t)
	request := serverTestRequest(a2aext.OperationStart)
	request.Commands = a2aext.Commands{}
	request.Environment = a2aext.Environment{}
	now := time.Now().UTC()
	eventID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	accepted := &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event: a2aext.EventHeader{ID: eventID.String(), Sequence: 1, Type: a2aext.EventExecutionAccepted, OccurredAt: now},
		Scope: a2aext.EventScope{
			LocalTaskID: request.Scope.LocalTaskID, ExecutionID: request.Scope.ExecutionID,
			Attempt: request.Scope.Attempt, Turn: request.Scope.Turn, WorkerID: request.Scope.ExpectedWorkerID,
		},
		Payload: map[string]any{},
	}
	execCtx := &a2asrv.ExecutorContext{
		Message: serverTestMessage(request), TaskID: "task-create-regression", ContextID: "context-create-regression",
	}
	if err := store.SaveBinding(context.Background(), a2astore.RuntimeBinding{
		ExecutionID: request.Scope.ExecutionID, LocalTaskID: request.Scope.LocalTaskID,
		WorkerID: request.Scope.ExpectedWorkerID, TaskID: string(execCtx.TaskID), ContextID: execCtx.ContextID,
		Attempt: request.Scope.Attempt, Turn: request.Scope.Turn, AgentType: request.Agent.Type, State: "SUBMITTED",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := submittedTask(execCtx, execCtx.Message, accepted)
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(*task); err != nil {
		t.Fatalf("SDK gob DeepCopy 编码首个 Task: %v", err)
	}
	var copied a2a.Task
	if err := gob.NewDecoder(&encoded).Decode(&copied); err != nil {
		t.Fatalf("SDK gob DeepCopy 解码首个 Task: %v", err)
	}
	version, err := store.Create(context.Background(), task)
	if err != nil {
		t.Fatalf("SDK 首个 Task Create 失败: %v", err)
	}
	if version != 1 {
		t.Fatalf("Task version=%d，期望 1", version)
	}
	events, err := store.ListEvents(context.Background(), request.Scope.ExecutionID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event.ID != accepted.Event.ID {
		t.Fatalf("accepted journal=%+v，期望精确去重为一条", events)
	}
}

func newExecutorTestStore(t *testing.T) *a2astore.Store {
	t.Helper()
	store, err := a2astore.Open(context.Background(), a2astore.Config{
		Path: filepath.Join(t.TempDir(), "a2a.db"),
		Authenticator: func(context.Context) (string, error) {
			return "executor-regression", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type idleExecutorAdapter struct{}

// Probe 返回取消回归测试使用的固定 Adapter 信息。
func (idleExecutorAdapter) Probe(context.Context) (a2aadapter.Info, error) {
	return a2aadapter.Info{Binary: "idle", Version: "test"}, nil
}

// Run 标记测试错误，因为首个 Task 未落库时不应启动 Adapter。
func (idleExecutorAdapter) Run(context.Context, a2aadapter.Input, func(a2aadapter.Event)) error {
	return fmt.Errorf("首个 Task 未落库却启动了 Adapter")
}

type interactionRuntimeStub struct {
	resumeErr   error
	updates     chan a2aruntime.Update
	resumeCalls int
	aborts      int
}

func (*interactionRuntimeStub) Begin(context.Context, *a2aext.ExecutionRequest, string, string, string) (*a2aruntime.Turn, error) {
	return nil, errors.New("测试未实现 Begin")
}

func (*interactionRuntimeStub) Start(string) error {
	return errors.New("测试未实现 Start")
}

func (r *interactionRuntimeStub) ResumeInteraction(context.Context, *a2aext.ExecutionRequest) (<-chan a2aruntime.Update, error) {
	r.resumeCalls++
	return r.updates, r.resumeErr
}

func (*interactionRuntimeStub) Cancel(context.Context, string) error {
	return errors.New("测试未实现 Cancel")
}

func (r *interactionRuntimeStub) Abort(string, error) {
	r.aborts++
}

func (*interactionRuntimeStub) LastSequence(string) (int64, error) {
	return 0, errors.New("测试未实现 LastSequence")
}
