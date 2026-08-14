package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestMemoryA2AValidationConflictMatrix(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	storage := NewMemoryStore()
	task := &domain.Task{ID: "task", WorkerID: "worker", Version: 2, UpdatedAt: now}
	worker := &domain.Worker{ID: "worker", Version: 3, UpdatedAt: now}
	storage.tasks[task.ID] = cloneTask(task)
	storage.workers[worker.ID] = cloneWorker(worker)
	round := memoryBranchRound("round", task.ID, "execution", worker.ID, "command", 1, 1, now)
	intent := memoryBranchIntent("intent", round, "command", now)

	invalidCommits := []A2ACommandCommit{
		{},
		{Round: round},
		{Round: round, Intent: memoryBranchIntent("wrong-round", &domain.TaskA2ARound{ID: "other", TaskID: task.ID, ExecutionID: round.ExecutionID, WorkerID: worker.ID, CommandID: round.CommandID}, round.CommandID, now), CreateRound: true},
	}
	for index, commit := range invalidCommits {
		if err := storage.CommitA2ACommand(ctx, commit); err == nil {
			t.Fatalf("非法 command commit %d 未失败", index)
		}
	}
	if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: round, Intent: intent, CreateRound: true}); err != nil {
		t.Fatal(err)
	}

	duplicateRoundIntent := memoryBranchIntent("intent-duplicate-round", round, "command-2", now)
	duplicateRoundIntent.Operation = domain.TaskA2AOperationRetry
	if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: round, Intent: duplicateRoundIntent, CreateRound: true}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("重复 round 错误=%v", err)
	}
	sameTurn := memoryBranchRound("round-same-turn", task.ID, "execution-2", worker.ID, "command-2", 1, 1, now.Add(time.Second))
	if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: sameTurn, Intent: memoryBranchIntent("intent-same-turn", sameTurn, sameTurn.CommandID, now), CreateRound: true}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("重复 attempt/turn 错误=%v", err)
	}
	missingRound := memoryBranchRound("round-missing", task.ID, "execution", worker.ID, "continue", 1, 2, now)
	missingRound.Operation = domain.TaskA2AOperationContinue
	continueIntent := memoryBranchIntent("intent-continue", missingRound, missingRound.CommandID, now)
	continueIntent.Operation = domain.TaskA2AOperationContinue
	if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: missingRound, Intent: continueIntent}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("缺失活动 round 错误=%v", err)
	}
	duplicateIntent := memoryBranchIntent(intent.ID, round, "fresh-command", now)
	duplicateIntent.Operation = domain.TaskA2AOperationCancel
	if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: round, Intent: duplicateIntent}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("重复 intent 错误=%v", err)
	}
	duplicateCommand := memoryBranchIntent("intent-duplicate-command", round, intent.CommandID, now)
	duplicateCommand.Operation = domain.TaskA2AOperationCancel
	if err := storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: round, Intent: duplicateCommand}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("重复 command 错误=%v", err)
	}

	if err := storage.UpdateA2ARound(ctx, nil, 1); err == nil {
		t.Fatal("nil round 更新未失败")
	}
	if err := storage.UpdateA2ARound(ctx, &domain.TaskA2ARound{ID: "missing"}, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失 round 更新错误=%v", err)
	}
	if err := storage.UpdateA2ARound(ctx, round, 99); !errors.Is(err, ErrA2AConcurrentModification) {
		t.Fatalf("round OCC 错误=%v", err)
	}
	if err := storage.UpdateA2ADispatchIntent(ctx, nil, 1); err == nil {
		t.Fatal("nil intent 更新未失败")
	}
	if err := storage.UpdateA2ADispatchIntent(ctx, &domain.TaskA2ADispatchIntent{ID: "missing"}, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失 intent 更新错误=%v", err)
	}
	if err := storage.UpdateA2ADispatchIntent(ctx, intent, 99); !errors.Is(err, ErrA2AConcurrentModification) {
		t.Fatalf("intent OCC 错误=%v", err)
	}
	if err := storage.CommitA2ADispatchResult(ctx, nil, 0, intent, 1); err == nil {
		t.Fatal("nil dispatch round 未失败")
	}
}

