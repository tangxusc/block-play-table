package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/migrations"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

// TestA2AStoreCommandDispatchProjectionContract 验证两种 Store 对 A2A 控制面的成功路径保持一致。
func TestA2AStoreCommandDispatchProjectionContract(t *testing.T) {
	forEachA2AStore(t, func(t *testing.T, storage Store) {
		ctx := context.Background()
		now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
		task, worker := seedA2AStoreAggregates(t, ctx, storage, "contract", now)
		round, intent := newA2AStoreRoundAndIntent(t, task, worker, "contract", now)

		if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{
			Task: task, ExpectedTaskVersion: task.Version,
			Worker: worker, ExpectedWorkerVersion: worker.Version,
			Round: round, CreateRound: true, Intent: intent,
		}); err != nil {
			t.Fatalf("CommitA2ACommand 返回错误: %v", err)
		}

		rounds, err := storage.TaskA2ARounds(ctx, task.ID)
		if err != nil || len(rounds) != 1 || rounds[0].ID != round.ID {
			t.Fatalf("TaskA2ARounds = %+v, %v", rounds, err)
		}
		latest, err := storage.LatestTaskA2ARound(ctx, task.ID)
		if err != nil || latest.ID != round.ID {
			t.Fatalf("LatestTaskA2ARound = %+v, %v", latest, err)
		}
		due, err := storage.A2ADispatchIntentsDue(ctx, now, 1)
		if err != nil || len(due) != 1 || due[0].ID != intent.ID {
			t.Fatalf("A2ADispatchIntentsDue = %+v, %v", due, err)
		}

		loadedIntent, err := storage.A2ADispatchIntent(ctx, intent.ID)
		if err != nil {
			t.Fatal(err)
		}
		expectedIntentVersion := loadedIntent.Version
		if err := loadedIntent.StartAttempt(now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := storage.UpdateA2ADispatchIntent(ctx, loadedIntent, expectedIntentVersion); err != nil {
			t.Fatalf("UpdateA2ADispatchIntent 返回错误: %v", err)
		}

		loadedRound, err := storage.TaskA2ARound(ctx, round.ID)
		if err != nil {
			t.Fatal(err)
		}
		expectedRoundVersion := loadedRound.Version
		if err := loadedRound.BindRemote("remote-task-contract", "context-contract", domain.TaskA2ARemoteStatusSubmitted, now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		loadedIntent.Complete(now.Add(2 * time.Second))
		if err := storage.CommitA2ADispatchResult(ctx, loadedRound, expectedRoundVersion, loadedIntent, loadedIntent.Version-1); err != nil {
			t.Fatalf("CommitA2ADispatchResult 返回错误: %v", err)
		}

		reconcile, err := storage.A2ARoundsForReconcile(ctx)
		if err != nil || len(reconcile) != 1 || reconcile[0].A2ATaskID != "remote-task-contract" {
			t.Fatalf("A2ARoundsForReconcile = %+v, %v", reconcile, err)
		}
		projectedRound := reconcile[0]
		projectionExpectedVersion := projectedRound.Version
		if _, err := projectedRound.ApplyRemoteSnapshot(domain.TaskA2ARemoteStatusWorking, 1, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		projectedRound.ErrorCode = "AUTH_REQUIRED"
		projectedRound.ErrorMessage = "authentication is required"
		projectedRound.Retryable = true
		inbox := &domain.A2AEventInbox{
			RoundID: projectedRound.ID, ExecutionID: projectedRound.ExecutionID,
			EventID: "event-contract", PayloadHash: "hash-contract", Sequence: 1,
			EventType: "log.chunk", ProjectionStatus: domain.A2AProjectionProjected,
			ProjectedAt: now.Add(3 * time.Second), CreatedAt: now.Add(3 * time.Second),
		}
		disposition, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
			Inbox: inbox, Round: &projectedRound, ExpectedRoundVersion: projectionExpectedVersion,
			Logs:          []domain.TaskLog{{ID: "log-contract", TaskID: task.ID, Stream: "stdout", Content: "A2A log", CreatedAt: now}},
			Conversations: []domain.ConversationMessage{{ID: "message-contract", TaskID: task.ID, Role: "assistant", Content: "A2A result", CreatedAt: now}},
		})
		if err != nil || disposition != A2AEventApplied {
			t.Fatalf("CommitA2AProjection = %q, %v", disposition, err)
		}
		storedInbox, err := storage.A2AEventInbox(ctx, projectedRound.ID, inbox.EventID)
		if err != nil || storedInbox.PayloadHash != inbox.PayloadHash {
			t.Fatalf("A2AEventInbox = %+v, %v", storedInbox, err)
		}
		storedRound, err := storage.TaskA2ARound(ctx, projectedRound.ID)
		if err != nil || storedRound.ErrorCode != "AUTH_REQUIRED" || storedRound.ErrorMessage != "authentication is required" || !storedRound.Retryable {
			t.Fatalf("诊断 round 持久化 = %+v, %v", storedRound, err)
		}
		logs, err := storage.TaskLogs(ctx, task.ID)
		if err != nil || len(logs) != 1 || logs[0].Content != "A2A log" {
			t.Fatalf("TaskLogs = %+v, %v", logs, err)
		}
		messages, err := storage.TaskConversations(ctx, task.ID)
		if err != nil || len(messages) != 1 || messages[0].Content != "A2A result" {
			t.Fatalf("TaskConversations = %+v, %v", messages, err)
		}

		// 相同事件必须在 OCC 检查前幂等返回，避免断流重放重复业务投影。
		disposition, err = storage.CommitA2AProjection(ctx, A2AProjectionCommit{
			Inbox: inbox, Round: &projectedRound, ExpectedRoundVersion: -1,
		})
		if err != nil || disposition != A2AEventDuplicate {
			t.Fatalf("重复 CommitA2AProjection = %q, %v", disposition, err)
		}
	})
}

