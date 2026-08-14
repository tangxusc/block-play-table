package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

// TestA2AProjectionHelperBranches 覆盖终态映射、诊断格式与事件作用域的边界分支。
func TestA2AProjectionHelperBranches(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	round := &domain.TaskA2ARound{TaskID: "task", ExecutionID: "execution", Attempt: 2, Turn: 3, WorkerID: "worker", LastSequence: 1}
	nonTerminal := &a2aext.ExecutionEvent{Event: a2aext.EventHeader{Type: a2aext.EventLogChunk}}
	terminal := func(status string) *a2aext.ExecutionEvent {
		return &a2aext.ExecutionEvent{Event: a2aext.EventHeader{Type: a2aext.EventExecutionTerminal}, Payload: map[string]any{"status": status}}
	}

	for _, test := range []struct {
		name  string
		event *a2aext.ExecutionEvent
		want  domain.TaskA2ARemoteStatus
	}{
		{name: "nil", want: domain.TaskA2ARemoteStatusUnspecified},
		{name: "non terminal", event: nonTerminal, want: domain.TaskA2ARemoteStatusUnspecified},
		{name: "completed", event: terminal("COMPLETED"), want: domain.TaskA2ARemoteStatusCompleted},
		{name: "completed sdk", event: terminal("task_state_completed"), want: domain.TaskA2ARemoteStatusCompleted},
		{name: "failed", event: terminal("FAILED"), want: domain.TaskA2ARemoteStatusFailed},
		{name: "failed sdk", event: terminal("TASK_STATE_FAILED"), want: domain.TaskA2ARemoteStatusFailed},
		{name: "rejected", event: terminal("REJECTED"), want: domain.TaskA2ARemoteStatusRejected},
		{name: "rejected sdk", event: terminal("TASK_STATE_REJECTED"), want: domain.TaskA2ARemoteStatusRejected},
		{name: "canceled", event: terminal("CANCELED"), want: domain.TaskA2ARemoteStatusCanceled},
		{name: "cancelled", event: terminal("CANCELLED"), want: domain.TaskA2ARemoteStatusCanceled},
		{name: "canceled sdk", event: terminal("TASK_STATE_CANCELED"), want: domain.TaskA2ARemoteStatusCanceled},
		{name: "unknown", event: terminal("OTHER"), want: domain.TaskA2ARemoteStatusUnspecified},
	} {
		t.Run("status/"+test.name, func(t *testing.T) {
			if got := statusFromTerminalPayload(test.event); got != test.want {
				t.Fatalf("statusFromTerminalPayload() = %s, want %s", got, test.want)
			}
		})
	}

	for _, test := range []struct{ code, message, want string }{
		{code: " CODE ", message: " message ", want: "CODE: message"},
		{code: " CODE ", want: "CODE"},
		{message: " message ", want: "message"},
		{want: ""},
	} {
		if got := formatA2ADiagnostic(test.code, test.message); got != test.want {
			t.Fatalf("formatA2ADiagnostic(%q, %q) = %q, want %q", test.code, test.message, got, test.want)
		}
	}

	validScope := a2aext.EventScope{LocalTaskID: "task", ExecutionID: "execution", Attempt: 2, Turn: 3, WorkerID: "worker"}
	if err := validateA2AEventScope(round, &a2aext.ExecutionEvent{Scope: validScope}); err != nil {
		t.Fatalf("valid scope: %v", err)
	}
	mutations := []func(*a2aext.EventScope){
		func(scope *a2aext.EventScope) { scope.LocalTaskID = "other" },
		func(scope *a2aext.EventScope) { scope.ExecutionID = "other" },
		func(scope *a2aext.EventScope) { scope.Attempt++ },
		func(scope *a2aext.EventScope) { scope.Turn++ },
		func(scope *a2aext.EventScope) { scope.WorkerID = "other" },
	}
	for index, mutate := range mutations {
		scope := validScope
		mutate(&scope)
		if err := validateA2AEventScope(round, &a2aext.ExecutionEvent{Scope: scope}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("scope mutation %d err = %v, want conflict", index, err)
		}
	}

	if err := validateA2ATerminalPair(round, A2ARemoteUpdate{Event: nonTerminal}); err != nil {
		t.Fatalf("non-terminal pair: %v", err)
	}
	if err := validateA2ATerminalPair(nil, A2ARemoteUpdate{Status: domain.TaskA2ARemoteStatusRejected}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("rejected without initial round err = %v, want conflict", err)
	}
	initial := *round
	initial.LastSequence = 0
	if err := validateA2ATerminalPair(&initial, A2ARemoteUpdate{Status: domain.TaskA2ARemoteStatusRejected}); err != nil {
		t.Fatalf("initial rejection: %v", err)
	}
	if err := validateA2ATerminalPair(round, A2ARemoteUpdate{Status: domain.TaskA2ARemoteStatusCompleted, Event: terminal("COMPLETED")}); err != nil {
		t.Fatalf("paired completion: %v", err)
	}
	_ = now
}