func TestMemoryA2AOCCProjectionAndMigrationBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 19, 0, 0, 0, time.UTC)
	storage := NewMemoryStore()
	task := &domain.Task{ID: "task", WorkerID: "worker", Version: 1, UpdatedAt: now}
	worker := &domain.Worker{ID: "worker", Version: 1, UpdatedAt: now}
	round := memoryBranchRound("round", task.ID, "execution", worker.ID, "command", 1, 1, now)
	intent := memoryBranchIntent("intent", round, round.CommandID, now)
	storage.tasks[task.ID] = cloneTask(task)
	storage.workers[worker.ID] = cloneWorker(worker)
	storage.a2aRounds[round.ID] = cloneA2ARound(*round)
	storage.a2aIntents[intent.ID] = cloneA2AIntent(*intent)

	checks := []func() error{
		func() error {
			return storage.validateA2AOCCLocked(&domain.Task{ID: "missing"}, 1, nil, 0, nil, 0, nil, 0)
		},
		func() error { return storage.validateA2AOCCLocked(task, 99, nil, 0, nil, 0, nil, 0) },
		func() error {
			return storage.validateA2AOCCLocked(nil, 0, &domain.Worker{ID: "missing"}, 1, nil, 0, nil, 0)
		},
		func() error { return storage.validateA2AOCCLocked(nil, 0, worker, 99, nil, 0, nil, 0) },
		func() error {
			return storage.validateA2AOCCLocked(nil, 0, nil, 0, &domain.TaskA2ARound{ID: "missing"}, 1, nil, 0)
		},
		func() error { return storage.validateA2AOCCLocked(nil, 0, nil, 0, round, 99, nil, 0) },
		func() error {
			return storage.validateA2AOCCLocked(nil, 0, nil, 0, nil, 0, &domain.TaskA2ADispatchIntent{ID: "missing"}, 1)
		},
		func() error { return storage.validateA2AOCCLocked(nil, 0, nil, 0, nil, 0, intent, 99) },
	}
	for index, check := range checks {
		if err := check(); err == nil {
			t.Fatalf("OCC 冲突 %d 未失败", index)
		}
	}

	remote := *round
	remote.A2ATaskID = "remote-task"
	storage.a2aRounds[round.ID] = remote
	other := memoryBranchRound("other", "other-task", "other-execution", worker.ID, "other-command", 2, 1, now)
	other.A2ATaskID = remote.A2ATaskID
	storage.a2aRounds[other.ID] = *other
	if err := storage.validateA2AOCCLocked(nil, 0, nil, 0, other, other.Version, nil, 0); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("远端 Task 复用错误=%v", err)
	}

	disposition, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{})
	if err != nil || disposition != A2AEventApplied {
		t.Fatalf("空投影=%q, %v", disposition, err)
	}
	inbox := &domain.A2AEventInbox{RoundID: round.ID, ExecutionID: "wrong", EventID: "event"}
	if _, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{Inbox: inbox, Round: round}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("inbox 关联错误=%v", err)
	}
	if _, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{}); err == nil {
		t.Fatal("nil migration task 未失败")
	}
	wrongWorker := *worker
	wrongWorker.ID = "wrong"
	if _, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{Task: task, Worker: &wrongWorker}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("migration worker 关联错误=%v", err)
	}
	if applied, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{Task: task}); err != nil || applied {
		t.Fatalf("已有 round migration=%v, %v", applied, err)
	}
}

func TestMemoryA2ACommonProjectionAndStableSortBranches(t *testing.T) {
	now := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	storage := NewMemoryStore()
	task := &domain.Task{ID: "task", UpdatedAt: now}
	worker := &domain.Worker{ID: "worker"}
	interaction := &domain.TaskInteraction{ID: "interaction", TaskID: task.ID, Status: domain.TaskInteractionPending}
	storage.interactions["cancel"] = domain.TaskInteraction{ID: "cancel", TaskID: task.ID, Status: domain.TaskInteractionPending}
	event := domain.DomainEvent{EventID: "event", OccurredAt: now}
	storage.applyA2ACommonLocked(task, worker, interaction, nil, nil, []domain.DomainEvent{event, event}, true)
	if storage.interactions["cancel"].Status != domain.TaskInteractionCanceled || len(storage.events) != 1 || len(storage.outbox) != 1 {
		t.Fatalf("通用投影未执行取消或事件去重: interactions=%+v events=%+v", storage.interactions, storage.events)
	}
	storage.applyA2ACommonLocked(nil, nil, nil, nil, nil, nil, false)

	rounds := []domain.TaskA2ARound{
		*memoryBranchRound("d", "task", "e4", "worker", "c4", 2, 1, now),
		*memoryBranchRound("c", "task", "e3", "worker", "c3", 1, 2, now),
		*memoryBranchRound("b", "task", "e2", "worker", "c2", 1, 1, now.Add(time.Second)),
		*memoryBranchRound("a", "task", "e1", "worker", "c1", 1, 1, now),
	}
	sortA2ARounds(rounds)
	if rounds[0].ID != "a" || rounds[1].ID != "b" || rounds[2].ID != "c" || rounds[3].ID != "d" {
		t.Fatalf("round 排序错误: %+v", rounds)
	}
}