// TestA2AStoreRejectsConflictsAtomically 验证冲突不会部分写入 A2A 状态。
func TestA2AStoreRejectsConflictsAtomically(t *testing.T) {
	forEachA2AStore(t, func(t *testing.T, storage Store) {
		ctx := context.Background()
		now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
		task, worker := seedA2AStoreAggregates(t, ctx, storage, "conflict", now)
		round, intent := newA2AStoreRoundAndIntent(t, task, worker, "conflict", now)
		if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{
			Task: task, ExpectedTaskVersion: task.Version,
			Worker: worker, ExpectedWorkerVersion: worker.Version,
			Round: round, CreateRound: true, Intent: intent,
		}); err != nil {
			t.Fatal(err)
		}

		duplicateRound := *round
		duplicateRound.ID = "round-conflict-second"
		duplicateRound.CommandID = "command-conflict-second"
		duplicateIntent, err := domain.NewTaskA2ADispatchIntent(&duplicateRound, domain.TaskA2AOperationStart, duplicateRound.CommandID, []byte(`{}`), now)
		if err != nil {
			t.Fatal(err)
		}
		if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{
			Round: &duplicateRound, CreateRound: true, Intent: duplicateIntent,
		}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("重复 attempt/turn 错误 = %v", err)
		}
		if rounds, err := storage.TaskA2ARounds(ctx, task.ID); err != nil || len(rounds) != 1 {
			t.Fatalf("冲突后 TaskA2ARounds = %+v, %v", rounds, err)
		}

		loadedRound, err := storage.TaskA2ARound(ctx, round.ID)
		if err != nil {
			t.Fatal(err)
		}
		expectedRoundVersion := loadedRound.Version
		if err := loadedRound.BindRemote("remote-task-conflict", "context-conflict", domain.TaskA2ARemoteStatusWorking, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := storage.UpdateA2ARound(ctx, loadedRound, expectedRoundVersion+99); !errors.Is(err, ErrA2AConcurrentModification) {
			t.Fatalf("错误 round version = %v", err)
		}
		persistedRound, err := storage.TaskA2ARound(ctx, round.ID)
		if err != nil || persistedRound.A2ATaskID != "" {
			t.Fatalf("OCC 冲突后 round = %+v, %v", persistedRound, err)
		}

		if err := storage.UpdateA2ARound(ctx, loadedRound, expectedRoundVersion); err != nil {
			t.Fatal(err)
		}
		projectionVersion := loadedRound.Version
		if _, err := loadedRound.ApplyRemoteSnapshot(domain.TaskA2ARemoteStatusWorking, 1, now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		firstInbox := &domain.A2AEventInbox{
			RoundID: loadedRound.ID, ExecutionID: loadedRound.ExecutionID, EventID: "event-first",
			PayloadHash: "hash-first", Sequence: 1, EventType: "log.chunk",
			ProjectionStatus: domain.A2AProjectionProjected, ProjectedAt: now, CreatedAt: now,
		}
		if disposition, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
			Inbox: firstInbox, Round: loadedRound, ExpectedRoundVersion: projectionVersion,
		}); err != nil || disposition != A2AEventApplied {
			t.Fatalf("首次投影 = %q, %v", disposition, err)
		}

		conflictingHash := *firstInbox
		conflictingHash.PayloadHash = "hash-changed"
		if _, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
			Inbox: &conflictingHash, Round: loadedRound, ExpectedRoundVersion: loadedRound.Version,
		}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("相同事件不同内容错误 = %v", err)
		}
		sequenceCollision := *firstInbox
		sequenceCollision.EventID = "event-second"
		sequenceCollision.PayloadHash = "hash-second"
		if _, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
			Inbox: &sequenceCollision, Round: loadedRound, ExpectedRoundVersion: loadedRound.Version,
		}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("sequence 冲突错误 = %v", err)
		}

		continueRound, err := domain.NewTaskA2ARound(domain.NewTaskA2ARoundInput{
			ID: "round-conflict-continue", TaskID: task.ID, ExecutionID: loadedRound.ExecutionID,
			Attempt: loadedRound.Attempt, Turn: 2, Operation: domain.TaskA2AOperationContinue,
			WorkerID: worker.ID, CommandID: "command-conflict-continue", ParentRoundID: loadedRound.ID,
			ContextID: loadedRound.ContextID, LastSequence: loadedRound.LastSequence, Now: now.Add(3 * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		continueIntent, err := domain.NewTaskA2ADispatchIntent(continueRound, domain.TaskA2AOperationContinue, continueRound.CommandID, []byte(`{}`), now.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: continueRound, CreateRound: true, Intent: continueIntent}); err != nil {
			t.Fatalf("创建 continue round: %v", err)
		}
		if continueRound.ContextID != loadedRound.ContextID || continueRound.A2ATaskID != "" {
			t.Fatalf("continue 预绑定 = %+v", continueRound)
		}
		continueVersion := continueRound.Version
		if err := continueRound.BindRemote("remote-task-conflict-continue", loadedRound.ContextID, domain.TaskA2ARemoteStatusWorking, now.Add(4*time.Second)); err != nil {
			t.Fatalf("continue 首包绑定远端 Task: %v", err)
		}
		if err := storage.UpdateA2ARound(ctx, continueRound, continueVersion); err != nil {
			t.Fatal(err)
		}
		continueProjectionVersion := continueRound.Version
		if _, err := continueRound.ApplyRemoteSnapshot(domain.TaskA2ARemoteStatusWorking, 2, now.Add(5*time.Second)); err != nil {
			t.Fatal(err)
		}
		crossRoundSameEvent := &domain.A2AEventInbox{
			RoundID: continueRound.ID, ExecutionID: continueRound.ExecutionID, EventID: firstInbox.EventID,
			PayloadHash: "hash-turn-two", Sequence: 2, EventType: "result.updated",
			ProjectionStatus: domain.A2AProjectionProjected, ProjectedAt: now.Add(5 * time.Second), CreatedAt: now.Add(5 * time.Second),
		}
		if disposition, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
			Inbox: crossRoundSameEvent, Round: continueRound, ExpectedRoundVersion: continueProjectionVersion,
		}); err != nil || disposition != A2AEventApplied {
			t.Fatalf("跨 round 相同 eventId = %q, %v", disposition, err)
		}
		if stored, err := storage.A2AEventInbox(ctx, continueRound.ID, firstInbox.EventID); err != nil || stored.RoundID != continueRound.ID {
			t.Fatalf("continue inbox = %+v, %v", stored, err)
		}
		crossRoundSequenceCollision := *crossRoundSameEvent
		crossRoundSequenceCollision.EventID = "event-turn-two-sequence-conflict"
		crossRoundSequenceCollision.PayloadHash = "hash-turn-two-sequence-conflict"
		crossRoundSequenceCollision.Sequence = firstInbox.Sequence
		if _, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
			Inbox: &crossRoundSequenceCollision, Round: continueRound, ExpectedRoundVersion: continueRound.Version,
		}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("跨 round 同 execution sequence 冲突 = %v", err)
		}

		migrated, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{
			Task: task, ExpectedTaskVersion: task.Version,
			Worker: worker, ExpectedWorkerVersion: worker.Version,
		})
		if err != nil || migrated {
			t.Fatalf("已有 round 的迁移 = %v, %v", migrated, err)
		}

		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := storage.A2ADispatchIntentsDue(canceled, now, 10); !errors.Is(err, context.Canceled) {
			t.Fatalf("取消 context 错误 = %v", err)
		}
		if _, err := storage.A2AEventInbox(ctx, "missing", "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("缺失 inbox 错误 = %v", err)
		}
	})
}

