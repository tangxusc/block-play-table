package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

func applyHTTPAPITestA2AEvent(t *testing.T, ctx context.Context, service *app.Service, taskID string, eventType a2aext.EventType, status domain.TaskA2ARemoteStatus, runtime *a2aext.RuntimeInfo, payload map[string]any) *domain.Task {
	t.Helper()
	round, err := service.Store().LatestTaskA2ARound(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	event := &a2aext.ExecutionEvent{
		Kind:    a2aext.EventKind,
		Version: a2aext.Version,
		Event: a2aext.EventHeader{
			ID:         uuid.Must(uuid.NewV7()).String(),
			Sequence:   round.LastSequence + 1,
			Type:       eventType,
			OccurredAt: time.Now().UTC(),
		},
		Scope: a2aext.EventScope{
			LocalTaskID: taskID,
			ExecutionID: round.ExecutionID,
			Attempt:     round.Attempt,
			Turn:        round.Turn,
			WorkerID:    round.WorkerID,
		},
		Runtime: runtime,
		Payload: payload,
	}
	update := app.A2ARemoteUpdate{
		TaskID:    round.A2ATaskID,
		ContextID: round.ContextID,
		Status:    status,
		Sequence:  event.Event.Sequence,
		Event:     event,
	}
	if round.A2ATaskID == "" || round.ContextID == "" {
		intentID := httpAPITestA2AIntentID(t, ctx, service, round.ID)
		update.TaskID = "a2a-" + round.ID
		update.ContextID = "context-" + round.ExecutionID
		err = service.ApplyA2ADispatchUpdate(ctx, intentID, update)
	} else {
		err = service.ApplyA2ARemoteUpdate(ctx, round.ID, update)
	}
	if err != nil {
		t.Fatalf("投影测试 A2A 事件 %s: %v", eventType, err)
	}
	task, err := service.Store().Task(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func httpAPITestA2AIntentID(t *testing.T, ctx context.Context, service *app.Service, roundID string) string {
	t.Helper()
	intents, err := service.Store().A2ADispatchIntentsDue(ctx, time.Now().Add(100*365*24*time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, intent := range intents {
		if intent.RoundID == roundID {
			return intent.ID
		}
	}
	t.Fatalf("round %s 缺少待发送 A2A intent", roundID)
	return ""
}