// TestApplyA2AStatusBranches 覆盖标准 A2A 状态到业务任务状态的全部映射分支。
func TestApplyA2AStatusBranches(t *testing.T) {
	now := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	round := &domain.TaskA2ARound{}
	event := func(status string, payload map[string]any, runtime *a2aext.RuntimeInfo) *a2aext.ExecutionEvent {
		if payload == nil {
			payload = map[string]any{}
		}
		if status != "" {
			payload["status"] = status
		}
		return &a2aext.ExecutionEvent{Event: a2aext.EventHeader{Type: a2aext.EventExecutionTerminal}, Runtime: runtime, Payload: payload}
	}

	assertStatus := func(name string, task *domain.Task, status domain.TaskA2ARemoteStatus, remoteEvent *a2aext.ExecutionEvent, want domain.TaskStatus) {
		t.Helper()
		if err := applyA2AStatus(task, round, status, remoteEvent, now); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if task.Status != want {
			t.Fatalf("%s status = %s, want %s", name, task.Status, want)
		}
	}

	assertStatus("submitted", &domain.Task{Status: domain.TaskStarting}, domain.TaskA2ARemoteStatusSubmitted, nil, domain.TaskStarting)
	assertStatus("working start", &domain.Task{Status: domain.TaskStarting}, domain.TaskA2ARemoteStatusWorking,
		&a2aext.ExecutionEvent{Runtime: &a2aext.RuntimeInfo{WorktreePath: "/tmp/worktree"}}, domain.TaskRunning)
	assertStatus("working existing worktree", &domain.Task{Status: domain.TaskStarting, WorktreePath: "/tmp/existing"}, domain.TaskA2ARemoteStatusWorking, nil, domain.TaskRunning)
	assertStatus("working resume", &domain.Task{Status: domain.TaskWaitingInput}, domain.TaskA2ARemoteStatusWorking, nil, domain.TaskRunning)
	assertStatus("working no-op", &domain.Task{Status: domain.TaskRunning}, domain.TaskA2ARemoteStatusWorking, nil, domain.TaskRunning)
	assertStatus("input running", &domain.Task{Status: domain.TaskRunning}, domain.TaskA2ARemoteStatusInputRequired, nil, domain.TaskWaitingInput)
	assertStatus("auth starting", &domain.Task{Status: domain.TaskStarting}, domain.TaskA2ARemoteStatusAuthRequired, nil, domain.TaskWaitingInput)
	assertStatus("input no-op", &domain.Task{Status: domain.TaskCompleted}, domain.TaskA2ARemoteStatusInputRequired, nil, domain.TaskCompleted)
	completed := &domain.Task{Status: domain.TaskRunning}
	assertStatus("completed", completed, domain.TaskA2ARemoteStatusCompleted, event("COMPLETED", map[string]any{"result": "done"}, &a2aext.RuntimeInfo{AgentSessionID: "session"}), domain.TaskCompleted)
	if completed.Result != "done" || completed.AgentSessionID != "session" {
		t.Fatalf("completion projection = %+v", completed)
	}
	assertStatus("completed no-op", &domain.Task{Status: domain.TaskCompleted}, domain.TaskA2ARemoteStatusCompleted, nil, domain.TaskCompleted)

	round.ErrorCode, round.ErrorMessage = "ERR", "failed"
	failed := &domain.Task{Status: domain.TaskRunning}
	assertStatus("failed diagnostic", failed, domain.TaskA2ARemoteStatusFailed, event("FAILED", nil, nil), domain.TaskFailed)
	if failed.Result != "ERR: failed" {
		t.Fatalf("diagnostic failure result = %q", failed.Result)
	}
	round.ErrorCode, round.ErrorMessage = "", ""
	failed = &domain.Task{Status: domain.TaskRunning}
	assertStatus("failed payload", failed, domain.TaskA2ARemoteStatusFailed, event("FAILED", map[string]any{"message": "payload failure"}, nil), domain.TaskFailed)
	if failed.Result != "payload failure" {
		t.Fatalf("payload failure result = %q", failed.Result)
	}
	failed = &domain.Task{Status: domain.TaskRunning}
	assertStatus("rejected fallback", failed, domain.TaskA2ARemoteStatusRejected, event("REJECTED", nil, nil), domain.TaskFailed)
	if failed.Result != string(domain.TaskA2ARemoteStatusRejected) {
		t.Fatalf("fallback failure result = %q", failed.Result)
	}
	assertStatus("failed no-op", &domain.Task{Status: domain.TaskFailed}, domain.TaskA2ARemoteStatusFailed, nil, domain.TaskFailed)
	assertStatus("canceled", &domain.Task{Status: domain.TaskRunning}, domain.TaskA2ARemoteStatusCanceled, event("CANCELED", nil, nil), domain.TaskInterrupted)
	assertStatus("canceled no-op", &domain.Task{Status: domain.TaskCompleted}, domain.TaskA2ARemoteStatusCanceled, nil, domain.TaskCompleted)
	assertStatus("unknown terminal fallback", &domain.Task{Status: domain.TaskRunning}, domain.TaskA2ARemoteStatusUnknown, event("COMPLETED", map[string]any{"content": "fallback"}, nil), domain.TaskCompleted)
	assertStatus("unspecified no-op", &domain.Task{Status: domain.TaskRunning}, domain.TaskA2ARemoteStatusUnspecified, nil, domain.TaskRunning)
}

