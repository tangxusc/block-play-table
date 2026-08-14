package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

// TestA2AStoreDuplicateInboxWithIntentReturnsOCCWithoutPartialWrite 验证竞态提交失败时不会产生部分写入。
func TestA2AStoreDuplicateInboxWithIntentReturnsOCCWithoutPartialWrite(t *testing.T) {
	forEachA2AStore(t, func(t *testing.T, storage Store) {
		for _, operation := range []domain.TaskA2AOperation{
			domain.TaskA2AOperationCancel,
			domain.TaskA2AOperationInteractionResponse,
		} {
			t.Run(string(operation), func(t *testing.T) {
				ctx := context.Background()
				now := time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC)
				suffix := "duplicate-" + string(operation)
				task, worker := seedA2AStoreAggregates(t, ctx, storage, suffix, now)
				round, initialIntent := newA2AStoreRoundAndIntent(t, task, worker, suffix, now)
				if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{
					Task: task, ExpectedTaskVersion: task.Version,
					Worker: worker, ExpectedWorkerVersion: worker.Version,
					Round: round, CreateRound: true, Intent: initialIntent,
				}); err != nil {
					t.Fatal(err)
				}
				expectedRoundVersion := round.Version
				if err := round.BindRemote("remote-"+suffix, "context-"+suffix, domain.TaskA2ARemoteStatusWorking, now); err != nil {
					t.Fatal(err)
				}
				initialIntent.Complete(now)
				if err := storage.CommitA2ADispatchResult(ctx, round, expectedRoundVersion, initialIntent, initialIntent.Version-1); err != nil {
					t.Fatal(err)
				}

				controlIntent, err := domain.NewTaskA2ADispatchIntent(round, operation, "command-control-"+suffix, []byte(`{}`), now)
				if err != nil {
					t.Fatal(err)
				}
				if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: round, CreateRound: false, Intent: controlIntent}); err != nil {
					t.Fatal(err)
				}
				expectedIntentVersion := controlIntent.Version
				if err := controlIntent.StartAttempt(now); err != nil {
					t.Fatal(err)
				}
				if err := storage.UpdateA2ADispatchIntent(ctx, controlIntent, expectedIntentVersion); err != nil {
					t.Fatal(err)
				}

				projectedRound, err := storage.TaskA2ARound(ctx, round.ID)
				if err != nil {
					t.Fatal(err)
				}
				projectionVersion := projectedRound.Version
				if _, err := projectedRound.ApplyRemoteSnapshot(domain.TaskA2ARemoteStatusWorking, 1, now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				inbox := &domain.A2AEventInbox{
					RoundID: projectedRound.ID, ExecutionID: projectedRound.ExecutionID,
					EventID: "event-" + suffix, PayloadHash: "hash-" + suffix, Sequence: 1,
					EventType: "log.chunk", ProjectionStatus: domain.A2AProjectionProjected,
					ProjectedAt: now.Add(time.Second), CreatedAt: now.Add(time.Second),
				}
				if disposition, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
					Inbox: inbox, Round: projectedRound, ExpectedRoundVersion: projectionVersion,
					Logs: []domain.TaskLog{{ID: "log-" + suffix, TaskID: task.ID, Stream: "stdout", Content: "single line", CreatedAt: now}},
				}); err != nil || disposition != A2AEventApplied {
					t.Fatalf("首次投影 = %q, %v", disposition, err)
				}

				controlIntent, err = storage.A2ADispatchIntent(ctx, controlIntent.ID)
				if err != nil {
					t.Fatal(err)
				}
				expectedIntentVersion = controlIntent.Version
				controlIntent.Complete(now.Add(2 * time.Second))
				disposition, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
					Inbox: inbox, Round: projectedRound, ExpectedRoundVersion: -1,
					Intent: controlIntent, ExpectedIntentVersion: expectedIntentVersion,
					Logs: []domain.TaskLog{{ID: "log-duplicate-" + suffix, TaskID: task.ID, Stream: "stdout", Content: "must not persist", CreatedAt: now}},
				})
				if disposition != "" || !errors.Is(err, ErrA2AConcurrentModification) {
					t.Fatalf("携带 intent 的重复投影 = %q, %v，期望 OCC", disposition, err)
				}
				persistedIntent, err := storage.A2ADispatchIntent(ctx, controlIntent.ID)
				if err != nil || persistedIntent.Status != domain.A2ADispatchSending || persistedIntent.CompletedAt != nil {
					t.Fatalf("竞态失败后的 intent = %+v, %v", persistedIntent, err)
				}
				logs, err := storage.TaskLogs(ctx, task.ID)
				if err != nil || len(logs) != 1 || logs[0].Content != "single line" {
					t.Fatalf("竞态失败后的日志 = %+v, %v", logs, err)
				}
			})
		}
	})
}