// TestA2AStoreRejectsTaskInteractionOwnerChange 验证 interaction ID 不能通过 upsert 迁移到另一个 Task。
func TestA2AStoreRejectsTaskInteractionOwnerChange(t *testing.T) {
	forEachA2AStore(t, func(t *testing.T, storage Store) {
		ctx := context.Background()
		now := time.Date(2026, 8, 10, 19, 0, 0, 0, time.UTC)
		firstTask, _ := seedA2AStoreAggregates(t, ctx, storage, "interaction-owner-first", now)
		secondTask, _ := seedA2AStoreAggregates(t, ctx, storage, "interaction-owner-second", now)
		interaction := domain.TaskInteraction{
			ID: "interaction-shared", TaskID: firstTask.ID,
			Kind: domain.TaskInteractionUserInput, Status: domain.TaskInteractionPending,
			Title: "Original", CreatedAt: now, UpdatedAt: now,
		}
		if err := storage.SaveTaskInteraction(ctx, interaction); err != nil {
			t.Fatal(err)
		}
		changed := interaction
		changed.TaskID = secondTask.ID
		changed.Title = "Hijacked"
		changed.UpdatedAt = now.Add(time.Second)
		if err := storage.SaveTaskInteraction(ctx, changed); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("跨 Task 覆写 interaction 错误 = %v，期望冲突", err)
		}
		persisted, err := storage.TaskInteraction(ctx, interaction.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.TaskID != firstTask.ID || persisted.Title != interaction.Title {
			t.Fatalf("跨 Task 覆写改变了 interaction: before=%+v after=%+v", interaction, persisted)
		}
	})
}

