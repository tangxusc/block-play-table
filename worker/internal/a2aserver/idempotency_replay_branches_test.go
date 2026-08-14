package a2aserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestIdempotentHandlerReturnsBoundTaskForSynchronousAndStreamingReplay(t *testing.T) {
	ctx := context.Background()
	storage := newExecutorTestStore(t)
	execution := serverTestRequest("START")
	if _, created, err := storage.ReserveCommand(ctx, execution); err != nil || !created {
		t.Fatalf("ReserveCommand = created=%v err=%v", created, err)
	}
	if _, err := storage.BindCommand(ctx, execution, "task", "context"); err != nil {
		t.Fatal(err)
	}
	task := &a2a.Task{ID: "task", ContextID: "context", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}
	if _, err := storage.Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	handler := &idempotentHandler{store: storage, workerID: "worker-1"}
	request := &a2a.SendMessageRequest{Message: serverTestMessage(execution)}
	result, err := handler.SendMessage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, ok := result.(*a2a.Task)
	if !ok || replayed.ID != task.ID {
		t.Fatalf("同步重放结果 = %#v", result)
	}
	var streamed a2a.Event
	for event, streamErr := range handler.SendStreamingMessage(ctx, request) {
		if streamErr != nil {
			t.Fatal(streamErr)
		}
		streamed = event
	}
	streamedTask, ok := streamed.(*a2a.Task)
	if !ok || streamedTask.ID != task.ID {
		t.Fatalf("流式重放结果 = %#v", streamed)
	}
}

func TestExecutorInteractionBindingRejectsMissingBinding(t *testing.T) {
	executor := &AgentExecutor{store: newExecutorTestStore(t)}
	request := serverTestRequest("INTERACTION_RESPONSE")
	if err := executor.validateInteractionBinding(context.Background(), nil, request); err == nil {
		t.Fatal("缺失 runtime binding 未失败")
	}
}

func TestExecutorInteractionBindingRejectsIdentityDriftDuringExecute(t *testing.T) {
	ctx := context.Background()
	storage := newExecutorTestStore(t)
	request := serverTestRequest(a2aext.OperationInteractionResponse)
	request.Worktree.Mode = a2aext.WorktreeResume
	request.Resume = &a2aext.Resume{AgentSessionID: "session", WorktreePath: "/tmp/worktree"}
	request.Interaction = &a2aext.Interaction{ID: "interaction", Decision: a2aext.DecisionRespond}
	if err := storage.SaveBinding(ctx, a2astore.RuntimeBinding{
		ExecutionID: request.Scope.ExecutionID, LocalTaskID: request.Scope.LocalTaskID,
		WorkerID: request.Scope.ExpectedWorkerID, TaskID: "bound-task", ContextID: "context",
		Attempt: request.Scope.Attempt, Turn: request.Scope.Turn, AgentType: request.Agent.Type,
		AgentSessionID: request.Resume.AgentSessionID, WorktreePath: request.Resume.WorktreePath,
	}); err != nil {
		t.Fatal(err)
	}
	executor := &AgentExecutor{
		workerID: request.Scope.ExpectedWorkerID, store: storage, runtime: &interactionRuntimeStub{}, now: time.Now,
		drains: map[string]chan struct{}{}, cancelLocks: map[string]*sync.Mutex{},
	}
	message := serverTestMessage(request)
	message.TaskID = "different-task"
	message.ContextID = "context"
	var got error
	for _, err := range executor.Execute(ctx, &a2asrv.ExecutorContext{
		TaskID: "different-task", ContextID: "context", Message: message,
	}) {
		got = err
	}
	if got == nil {
		t.Fatal("交互回复 Task 身份漂移未失败")
	}
}
