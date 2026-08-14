package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTaskA2AOperationAndRemoteStatusClosedSets(t *testing.T) {
	operations := []TaskA2AOperation{
		TaskA2AOperationStart, TaskA2AOperationRetry, TaskA2AOperationContinue,
		TaskA2AOperationInteractionResponse, TaskA2AOperationCancel,
	}
	for _, operation := range operations {
		if !operation.Valid() {
			t.Fatalf("合法 operation %q 被拒绝", operation)
		}
		wantRound := operation == TaskA2AOperationStart || operation == TaskA2AOperationRetry || operation == TaskA2AOperationContinue
		if operation.CreatesRound() != wantRound {
			t.Fatalf("operation %q CreatesRound=%v，期望 %v", operation, operation.CreatesRound(), wantRound)
		}
	}
	if TaskA2AOperation("UNKNOWN").Valid() || TaskA2AOperationCancel.CreatesRound() {
		t.Fatal("闭集接受了未知 operation 或把 CANCEL 识别为新 round")
	}

	terminal := map[TaskA2ARemoteStatus]bool{
		TaskA2ARemoteStatusCompleted: true,
		TaskA2ARemoteStatusFailed:    true,
		TaskA2ARemoteStatusRejected:  true,
		TaskA2ARemoteStatusCanceled:  true,
	}
	for _, status := range []TaskA2ARemoteStatus{
		TaskA2ARemoteStatusUnspecified, TaskA2ARemoteStatusSubmitted, TaskA2ARemoteStatusWorking,
		TaskA2ARemoteStatusInputRequired, TaskA2ARemoteStatusAuthRequired, TaskA2ARemoteStatusCompleted,
		TaskA2ARemoteStatusFailed, TaskA2ARemoteStatusRejected, TaskA2ARemoteStatusCanceled, TaskA2ARemoteStatusUnknown,
	} {
		if status.Terminal() != terminal[status] {
			t.Fatalf("status %q Terminal=%v，期望 %v", status, status.Terminal(), terminal[status])
		}
	}
}

func TestNewTaskA2ARoundRejectsInvalidIdentityAndCounters(t *testing.T) {
	now := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	valid := NewTaskA2ARoundInput{
		ID: "round", TaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1,
		Operation: TaskA2AOperationStart, WorkerID: "worker", CommandID: "command", Now: now,
	}
	tests := []struct {
		name   string
		mutate func(*NewTaskA2ARoundInput)
	}{
		{"round id", func(input *NewTaskA2ARoundInput) { input.ID = " " }},
		{"task id", func(input *NewTaskA2ARoundInput) { input.TaskID = "" }},
		{"execution id", func(input *NewTaskA2ARoundInput) { input.ExecutionID = "" }},
		{"worker id", func(input *NewTaskA2ARoundInput) { input.WorkerID = "" }},
		{"command id", func(input *NewTaskA2ARoundInput) { input.CommandID = "" }},
		{"attempt", func(input *NewTaskA2ARoundInput) { input.Attempt = 0 }},
		{"turn", func(input *NewTaskA2ARoundInput) { input.Turn = 0 }},
		{"sequence", func(input *NewTaskA2ARoundInput) { input.LastSequence = -1 }},
		{"operation", func(input *NewTaskA2ARoundInput) { input.Operation = TaskA2AOperationCancel }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := valid
			testCase.mutate(&input)
			if _, err := NewTaskA2ARound(input); err == nil {
				t.Fatal("非法 round 输入未被拒绝")
			}
		})
	}
	round, err := NewTaskA2ARound(valid)
	if err != nil || round.Version != 1 || round.RemoteStatus != TaskA2ARemoteStatusUnspecified || !round.CreatedAt.Equal(now) {
		t.Fatalf("合法 round 初始化错误: round=%+v err=%v", round, err)
	}
}