// TestTaskProjectionRecordsRejectDuplicateIDs 验证普通写接口在两种 Store 中都拒绝全局业务 ID 冲突。
func TestTaskProjectionRecordsRejectDuplicateIDs(t *testing.T) {
	forEachA2AStore(t, func(t *testing.T, storage Store) {
		ctx := context.Background()
		now := time.Date(2026, 8, 10, 20, 30, 0, 0, time.UTC)
		firstTask, _ := seedA2AStoreAggregates(t, ctx, storage, "business-id-first", now)
		secondTask, _ := seedA2AStoreAggregates(t, ctx, storage, "business-id-second", now)

		firstLog := domain.TaskLog{ID: "shared-log-id", TaskID: firstTask.ID, Stream: "stdout", Content: "first", CreatedAt: now}
		if err := storage.AppendTaskLog(ctx, firstLog); err != nil {
			t.Fatal(err)
		}
		changedLog := firstLog
		changedLog.TaskID = secondTask.ID
		changedLog.Content = "must not persist"
		if err := storage.AppendTaskLog(ctx, changedLog); err == nil {
			t.Fatal("重复 TaskLog ID 未被拒绝")
		}
		if logs, err := storage.TaskLogs(ctx, firstTask.ID); err != nil || len(logs) != 1 || logs[0].Content != firstLog.Content {
			t.Fatalf("首次 TaskLog 被冲突写改变: %+v, %v", logs, err)
		}
		if logs, err := storage.TaskLogs(ctx, secondTask.ID); err != nil || len(logs) != 0 {
			t.Fatalf("冲突 TaskLog 被写入: %+v, %v", logs, err)
		}

		firstMessage := domain.ConversationMessage{ID: "shared-message-id", TaskID: firstTask.ID, Role: "assistant", Content: "first", CreatedAt: now}
		if err := storage.AppendConversation(ctx, firstMessage); err != nil {
			t.Fatal(err)
		}
		changedMessage := firstMessage
		changedMessage.TaskID = secondTask.ID
		changedMessage.Content = "must not persist"
		if err := storage.AppendConversation(ctx, changedMessage); err == nil {
			t.Fatal("重复 ConversationMessage ID 未被拒绝")
		}
		if messages, err := storage.TaskConversations(ctx, firstTask.ID); err != nil || len(messages) != 1 || messages[0].Content != firstMessage.Content {
			t.Fatalf("首次 ConversationMessage 被冲突写改变: %+v, %v", messages, err)
		}
		if messages, err := storage.TaskConversations(ctx, secondTask.ID); err != nil || len(messages) != 0 {
			t.Fatalf("冲突 ConversationMessage 被写入: %+v, %v", messages, err)
		}
	})
}

