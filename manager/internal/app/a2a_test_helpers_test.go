package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

func preparedA2ARequestForTask(t *testing.T, ctx context.Context, service *Service, taskID string) *A2ADispatchRequest {
	t.Helper()
	intents, err := service.Store().A2ADispatchIntentsDue(ctx, time.Now().Add(100*365*24*time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(intents) - 1; index >= 0; index-- {
		if intents[index].TaskID != taskID {
			continue
		}
		request, err := service.PrepareA2ADispatchRequest(ctx, intents[index])
		if err != nil {
			t.Fatal(err)
		}
		return request
	}
	t.Fatalf("task %s has no pending A2A dispatch intent", taskID)
	return nil
}

func bindTestA2ARound(t *testing.T, ctx context.Context, service *Service, taskID string) *domain.TaskA2ARound {
	t.Helper()
	round, err := service.Store().LatestTaskA2ARound(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	expected := round.Version
	if err := round.BindRemote("a2a-"+round.ID, "context-"+round.ExecutionID, domain.TaskA2ARemoteStatusSubmitted, service.clock()); err != nil {
		t.Fatal(err)
	}
	if err := service.Store().UpdateA2ARound(ctx, round, expected); err != nil {
		t.Fatal(err)
	}
	return round
}

func applyTestA2AEvent(t *testing.T, ctx context.Context, service *Service, taskID string, eventType a2aext.EventType, status domain.TaskA2ARemoteStatus, runtime *a2aext.RuntimeInfo, payload map[string]any) *domain.Task {
	t.Helper()
	round, event := prepareTestA2AEvent(t, ctx, service, taskID, eventType, runtime, payload)
	if err := applyTestA2ARawEvent(ctx, service, round, event, status); err != nil {
		t.Fatal(err)
	}
	return loadTestA2ATask(t, ctx, service, taskID)
}

func prepareTestA2AEvent(t *testing.T, ctx context.Context, service *Service, taskID string, eventType a2aext.EventType, runtime *a2aext.RuntimeInfo, payload map[string]any) (*domain.TaskA2ARound, *a2aext.ExecutionEvent) {
	t.Helper()
	round, err := service.Store().LatestTaskA2ARound(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	event := newTestA2AEvent(t, service, round, uuid.Must(uuid.NewV7()).String(), round.LastSequence+1, eventType, runtime, payload)
	return round, event
}

func applyTestA2AStatus(t *testing.T, ctx context.Context, service *Service, taskID string, status domain.TaskA2ARemoteStatus) *domain.Task {
	t.Helper()
	round, err := service.Store().LatestTaskA2ARound(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyA2ARemoteUpdate(ctx, round.ID, A2ARemoteUpdate{TaskID: round.A2ATaskID, ContextID: round.ContextID, Status: status}); err != nil {
		t.Fatal(err)
	}
	return loadTestA2ATask(t, ctx, service, taskID)
}

func newTestA2AEvent(t *testing.T, service *Service, round *domain.TaskA2ARound, eventID string, sequence int64, eventType a2aext.EventType, runtime *a2aext.RuntimeInfo, payload map[string]any) *a2aext.ExecutionEvent {
	t.Helper()
	event := &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event:   a2aext.EventHeader{ID: eventID, Sequence: sequence, Type: eventType, OccurredAt: service.clock()},
		Scope:   a2aext.EventScope{LocalTaskID: round.TaskID, ExecutionID: round.ExecutionID, Attempt: round.Attempt, Turn: round.Turn, WorkerID: round.WorkerID},
		Runtime: runtime, Payload: payload,
	}
	if err := a2aext.ValidateEvent(event); err != nil {
		t.Fatalf("测试 A2A 事件非法: %v", err)
	}
	return event
}

func applyTestA2ARawEvent(ctx context.Context, service *Service, round *domain.TaskA2ARound, event *a2aext.ExecutionEvent, status domain.TaskA2ARemoteStatus) error {
	return service.ApplyA2ARemoteUpdate(ctx, round.ID, A2ARemoteUpdate{
		TaskID: round.A2ATaskID, ContextID: round.ContextID, Status: status,
		Sequence: event.Event.Sequence, Event: event,
	})
}

func loadTestA2ATask(t *testing.T, ctx context.Context, service *Service, taskID string) *domain.Task {
	t.Helper()
	task, err := service.Store().Task(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}
