package a2astore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestCommandReservationBindingAndAbandonContracts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	request := testExecutionRequest("command-contract", "secret")
	if _, _, err := store.ClaimCommand(ctx, request, "", "context"); err == nil {
		t.Fatal("ClaimCommand 空 taskID 未失败")
	}
	record, created, err := store.ReserveCommand(ctx, request)
	if err != nil || !created || record.TaskID != "" {
		t.Fatalf("ReserveCommand=%+v created=%v err=%v", record, created, err)
	}
	if _, _, err := store.ClaimCommand(ctx, request, "task", "context"); !errors.Is(err, ErrCommandPending) {
		t.Fatalf("pending ClaimCommand 错误=%v", err)
	}
	if _, err := store.BindCommand(ctx, request, "", "context"); err == nil {
		t.Fatal("BindCommand 空 taskID 未失败")
	}
	unknown := testExecutionRequest("unknown-command", "secret")
	if _, err := store.BindCommand(ctx, unknown, "task", "context"); !errors.Is(err, ErrCommandPending) {
		t.Fatalf("未知 reservation bind 错误=%v", err)
	}
	bound, err := store.BindCommand(ctx, request, "task", "context")
	if err != nil || bound.TaskID != "task" || bound.ContextID != "context" {
		t.Fatalf("BindCommand=%+v err=%v", bound, err)
	}
	if replay, err := store.BindCommand(ctx, request, "task", "context"); err != nil || replay.TaskID != "task" {
		t.Fatalf("BindCommand 重放=%+v err=%v", replay, err)
	}
	if _, err := store.BindCommand(ctx, request, "other-task", "context"); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("BindCommand 冲突错误=%v", err)
	}
	if deleted, err := store.AbandonCommand(ctx, request); err != nil || deleted {
		t.Fatalf("已绑定 AbandonCommand deleted=%v err=%v", deleted, err)
	}
	if _, err := store.AbandonBoundCommand(ctx, request, " "); err == nil {
		t.Fatal("AbandonBoundCommand 空 taskID 未失败")
	}
	if deleted, err := store.AbandonBoundCommand(ctx, request, "other-task"); err != nil || deleted {
		t.Fatalf("错误 task AbandonBoundCommand deleted=%v err=%v", deleted, err)
	}
	if deleted, err := store.AbandonBoundCommand(ctx, request, "task"); err != nil || !deleted {
		t.Fatalf("AbandonBoundCommand deleted=%v err=%v", deleted, err)
	}
	if _, err := store.LookupCommand(ctx, request.Command.ID); err == nil {
		t.Fatal("删除后的 command 仍可查询")
	}

	failed := testExecutionRequest("command-failed-bound", "secret")
	if _, created, err := store.ReserveCommand(ctx, failed); err != nil || !created {
		t.Fatal(err)
	}
	if _, err := store.BindCommand(ctx, failed, "failed-task", "failed-context"); err != nil {
		t.Fatal(err)
	}
	binding := contractBinding("execution", "failed-task", "failed-context")
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AbandonFailedBoundCommand(ctx, failed, ""); err == nil {
		t.Fatal("AbandonFailedBoundCommand 空 taskID 未失败")
	}
	conflict := *failed
	conflict.Task.Title = "different"
	if _, err := store.AbandonFailedBoundCommand(ctx, &conflict, "failed-task"); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("失败 command 内容冲突错误=%v", err)
	}
	if deleted, err := store.AbandonFailedBoundCommand(ctx, failed, "failed-task"); err != nil || !deleted {
		t.Fatalf("AbandonFailedBoundCommand deleted=%v err=%v", deleted, err)
	}
	if _, err := store.GetBinding(ctx, binding.ExecutionID); err == nil {
		t.Fatal("失败 command 的孤儿 binding 未删除")
	}
	if deleted, err := store.AbandonFailedBoundCommand(ctx, failed, "failed-task"); err != nil || deleted {
		t.Fatalf("重复清理 deleted=%v err=%v", deleted, err)
	}
}

