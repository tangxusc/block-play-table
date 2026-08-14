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

// TestApplyA2ARemoteUpdateRequiresCompleteRemoteIdentity 验证已绑定 round 的每次更新都必须携带完整远端身份。
func TestApplyA2ARemoteUpdateRequiresCompleteRemoteIdentity(t *testing.T) {
	for _, test := range []struct {
		name         string
		clearTaskID  bool
		clearContext bool
	}{
		{name: "missing task id", clearTaskID: true},
		{name: "missing context id", clearContext: true},
		{name: "missing both", clearTaskID: true, clearContext: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
				return time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
			}))
			task := seedRunningTask(t, ctx, service)
			round, err := service.Store().LatestTaskA2ARound(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			update := A2ARemoteUpdate{
				TaskID: round.A2ATaskID, ContextID: round.ContextID,
				Status: domain.TaskA2ARemoteStatusWorking,
			}
			if test.clearTaskID {
				update.TaskID = ""
			}
			if test.clearContext {
				update.ContextID = ""
			}

			if err := service.ApplyA2ARemoteUpdate(ctx, round.ID, update); !errors.Is(err, ErrA2AProtocolConflict) {
				t.Fatalf("缺失远端身份错误 = %v，期望协议冲突", err)
			}
			persisted, err := service.Store().TaskA2ARound(ctx, round.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Version != round.Version || persisted.LastSequence != round.LastSequence {
				t.Fatalf("非法更新改变了 round: before=%+v after=%+v", round, persisted)
			}
		})
	}
}

// TestApplyA2ARemoteUpdateRejectsForeignInteractionResolution 验证远端事件不能解决其他 Task 的交互。
func TestApplyA2ARemoteUpdateRejectsForeignInteractionResolution(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 18, 30, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedRunningTask(t, ctx, service)
	foreign, err := domain.NewTaskInteraction(domain.TaskInteraction{
		ID: "interaction-foreign", TaskID: "task-foreign",
		Kind: domain.TaskInteractionUserInput, Status: domain.TaskInteractionPending,
		Title: "Foreign question", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Store().SaveTaskInteraction(ctx, *foreign); err != nil {
		t.Fatal(err)
	}
	round, resolved := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionResolved, nil, map[string]any{
		"interactionId": foreign.ID,
		"kind":          string(a2aext.InteractionUserInput),
		"decision":      string(a2aext.DecisionRespond),
		"message":       "answer",
	})

	if err := applyTestA2ARawEvent(ctx, service, round, resolved, domain.TaskA2ARemoteStatusWorking); !errors.Is(err, ErrA2AProtocolConflict) {
		t.Fatalf("跨 Task 解决交互错误 = %v，期望协议冲突", err)
	}
	persisted, err := service.Store().TaskInteraction(ctx, foreign.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.TaskID != foreign.TaskID || persisted.Status != domain.TaskInteractionPending {
		t.Fatalf("跨 Task 事件改变了交互: before=%+v after=%+v", foreign, persisted)
	}
}