func TestMemoryA2ACommitOwnerMigrationInboxAndOCCBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)

	ownerStore := NewMemoryStore()
	ownerStore.interactions["interaction"] = domain.TaskInteraction{ID: "interaction", TaskID: "original-task"}
	round := memoryBranchRound("owner-round", "new-task", "owner-execution", "worker", "owner-command", 1, 1, now)
	intent := memoryBranchIntent("owner-intent", round, round.CommandID, now)
	conflictingInteraction := &domain.TaskInteraction{ID: "interaction", TaskID: "new-task"}
	if err := ownerStore.CommitA2ACommand(ctx, A2ACommandCommit{
		Round: round, Intent: intent, CreateRound: true, Interaction: conflictingInteraction,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("command interaction 所有权错误=%v", err)
	}
	if _, err := ownerStore.CommitA2AProjection(ctx, A2AProjectionCommit{Interaction: conflictingInteraction}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("projection interaction 所有权错误=%v", err)
	}

	projectionStore := NewMemoryStore()
	projectionStore.a2aIntents[intent.ID] = cloneA2AIntent(*intent)
	if disposition, err := projectionStore.CommitA2AProjection(ctx, A2AProjectionCommit{
		Intent: intent, ExpectedIntentVersion: intent.Version,
	}); err != nil || disposition != A2AEventApplied {
		t.Fatalf("仅 intent 投影=%q, %v", disposition, err)
	}
	if _, err := projectionStore.A2AEventInbox(ctx, "missing-round", "missing-event"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失 inbox 错误=%v", err)
	}

	migrationStore := NewMemoryStore()
	migrationStore.a2aRounds["foreign"] = *memoryBranchRound("foreign", "other-task", "foreign-execution", "worker", "foreign-command", 1, 1, now)
	if applied, err := migrationStore.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{
		Task: &domain.Task{ID: "migrate-task", Version: 1, UpdatedAt: now},
	}); err != nil || !applied {
		t.Fatalf("忽略其他任务 round 的 migration=%v, %v", applied, err)
	}

	occStore := NewMemoryStore()
	if err := occStore.validateA2ACommitLocked(
		&domain.Task{ID: "missing", Version: 1}, 1, nil, 0, round, intent, true,
	); !errors.Is(err, ErrA2AConcurrentModification) {
		t.Fatalf("command 前置 OCC 错误=%v", err)
	}
	occStore.a2aRounds[round.ID] = cloneA2ARound(*round)
	duplicateIntent := memoryBranchIntent("duplicate-round-intent", round, "different-command", now)
	duplicateIntent.Operation = domain.TaskA2AOperationCancel
	if err := occStore.CommitA2ACommand(ctx, A2ACommandCommit{
		Round: round, Intent: duplicateIntent, CreateRound: true,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("已存在 round 的 create 错误=%v", err)
	}
	if err := occStore.CommitA2ADispatchResult(ctx, round, round.Version, intent, intent.Version); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失 dispatch intent OCC 错误=%v", err)
	}
}

func memoryBranchRound(id, taskID, executionID, workerID, commandID string, attempt, turn int, now time.Time) *domain.TaskA2ARound {
	return &domain.TaskA2ARound{
		ID: id, TaskID: taskID, ExecutionID: executionID, WorkerID: workerID, CommandID: commandID,
		Operation: domain.TaskA2AOperationStart, Attempt: attempt, Turn: turn, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
}

func memoryBranchIntent(id string, round *domain.TaskA2ARound, commandID string, now time.Time) *domain.TaskA2ADispatchIntent {
	return &domain.TaskA2ADispatchIntent{
		ID: id, RoundID: round.ID, TaskID: round.TaskID, ExecutionID: round.ExecutionID, WorkerID: round.WorkerID,
		CommandID: commandID, Operation: round.Operation, Status: domain.A2ADispatchPending,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}