func TestRuntimeBindingConsistencyAndTaskLookup(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.SaveBinding(ctx, RuntimeBinding{}); err == nil {
		t.Fatal("空 binding 未失败")
	}
	binding := contractBinding("execution-binding", "task-binding", "context-binding")
	binding.AgentSessionID = "session"
	binding.WorktreePath = "/tmp/worktree"
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	byTask, err := store.GetBindingByTask(ctx, binding.TaskID)
	if err != nil || byTask.ExecutionID != binding.ExecutionID {
		t.Fatalf("GetBindingByTask=%+v err=%v", byTask, err)
	}
	if _, err := store.GetBindingByTask(ctx, "missing"); err == nil {
		t.Fatal("缺失 Task binding 未失败")
	}
	updated := binding
	updated.TaskID = "task-binding-turn-2"
	updated.Turn = 2
	updated.AgentSessionID = ""
	updated.WorktreePath = ""
	updated.State = "INPUT_REQUIRED"
	if err := store.SaveBinding(ctx, updated); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetBinding(ctx, binding.ExecutionID)
	if err != nil || got.AgentSessionID != "session" || got.WorktreePath != "/tmp/worktree" || got.Turn != 2 {
		t.Fatalf("binding 更新=%+v err=%v", got, err)
	}
	for name, mutate := range map[string]func(*RuntimeBinding){
		"local task": func(value *RuntimeBinding) { value.LocalTaskID = "other" },
		"worker":     func(value *RuntimeBinding) { value.WorkerID = "other" },
		"context":    func(value *RuntimeBinding) { value.ContextID = "other" },
		"attempt":    func(value *RuntimeBinding) { value.Attempt = 2 },
		"agent":      func(value *RuntimeBinding) { value.AgentType = a2aext.AgentClaude },
		"turn":       func(value *RuntimeBinding) { value.Turn = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *got
			mutate(&candidate)
			if err := store.SaveBinding(ctx, candidate); !errors.Is(err, ErrProtocolConflict) {
				t.Fatalf("binding 冲突错误=%v", err)
			}
		})
	}
}

func TestEventJournalSequenceDuplicateAndConflictContracts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	binding := contractBinding("execution-events", "task-events", "context-events")
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, &a2aext.ExecutionEvent{}); err == nil {
		t.Fatal("非法 event 未失败")
	}
	missingBindingEvent := contractEvent(t, contractBinding("missing-execution", "task", "context"), 1, "line")
	if _, err := store.AppendEvent(ctx, missingBindingEvent); err == nil {
		t.Fatal("缺失 binding event 未失败")
	}
	gap := contractEvent(t, binding, 2, "gap")
	if _, err := store.AppendEvent(ctx, gap); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("sequence 缺号错误=%v", err)
	}
	first := contractEvent(t, binding, 1, "line")
	if duplicate, err := store.AppendEvent(ctx, first); err != nil || duplicate {
		t.Fatalf("首次 AppendEvent duplicate=%v err=%v", duplicate, err)
	}
	if duplicate, err := store.AppendEvent(ctx, first); err != nil || !duplicate {
		t.Fatalf("重复 AppendEvent duplicate=%v err=%v", duplicate, err)
	}
	conflict := *first
	conflict.Payload = map[string]any{"stream": "stdout", "content": "different"}
	if _, err := store.AppendEvent(ctx, &conflict); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("eventId 内容冲突错误=%v", err)
	}
	second := contractEvent(t, binding, 2, "second")
	if duplicate, err := store.AppendEvent(ctx, second); err != nil || duplicate {
		t.Fatalf("第二事件 duplicate=%v err=%v", duplicate, err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err := store.ListEvents(ctx, binding.ExecutionID, 0, limit); err == nil {
			t.Fatalf("limit %d 未失败", limit)
		}
	}
	events, err := store.ListEvents(ctx, binding.ExecutionID, 1, 1)
	if err != nil || len(events) != 1 || events[0].Event.Sequence != 2 {
		t.Fatalf("ListEvents=%+v err=%v", events, err)
	}
	if normalizedState(" task_state_working ") != "WORKING" || normalizedState("completed") != "COMPLETED" {
		t.Fatal("normalizedState 错误")
	}
}

func contractBinding(executionID, taskID, contextID string) RuntimeBinding {
	return RuntimeBinding{
		ExecutionID: executionID, LocalTaskID: "local-" + executionID, WorkerID: "worker",
		TaskID: taskID, ContextID: contextID, Attempt: 1, Turn: 1, AgentType: a2aext.AgentCodex, State: "WORKING",
	}
}

func contractEvent(t *testing.T, binding RuntimeBinding, sequence int64, content string) *a2aext.ExecutionEvent {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	event := &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event: a2aext.EventHeader{ID: id.String(), Sequence: sequence, Type: a2aext.EventLogChunk, OccurredAt: time.Now().UTC()},
		Scope: a2aext.EventScope{
			LocalTaskID: binding.LocalTaskID, ExecutionID: binding.ExecutionID,
			Attempt: binding.Attempt, Turn: binding.Turn, WorkerID: binding.WorkerID,
		},
		Payload: map[string]any{"stream": "stdout", "content": content},
	}
	if err := a2aext.ValidateEvent(event); err != nil {
		t.Fatal(err)
	}
	return event
}
