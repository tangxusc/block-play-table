package app

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestFailA2ARoundWithIntentConvergesTaskWorkerAndDispatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 21, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedRunningTask(t, ctx, service)
	round, err := service.Store().LatestTaskA2ARound(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	intents, err := service.Store().A2ADispatchIntentsDue(ctx, now.Add(time.Hour), 10)
	if err != nil || len(intents) != 1 {
		t.Fatalf("dispatch intents=%+v err=%v", intents, err)
	}
	if err := service.failA2ARoundWithIntent(ctx, round.ID, intents[0].ID, "TEST_FAILURE", "safe detail"); err != nil {
		t.Fatal(err)
	}
	persistedRound, err := service.Store().TaskA2ARound(ctx, round.ID)
	if err != nil || persistedRound.RemoteStatus != domain.TaskA2ARemoteStatusFailed || persistedRound.ErrorCode != "TEST_FAILURE" || persistedRound.CompletedAt == nil {
		t.Fatalf("round=%+v err=%v", persistedRound, err)
	}
	persistedTask, err := service.Task(ctx, task.ID)
	if err != nil || persistedTask.Status != domain.TaskFailed {
		t.Fatalf("task=%+v err=%v", persistedTask, err)
	}
	worker, err := service.Worker(ctx, task.WorkerID)
	if err != nil || len(worker.CurrentTaskIDs) != 0 {
		t.Fatalf("worker=%+v err=%v", worker, err)
	}
	intent, err := service.Store().A2ADispatchIntent(ctx, intents[0].ID)
	if err != nil || intent.Status != domain.A2ADispatchFailed || intent.ErrorCode != "TEST_FAILURE" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	version := persistedRound.Version
	if err := service.failA2ARoundWithIntent(ctx, round.ID, intents[0].ID, "TEST_FAILURE", "safe detail"); err != nil {
		t.Fatal(err)
	}
	idempotent, err := service.Store().TaskA2ARound(ctx, round.ID)
	if err != nil || idempotent.Version != version {
		t.Fatalf("重复失败推进了 round: before=%d after=%+v err=%v", version, idempotent, err)
	}
}