// TestA2AStoreRejectsDuplicateProjectionIDsAtomically 验证业务 ID 冲突不会部分推进 round、inbox 或其他业务投影。
func TestA2AStoreRejectsDuplicateProjectionIDsAtomically(t *testing.T) {
	for _, conflictType := range []string{"log", "conversation"} {
		t.Run(conflictType, func(t *testing.T) {
			forEachA2AStore(t, func(t *testing.T, storage Store) {
				ctx := context.Background()
				now := time.Date(2026, 8, 10, 21, 0, 0, 0, time.UTC)
				task, worker := seedA2AStoreAggregates(t, ctx, storage, "atomic-business-id-"+conflictType, now)
				round, intent := newA2AStoreRoundAndIntent(t, task, worker, "atomic-business-id-"+conflictType, now)
				if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{
					Task: task, ExpectedTaskVersion: task.Version, Worker: worker, ExpectedWorkerVersion: worker.Version,
					Round: round, CreateRound: true, Intent: intent,
				}); err != nil {
					t.Fatal(err)
				}
				expectedRoundVersion := round.Version
				if err := round.BindRemote("remote-"+conflictType, "context-"+conflictType, domain.TaskA2ARemoteStatusWorking, now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if err := storage.UpdateA2ARound(ctx, round, expectedRoundVersion); err != nil {
					t.Fatal(err)
				}

				baselineLog := domain.TaskLog{ID: "projection-log-" + conflictType, TaskID: task.ID, Stream: "stdout", Content: "baseline", CreatedAt: now}
				baselineMessage := domain.ConversationMessage{ID: "projection-message-" + conflictType, TaskID: task.ID, Role: "assistant", Content: "baseline", CreatedAt: now}
				if err := storage.AppendTaskLog(ctx, baselineLog); err != nil {
					t.Fatal(err)
				}
				if err := storage.AppendConversation(ctx, baselineMessage); err != nil {
					t.Fatal(err)
				}

				projectedRound := *round
				projectionVersion := projectedRound.Version
				if _, err := projectedRound.ApplyRemoteSnapshot(domain.TaskA2ARemoteStatusWorking, 1, now.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
				inbox := &domain.A2AEventInbox{
					RoundID: projectedRound.ID, ExecutionID: projectedRound.ExecutionID,
					EventID: "projection-event-" + conflictType, PayloadHash: "projection-hash-" + conflictType,
					Sequence: 1, EventType: "log.chunk", ProjectionStatus: domain.A2AProjectionProjected,
					ProjectedAt: now.Add(2 * time.Second), CreatedAt: now.Add(2 * time.Second),
				}
				logs := []domain.TaskLog{{ID: "new-log-" + conflictType, TaskID: task.ID, Stream: "stderr", Content: "must roll back", CreatedAt: now}}
				messages := []domain.ConversationMessage{{ID: "new-message-" + conflictType, TaskID: task.ID, Role: "assistant", Content: "must roll back", CreatedAt: now}}
				if conflictType == "log" {
					logs[0].ID = baselineLog.ID
				} else {
					messages[0].ID = baselineMessage.ID
				}
				if _, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
					Inbox: inbox, Round: &projectedRound, ExpectedRoundVersion: projectionVersion,
					Logs: logs, Conversations: messages,
				}); err == nil {
					t.Fatal("重复业务投影 ID 未被拒绝")
				}
				persistedRound, err := storage.TaskA2ARound(ctx, round.ID)
				if err != nil || persistedRound.Version != projectionVersion || persistedRound.LastSequence != 0 {
					t.Fatalf("冲突后 round 发生变化: %+v, %v", persistedRound, err)
				}
				if _, err := storage.A2AEventInbox(ctx, round.ID, inbox.EventID); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("冲突后 inbox 已写入: %v", err)
				}
				if storedLogs, err := storage.TaskLogs(ctx, task.ID); err != nil || len(storedLogs) != 1 || storedLogs[0].ID != baselineLog.ID {
					t.Fatalf("冲突后日志发生部分写入: %+v, %v", storedLogs, err)
				}
				if storedMessages, err := storage.TaskConversations(ctx, task.ID); err != nil || len(storedMessages) != 1 || storedMessages[0].ID != baselineMessage.ID {
					t.Fatalf("冲突后会话发生部分写入: %+v, %v", storedMessages, err)
				}
			})
		})
	}
}

