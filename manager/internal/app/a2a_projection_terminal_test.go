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

// TestApplyA2ARemoteUpdateRequiresPairedTerminalStatus 验证扩展终态不能脱离标准 A2A 终态结束业务任务。
func TestApplyA2ARemoteUpdateRequiresPairedTerminalStatus(t *testing.T) {
	for _, test := range []struct {
		name   string
		status domain.TaskA2ARemoteStatus
	}{
		{name: "missing standard status"},
		{name: "mismatched standard status", status: domain.TaskA2ARemoteStatusFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
			service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
			task := seedRunningTask(t, ctx, service)
			round, terminal := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, nil, map[string]any{
				"status": string(a2aext.TerminalCompleted),
				"result": "must not commit",
			})

			err := applyTestA2ARawEvent(ctx, service, round, terminal, test.status)
			if !errors.Is(err, ErrA2AProtocolConflict) {
				t.Fatalf("未配对终态错误 = %v，期望协议冲突", err)
			}
			persistedTask := loadTestA2ATask(t, ctx, service, task.ID)
			if persistedTask.Status != task.Status || persistedTask.Result != task.Result {
				t.Fatalf("非法终态改变了 Task: before=%+v after=%+v", task, persistedTask)
			}
			persistedRound, err := service.Store().TaskA2ARound(ctx, round.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persistedRound.CompletedAt != nil || persistedRound.LastSequence != round.LastSequence {
				t.Fatalf("非法终态改变了 round: before=%+v after=%+v", round, persistedRound)
			}
			if _, err := service.Store().A2AEventInbox(ctx, round.ID, terminal.Event.ID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("非法终态写入了 inbox: %v", err)
			}
		})
	}
}

// TestApplyA2ARemoteUpdateRejectsUnpairedStandardTerminal 验证已建立 execution 后的标准终态也必须携带 terminal 事件。
func TestApplyA2ARemoteUpdateRejectsUnpairedStandardTerminal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 20, 30, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedRunningTask(t, ctx, service)
	round, err := service.Store().LatestTaskA2ARound(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, update := range []A2ARemoteUpdate{
		{
			TaskID: round.A2ATaskID, ContextID: round.ContextID,
			Status: domain.TaskA2ARemoteStatusCompleted,
		},
		{
			TaskID: round.A2ATaskID, ContextID: round.ContextID,
			Status: domain.TaskA2ARemoteStatusFailed,
			Event: newTestA2AEvent(t, service, round, "0198d5e6-3150-7bf6-b807-9f4aebcec4c1", round.LastSequence+1,
				a2aext.EventResultUpdated, nil, map[string]any{"result": "not terminal"}),
		},
	} {
		if err := service.ApplyA2ARemoteUpdate(ctx, round.ID, update); !errors.Is(err, ErrA2AProtocolConflict) {
			t.Fatalf("未配对标准终态错误 = %v，期望协议冲突", err)
		}
	}

	persistedTask := loadTestA2ATask(t, ctx, service, task.ID)
	if persistedTask.Status != task.Status || persistedTask.Result != task.Result {
		t.Fatalf("非法标准终态改变了 Task: before=%+v after=%+v", task, persistedTask)
	}
	persistedRound, err := service.Store().TaskA2ARound(ctx, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedRound.CompletedAt != nil || persistedRound.LastSequence != round.LastSequence {
		t.Fatalf("非法标准终态改变了 round: before=%+v after=%+v", round, persistedRound)
	}
}

// TestApplyA2ADispatchUpdateAllowsEarlyRejectedWithoutExtensionEvent 验证 Runtime 建立前的标准 REJECTED 保持兼容。
func TestApplyA2ADispatchUpdateAllowsEarlyRejectedWithoutExtensionEvent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 21, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task, _, intent := seedUnconfirmedA2AErrorTestDispatch(t, ctx, service, now)

	err := service.ApplyA2ADispatchUpdate(ctx, intent.ID, A2ARemoteUpdate{
		TaskID: "early-rejected-task", ContextID: "early-rejected-context",
		Status: domain.TaskA2ARemoteStatusRejected,
	})
	if err != nil {
		t.Fatalf("早期 REJECTED 投影失败: %v", err)
	}
	persistedTask := loadTestA2ATask(t, ctx, service, task.ID)
	if persistedTask.Status != domain.TaskFailed {
		t.Fatalf("早期 REJECTED Task 状态 = %s", persistedTask.Status)
	}
	round, err := service.Store().LatestTaskA2ARound(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if round.RemoteStatus != domain.TaskA2ARemoteStatusRejected || round.CompletedAt == nil || round.LastSequence != 0 {
		t.Fatalf("早期 REJECTED round = %+v", round)
	}
}
