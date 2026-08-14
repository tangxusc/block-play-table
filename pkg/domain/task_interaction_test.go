package domain

import (
	"errors"
	"testing"
	"time"
)

func TestTaskRuntimeInteractionAndSessionBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	task := &Task{ID: "task", Status: TaskRunning, Version: 1}
	if err := task.RequestInteraction("interaction", TaskInteractionCommandApproval, "批准命令", now, "", "session-1"); err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskWaitingInput || task.AgentSessionID != "session-1" {
		t.Fatalf("交互请求未保存等待状态和 session: %+v", task)
	}
	if err := task.RecordInteractionAnswered("interaction", TaskInteractionApprove, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := task.RecordInteractionResolved("interaction", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := task.Resume(now.Add(3 * time.Second)); err != nil || task.Status != TaskRunning {
		t.Fatalf("Resume 错误: status=%s err=%v", task.Status, err)
	}
	beforeVersion := task.Version
	task.RememberAgentSession("session-1", now.Add(4*time.Second))
	task.RememberAgentSession("", now.Add(4*time.Second))
	if task.Version != beforeVersion {
		t.Fatal("空值或相同 session 不应推进版本")
	}
	task.RememberAgentSession("session-2", now.Add(5*time.Second))
	if task.AgentSessionID != "session-2" || task.Version != beforeVersion+1 {
		t.Fatalf("新 session 未持久化: %+v", task)
	}

	invalid := &Task{ID: "invalid", Status: TaskCompleted}
	for name, err := range map[string]error{
		"request":  invalid.RequestInteraction("i", TaskInteractionUserInput, "title", now),
		"answered": invalid.RecordInteractionAnswered("i", TaskInteractionDeny, now),
		"resolved": invalid.RecordInteractionResolved("i", now),
		"resume":   invalid.Resume(now),
	} {
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s 非法状态错误=%v", name, err)
		}
	}
}

func TestNewTaskInteractionDefaultsAndValidation(t *testing.T) {
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	interaction, err := NewTaskInteraction(TaskInteraction{
		ID: "interaction", TaskID: "task", Kind: TaskInteractionPermissionApproval, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if interaction.Status != TaskInteractionPending || !interaction.CreatedAt.Equal(now) || !interaction.UpdatedAt.Equal(now) {
		t.Fatalf("交互默认值错误: %+v", interaction)
	}
	createdOnly, err := NewTaskInteraction(TaskInteraction{
		ID: "interaction-2", TaskID: "task", Kind: TaskInteractionFileApproval,
		Status: TaskInteractionAnswered, ResponseDecision: TaskInteractionApproveForSession, CreatedAt: now,
	})
	if err != nil || !createdOnly.UpdatedAt.Equal(now) {
		t.Fatalf("CreatedAt 回填错误: %+v err=%v", createdOnly, err)
	}
	for _, mutate := range []func(*TaskInteraction){
		func(input *TaskInteraction) { input.ID = "" },
		func(input *TaskInteraction) { input.TaskID = " " },
		func(input *TaskInteraction) { input.Kind = TaskInteractionKind("UNKNOWN") },
		func(input *TaskInteraction) { input.Status = TaskInteractionStatus("UNKNOWN") },
		func(input *TaskInteraction) { input.ResponseDecision = TaskInteractionDecision("UNKNOWN") },
	} {
		input := TaskInteraction{ID: "interaction", TaskID: "task", Kind: TaskInteractionUserInput}
		mutate(&input)
		if _, err := NewTaskInteraction(input); err == nil {
			t.Fatalf("非法交互未被拒绝: %+v", input)
		}
	}
	for _, kind := range []TaskInteractionKind{TaskInteractionUserInput, TaskInteractionCommandApproval, TaskInteractionFileApproval, TaskInteractionPermissionApproval} {
		if !kind.Valid() {
			t.Fatalf("合法 kind %q 被拒绝", kind)
		}
	}
	if TaskInteractionKind("UNKNOWN").Valid() || TaskInteractionStatus("UNKNOWN").Valid() || TaskInteractionDecision("UNKNOWN").Valid() {
		t.Fatal("交互闭集接受了未知值")
	}
}