// TestA2AStoreAcceptsRoundScopedIDsForReusedEvent 验证不同 execution 复用 eventId 时可原子保存各自业务投影。
func TestA2AStoreAcceptsRoundScopedIDsForReusedEvent(t *testing.T) {
	forEachA2AStore(t, func(t *testing.T, storage Store) {
		ctx := context.Background()
		now := time.Date(2026, 8, 10, 21, 15, 0, 0, time.UTC)
		sharedEventID := "018f0000-0000-7000-8000-000000000099"
		for index, suffix := range []string{"reused-event-first", "reused-event-second"} {
			task, worker := seedA2AStoreAggregates(t, ctx, storage, suffix, now)
			round, intent := newA2AStoreRoundAndIntent(t, task, worker, suffix, now)
			if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{
				Task: task, ExpectedTaskVersion: task.Version, Worker: worker, ExpectedWorkerVersion: worker.Version,
				Round: round, CreateRound: true, Intent: intent,
			}); err != nil {
				t.Fatal(err)
			}
			expectedRoundVersion := round.Version
			if err := round.BindRemote("remote-"+suffix, "context-"+suffix, domain.TaskA2ARemoteStatusWorking, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := storage.UpdateA2ARound(ctx, round, expectedRoundVersion); err != nil {
				t.Fatal(err)
			}
			projectionVersion := round.Version
			if _, err := round.ApplyRemoteSnapshot(domain.TaskA2ARemoteStatusWorking, 1, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			inbox := &domain.A2AEventInbox{
				RoundID: round.ID, ExecutionID: round.ExecutionID, EventID: sharedEventID,
				PayloadHash: "hash-" + suffix, Sequence: 1, EventType: "conversation.message",
				ProjectionStatus: domain.A2AProjectionProjected, ProjectedAt: now.Add(2 * time.Second), CreatedAt: now.Add(2 * time.Second),
			}
			logID := "log_" + round.ID + "_" + sharedEventID
			messageID := "msg_" + round.ID + "_" + sharedEventID
			disposition, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
				Inbox: inbox, Round: round, ExpectedRoundVersion: projectionVersion,
				Logs:          []domain.TaskLog{{ID: logID, TaskID: task.ID, Stream: "stdout", Content: suffix, CreatedAt: now}},
				Conversations: []domain.ConversationMessage{{ID: messageID, TaskID: task.ID, Role: "assistant", Content: suffix, CreatedAt: now}},
			})
			if err != nil || disposition != A2AEventApplied {
				t.Fatalf("第 %d 个复用 eventId 投影 = %q, %v", index+1, disposition, err)
			}
			if logs, err := storage.TaskLogs(ctx, task.ID); err != nil || len(logs) != 1 || logs[0].ID != logID {
				t.Fatalf("round-scoped 日志 = %+v, %v", logs, err)
			}
			if messages, err := storage.TaskConversations(ctx, task.ID); err != nil || len(messages) != 1 || messages[0].ID != messageID {
				t.Fatalf("round-scoped 会话 = %+v, %v", messages, err)
			}
			if storedInbox, err := storage.A2AEventInbox(ctx, round.ID, sharedEventID); err != nil || storedInbox.ExecutionID != round.ExecutionID {
				t.Fatalf("round-scoped inbox = %+v, %v", storedInbox, err)
			}
		}
	})
}