// TestProjectA2AEventBranches 覆盖各 execution 事件的缺省值、校验和状态变更分支。
func TestProjectA2AEventBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	round := &domain.TaskA2ARound{ID: "round", TaskID: "task", ExecutionID: "execution", WorkerID: "worker"}
	baseEvent := func(eventType a2aext.EventType, payload map[string]any, runtime *a2aext.RuntimeInfo) *a2aext.ExecutionEvent {
		return &a2aext.ExecutionEvent{Event: a2aext.EventHeader{ID: "event", Type: eventType, OccurredAt: now}, Runtime: runtime, Payload: payload}
	}
	project := func(task *domain.Task, event *a2aext.ExecutionEvent) (*a2aBusinessProjection, error) {
		projection := &a2aBusinessProjection{}
		err := service.projectA2AEvent(ctx, task, round, event, projection, now)
		return projection, err
	}

	if _, err := project(&domain.Task{Status: domain.TaskStarting}, baseEvent(a2aext.EventExecutionAccepted, nil, nil)); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []*a2aext.RuntimeInfo{nil, {}} {
		if _, err := project(&domain.Task{Status: domain.TaskStarting}, baseEvent(a2aext.EventWorkspaceReady, nil, runtime)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("workspace runtime %+v err = %v", runtime, err)
		}
	}
	workspaceTask := &domain.Task{Status: domain.TaskStarting}
	if _, err := project(workspaceTask, baseEvent(a2aext.EventWorkspaceReady, nil, &a2aext.RuntimeInfo{WorktreePath: "/tmp/worktree"})); err != nil || workspaceTask.Status != domain.TaskRunning {
		t.Fatalf("workspace projection task=%+v err=%v", workspaceTask, err)
	}
	for _, eventType := range []a2aext.EventType{a2aext.EventAgentSessionStarted, a2aext.EventAgentSessionUpdated} {
		if _, err := project(&domain.Task{Status: domain.TaskRunning}, baseEvent(eventType, nil, nil)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("missing session for %s: %v", eventType, err)
		}
		task := &domain.Task{Status: domain.TaskRunning}
		if _, err := project(task, baseEvent(eventType, nil, &a2aext.RuntimeInfo{AgentSessionID: "session"})); err != nil || task.AgentSessionID != "session" {
			t.Fatalf("session projection for %s task=%+v err=%v", eventType, task, err)
		}
	}

	projection, err := project(&domain.Task{ID: "task", Status: domain.TaskRunning}, baseEvent(a2aext.EventLogChunk, map[string]any{"text": "log"}, nil))
	if err != nil || len(projection.logs) != 1 || projection.logs[0].Stream != string(a2aext.LogSystem) || projection.logs[0].Content != "log" {
		t.Fatalf("log projection=%+v err=%v", projection, err)
	}
	projection, err = project(&domain.Task{ID: "task", Status: domain.TaskRunning}, baseEvent(a2aext.EventConversationMessage, map[string]any{"text": "answer"}, nil))
	if err != nil || len(projection.conversations) != 1 || projection.conversations[0].Role != "assistant" || projection.conversations[0].Content != "answer" {
		t.Fatalf("conversation projection=%+v err=%v", projection, err)
	}
	if _, err := project(&domain.Task{Status: domain.TaskCompleted}, baseEvent(a2aext.EventLogChunk, map[string]any{"content": "late"}, nil)); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("late log err = %v", err)
	}
	if _, err := project(&domain.Task{Status: domain.TaskCompleted}, baseEvent(a2aext.EventConversationMessage, map[string]any{"content": "late"}, nil)); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("late conversation err = %v", err)
	}

	for _, payload := range []map[string]any{
		{"kind": string(a2aext.InteractionUserInput)},
		{"interactionId": "interaction", "kind": "UNKNOWN"},
	} {
		if _, err := project(&domain.Task{Status: domain.TaskRunning}, baseEvent(a2aext.EventInteractionRequested, payload, nil)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("invalid interaction payload %+v err = %v", payload, err)
		}
	}

	diagnosticRound := *round
	round = &diagnosticRound
	projection, err = project(&domain.Task{ID: "task", Status: domain.TaskRunning}, baseEvent(a2aext.EventExecutionDiagnostic, map[string]any{"errorCode": "CODE", "retryable": true}, nil))
	if err != nil || len(projection.logs) != 1 || !round.Retryable || round.ErrorCode != "CODE" {
		t.Fatalf("diagnostic projection=%+v round=%+v err=%v", projection, round, err)
	}
	if _, err := project(&domain.Task{Status: domain.TaskRunning}, baseEvent(a2aext.EventExecutionTerminal, map[string]any{"status": "FAILED", "message": "terminal"}, nil)); err != nil || round.ErrorMessage != "terminal" {
		t.Fatalf("failed terminal round=%+v err=%v", round, err)
	}
	if _, err := project(&domain.Task{Status: domain.TaskRunning}, baseEvent(a2aext.EventExecutionTerminal, map[string]any{"status": "COMPLETED"}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := project(&domain.Task{Status: domain.TaskRunning}, baseEvent(a2aext.EventType("unsupported"), nil, nil)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unsupported event err = %v", err)
	}
}

// TestA2APayloadHelpersBranches 覆盖 payload 和 runtime 提取的缺省及类型分支。
func TestA2APayloadHelpersBranches(t *testing.T) {
	payload := map[string]any{"number": 1, "empty": "", "value": "ok"}
	if got := payloadString(payload, "missing", "number", "value"); got != "ok" {
		t.Fatalf("payloadString = %q", got)
	}
	if got := payloadString(payload, "missing"); got != "" {
		t.Fatalf("missing payloadString = %q", got)
	}
	if got := payloadJSON(nil); got != "" {
		t.Fatalf("nil payloadJSON = %q", got)
	}
	if got := payloadJSON("raw"); got != "raw" {
		t.Fatalf("string payloadJSON = %q", got)
	}
	if got := payloadJSON(map[string]any{"key": "value"}); got != `{"key":"value"}` {
		t.Fatalf("object payloadJSON = %q", got)
	}
	if got := payloadFromEvent(nil, "value"); got != "" {
		t.Fatalf("nil payloadFromEvent = %q", got)
	}
	if got := payloadFromEvent(&a2aext.ExecutionEvent{Payload: payload}, "value"); got != "ok" {
		t.Fatalf("payloadFromEvent = %q", got)
	}
	if got := runtimeSession(nil); got != "" {
		t.Fatalf("nil runtimeSession = %q", got)
	}
	if got := runtimeSession(&a2aext.ExecutionEvent{}); got != "" {
		t.Fatalf("empty runtimeSession = %q", got)
	}
	if got := runtimeSession(&a2aext.ExecutionEvent{Runtime: &a2aext.RuntimeInfo{AgentSessionID: "session"}}); got != "session" {
		t.Fatalf("runtimeSession = %q", got)
	}
}

func TestProjectA2AInteractionDuplicateMissingAndRunningResolutionBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 23, 0, 0, 0, time.UTC)
	storage := store.NewMemoryStore()
	service := NewService(storage)
	round := &domain.TaskA2ARound{ID: "round", TaskID: "task", ExecutionID: "execution", WorkerID: "worker"}
	event := func(eventType a2aext.EventType, interactionID string) *a2aext.ExecutionEvent {
		return &a2aext.ExecutionEvent{
			Event:   a2aext.EventHeader{ID: "event", Type: eventType, OccurredAt: now},
			Payload: map[string]any{"interactionId": interactionID, "kind": string(a2aext.InteractionUserInput)},
		}
	}
	existing := domain.TaskInteraction{
		ID: "existing", TaskID: "task", Kind: domain.TaskInteractionUserInput,
		Status: domain.TaskInteractionPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := storage.SaveTaskInteraction(ctx, existing); err != nil {
		t.Fatal(err)
	}
	if err := service.projectA2AEvent(ctx, &domain.Task{ID: "task", Status: domain.TaskRunning}, round, event(a2aext.EventInteractionRequested, existing.ID), &a2aBusinessProjection{}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("重复 interaction.requested 错误 = %v", err)
	}
	if err := service.projectA2AEvent(ctx, &domain.Task{ID: "task", Status: domain.TaskRunning}, round, event(a2aext.EventInteractionResolved, "missing"), &a2aBusinessProjection{}, now); !errors.Is(err, ErrA2AProjection) {
		t.Fatalf("缺失 interaction.resolved 错误 = %v", err)
	}
	running := &domain.Task{ID: "task", Status: domain.TaskRunning}
	projection := &a2aBusinessProjection{}
	if err := service.projectA2AEvent(ctx, running, round, event(a2aext.EventInteractionResolved, existing.ID), projection, now); err != nil {
		t.Fatal(err)
	}
	if running.Status != domain.TaskRunning || projection.interaction == nil || projection.interaction.Status != domain.TaskInteractionAnswered {
		t.Fatalf("运行态交互解决投影 = task=%+v projection=%+v", running, projection)
	}
}