func TestTaskA2ARoundBindingObservationAndTerminalGuards(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	newRound := func() *TaskA2ARound {
		round, err := NewTaskA2ARound(NewTaskA2ARoundInput{
			ID: "round", TaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1,
			Operation: TaskA2AOperationStart, WorkerID: "worker", CommandID: "command", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return round
	}

	round := newRound()
	for _, ids := range [][2]string{{"", "context"}, {"task", " "}} {
		if err := round.BindRemote(ids[0], ids[1], TaskA2ARemoteStatusWorking, now); err == nil {
			t.Fatalf("空远端 identity 未被拒绝: %q/%q", ids[0], ids[1])
		}
	}
	if err := round.BindRemote("remote", "context", TaskA2ARemoteStatusUnknown, now); err != nil {
		t.Fatal(err)
	}
	if round.UnknownSince == nil || round.RemoteStatus != TaskA2ARemoteStatusUnspecified {
		t.Fatalf("UNKNOWN 绑定错误: %+v", round)
	}
	if err := round.BindRemote("other", "context", TaskA2ARemoteStatusWorking, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("task binding 冲突错误=%v", err)
	}
	if err := round.BindRemote("remote", "other", TaskA2ARemoteStatusWorking, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("context binding 冲突错误=%v", err)
	}
	if err := round.BindRemote("remote", "context", TaskA2ARemoteStatusWorking, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if round.UnknownSince != nil || round.RemoteStatus != TaskA2ARemoteStatusWorking {
		t.Fatalf("已知状态未清除 unknown: %+v", round)
	}

	if _, err := round.ApplyRemoteEvent(-1, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("sequence 回退错误=%v", err)
	}
	round.MarkUnreachable(now)
	if changed, err := round.ApplyRemoteEvent(2, now.Add(2*time.Second)); err != nil || !changed || round.LastSequence != 2 || round.UnreachableSince != nil {
		t.Fatalf("事件观测错误: changed=%v round=%+v err=%v", changed, round, err)
	}
	if changed, err := round.ApplyRemoteSnapshot(TaskA2ARemoteStatusUnspecified, 2, now.Add(3*time.Second)); err != nil || !changed || round.UnknownSince == nil {
		t.Fatalf("未知快照错误: changed=%v round=%+v err=%v", changed, round, err)
	}
	if changed, err := round.ApplyRemoteSnapshot(TaskA2ARemoteStatusCompleted, 3, now.Add(4*time.Second)); err != nil || !changed || round.CompletedAt == nil || round.UnknownSince != nil {
		t.Fatalf("终态快照错误: changed=%v round=%+v err=%v", changed, round, err)
	}
	if _, err := round.ApplyRemoteSnapshot(TaskA2ARemoteStatusFailed, 4, now.Add(5*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("终态回退错误=%v", err)
	}

	terminalAtBind := newRound()
	if err := terminalAtBind.BindRemote("remote", "context", TaskA2ARemoteStatusCanceled, now); err != nil || terminalAtBind.CompletedAt == nil {
		t.Fatalf("终态绑定未完成 round: %+v err=%v", terminalAtBind, err)
	}
	if !terminalAtBind.MarkUnreachable(now.Add(time.Second)) || terminalAtBind.MarkUnreachable(now.Add(2*time.Second)) {
		t.Fatal("MarkUnreachable 首次/重复语义错误")
	}
	terminalAtBind.SetError("SAFE_CODE", "safe message", now.Add(3*time.Second))
	if terminalAtBind.ErrorCode != "SAFE_CODE" || terminalAtBind.CompletedAt == nil {
		t.Fatalf("SetError 未保存终态: %+v", terminalAtBind)
	}
}

func TestTaskA2ADispatchIntentLifecycleAndPayloadIsolation(t *testing.T) {
	now := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	round := &TaskA2ARound{ID: "round", TaskID: "task", ExecutionID: "execution", WorkerID: "worker"}
	for _, testCase := range []struct {
		name      string
		round     *TaskA2ARound
		operation TaskA2AOperation
		commandID string
	}{
		{"nil round", nil, TaskA2AOperationStart, "command"},
		{"invalid operation", round, TaskA2AOperation("UNKNOWN"), "command"},
		{"blank command", round, TaskA2AOperationStart, " "},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewTaskA2ADispatchIntent(testCase.round, testCase.operation, testCase.commandID, nil, now); err == nil {
				t.Fatal("非法 dispatch intent 未被拒绝")
			}
		})
	}
	payload := []byte("payload")
	intent, err := NewTaskA2ADispatchIntent(round, TaskA2AOperationStart, "command", payload, now)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X'
	if string(intent.Payload) != "payload" || intent.Status != A2ADispatchPending || intent.Version != 1 {
		t.Fatalf("dispatch intent 初始化错误: %+v", intent)
	}
	if err := intent.MarkSent(now); !errors.Is(err, ErrConflict) {
		t.Fatalf("PENDING MarkSent 错误=%v", err)
	}
	if err := intent.StartAttempt(now.Add(time.Second)); err != nil || intent.Status != A2ADispatchSending || intent.AttemptCount != 1 {
		t.Fatalf("StartAttempt 错误: %+v err=%v", intent, err)
	}
	if err := intent.StartAttempt(now.Add(2 * time.Second)); err != nil || intent.AttemptCount != 2 {
		t.Fatalf("SENDING 重领错误: %+v err=%v", intent, err)
	}
	if err := intent.MarkSent(now.Add(3 * time.Second)); err != nil || intent.SentAt == nil {
		t.Fatalf("MarkSent 错误: %+v err=%v", intent, err)
	}
	if err := intent.StartAttempt(now); !errors.Is(err, ErrConflict) {
		t.Fatalf("SENT 重领错误=%v", err)
	}
	retryAt := now.Add(time.Minute)
	if err := intent.ScheduleRetry("NET", "temporary", retryAt, now.Add(4*time.Second)); err != nil || intent.Status != A2ADispatchPending || !intent.AvailableAt.Equal(retryAt) {
		t.Fatalf("ScheduleRetry 错误: %+v err=%v", intent, err)
	}
	intent.Complete(now.Add(5 * time.Second))
	if intent.Status != A2ADispatchCompleted || intent.ErrorCode != "" || intent.CompletedAt == nil {
		t.Fatalf("Complete 错误: %+v", intent)
	}
	if err := intent.ScheduleRetry("NET", "again", retryAt, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("COMPLETED 重试错误=%v", err)
	}
	intent.Fail("FATAL", "safe", now.Add(6*time.Second))
	if intent.Status != A2ADispatchFailed || intent.ErrorCode != "FATAL" || !strings.Contains(intent.ErrorMessage, "safe") {
		t.Fatalf("Fail 错误: %+v", intent)
	}
}