// TestA2ADispatchIntentsDueUsesStableIDTieBreak 验证完全同时间的 intent 与 SQL 一样按 ID 稳定排序。
func TestA2ADispatchIntentsDueUsesStableIDTieBreak(t *testing.T) {
	forEachA2AStore(t, func(t *testing.T, storage Store) {
		ctx := context.Background()
		now := time.Date(2026, 8, 10, 21, 30, 0, 0, time.UTC)
		suffixes := []string{"zulu", "alpha", "mike", "bravo", "yankee", "charlie", "xray", "delta"}
		expectedIDs := make([]string, 0, len(suffixes))
		for _, suffix := range suffixes {
			task, worker := seedA2AStoreAggregates(t, ctx, storage, "intent-order-"+suffix, now)
			round, intent := newA2AStoreRoundAndIntent(t, task, worker, "intent-order-"+suffix, now)
			if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{
				Task: task, ExpectedTaskVersion: task.Version, Worker: worker, ExpectedWorkerVersion: worker.Version,
				Round: round, CreateRound: true, Intent: intent,
			}); err != nil {
				t.Fatal(err)
			}
			expectedIDs = append(expectedIDs, intent.ID)
		}
		slices.Sort(expectedIDs)
		intents, err := storage.A2ADispatchIntentsDue(ctx, now, len(expectedIDs))
		if err != nil {
			t.Fatal(err)
		}
		actualIDs := make([]string, 0, len(intents))
		for _, intent := range intents {
			actualIDs = append(actualIDs, intent.ID)
		}
		if !slices.Equal(actualIDs, expectedIDs) {
			t.Fatalf("intent 排序 = %v，期望 %v", actualIDs, expectedIDs)
		}
	})
}

func forEachA2AStore(t *testing.T, run func(*testing.T, Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		run(t, NewMemoryStore())
	})
	t.Run("sqlite", func(t *testing.T) {
		ctx := context.Background()
		storage, err := OpenSQLStore(ctx, SQLDriverSQLite, filepath.Join(t.TempDir(), "manager.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = storage.Close() })
		items := make([]Migration, 0, len(migrations.All))
		for _, item := range migrations.All {
			items = append(items, Migration{Version: item.Version, SQL: item.SQL})
		}
		if err := storage.MigrateVersioned(ctx, items); err != nil {
			t.Fatal(err)
		}
		run(t, storage)
	})
}

func seedA2AStoreAggregates(t *testing.T, ctx context.Context, storage Store, suffix string, now time.Time) (*domain.Task, *domain.Worker) {
	t.Helper()
	project, err := domain.NewProject(domain.NewProjectInput{
		ID: "project-" + suffix, Name: "A2A Project " + suffix, GitURL: "file:///tmp/a2a-" + suffix,
		DefaultBranch: "main", WorktreeNamePrefix: "a2a-" + suffix, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := domain.NewWorker(domain.NewWorkerInput{
		ID: "worker-" + suffix, Name: "A2A Worker " + suffix,
		SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/a2a-worker-" + suffix,
		ProjectBindingMode: domain.WorkerAllProjects, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.Connect(now)
	task, err := domain.NewTask(domain.NewTaskInput{
		ID: "task-" + suffix, Title: "A2A Task " + suffix, ProjectID: project.ID,
		AgentType: domain.AgentCodex, BaseBranch: "main", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := task.AssignWorker(worker.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := worker.AssignTask(task.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	return task, worker
}

func newA2AStoreRoundAndIntent(t *testing.T, task *domain.Task, worker *domain.Worker, suffix string, now time.Time) (*domain.TaskA2ARound, *domain.TaskA2ADispatchIntent) {
	t.Helper()
	round, err := domain.NewTaskA2ARound(domain.NewTaskA2ARoundInput{
		ID: "round-" + suffix, TaskID: task.ID, ExecutionID: "execution-" + suffix,
		Attempt: 1, Turn: 1, Operation: domain.TaskA2AOperationStart,
		WorkerID: worker.ID, CommandID: "command-" + suffix, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.NewTaskA2ADispatchIntent(round, domain.TaskA2AOperationStart, round.CommandID, []byte(`{}`), now)
	if err != nil {
		t.Fatal(err)
	}
	return round, intent
}
