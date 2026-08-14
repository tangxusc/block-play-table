package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

// TestMemoryStoreDeleteTaskCascadesA2AAndGitState 验证任务删除会清理全部关联明细并保留其他任务数据。
func TestMemoryStoreDeleteTaskCascadesA2AAndGitState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	storage := NewMemoryStore()
	storage.tasks["target"] = &domain.Task{ID: "target"}
	storage.tasks["other"] = &domain.Task{ID: "other"}
	storage.interactions["target-interaction"] = domain.TaskInteraction{ID: "target-interaction", TaskID: "target"}
	storage.interactions["other-interaction"] = domain.TaskInteraction{ID: "other-interaction", TaskID: "other"}
	storage.gitBackups["target-backup"] = domain.TaskGitBackup{ID: "target-backup", TaskID: "target"}
	storage.gitBackups["other-backup"] = domain.TaskGitBackup{ID: "other-backup", TaskID: "other"}
	storage.gitSnapshots["target-snapshot"] = domain.TaskGitTurnSnapshot{ID: "target-snapshot", TaskID: "target"}
	storage.gitSnapshots["other-snapshot"] = domain.TaskGitTurnSnapshot{ID: "other-snapshot", TaskID: "other"}
	storage.a2aRounds["target-round"] = domain.TaskA2ARound{ID: "target-round", TaskID: "target", ExecutionID: "target-execution"}
	storage.a2aRounds["other-round"] = domain.TaskA2ARound{ID: "other-round", TaskID: "other", ExecutionID: "other-execution"}
	storage.a2aIntents["target-intent"] = domain.TaskA2ADispatchIntent{ID: "target-intent", TaskID: "target"}
	storage.a2aIntents["other-intent"] = domain.TaskA2ADispatchIntent{ID: "other-intent", TaskID: "other"}
	storage.a2aEventInbox[a2aInboxKey("target-round", "event-round")] = domain.A2AEventInbox{RoundID: "target-round", ExecutionID: "other-execution", EventID: "event-round"}
	storage.a2aEventInbox[a2aInboxKey("other-round", "event-execution")] = domain.A2AEventInbox{RoundID: "other-round", ExecutionID: "target-execution", EventID: "event-execution"}
	storage.a2aEventInbox[a2aInboxKey("other-round", "event-other")] = domain.A2AEventInbox{RoundID: "other-round", ExecutionID: "other-execution", EventID: "event-other"}
	storage.logs = []domain.TaskLog{{ID: "target-log", TaskID: "target", CreatedAt: now}, {ID: "other-log", TaskID: "other", CreatedAt: now}}
	storage.conversations = []domain.ConversationMessage{{ID: "target-message", TaskID: "target", CreatedAt: now}, {ID: "other-message", TaskID: "other", CreatedAt: now}}

	if err := storage.DeleteTask(ctx, "target"); err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.gitBackups["target-backup"]; ok {
		t.Fatal("target backup remains")
	}
	if _, ok := storage.gitSnapshots["target-snapshot"]; ok {
		t.Fatal("target snapshot remains")
	}
	if _, ok := storage.a2aRounds["target-round"]; ok {
		t.Fatal("target round remains")
	}
	if _, ok := storage.a2aIntents["target-intent"]; ok {
		t.Fatal("target intent remains")
	}
	if len(storage.a2aEventInbox) != 1 || len(storage.logs) != 1 || len(storage.conversations) != 1 {
		t.Fatalf("cascade state inbox=%+v logs=%+v conversations=%+v", storage.a2aEventInbox, storage.logs, storage.conversations)
	}
	if _, ok := storage.gitBackups["other-backup"]; !ok {
		t.Fatal("other backup was removed")
	}
}

// TestMemoryStoreInteractionSortAndFilterBranches 验证交互筛选和 CreatedAt、ID 稳定排序的正反路径。
func TestMemoryStoreInteractionSortAndFilterBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 19, 0, 0, 0, time.UTC)
	storage := NewMemoryStore()
	for _, interaction := range []domain.TaskInteraction{
		{ID: "b", TaskID: "task", Status: domain.TaskInteractionPending, CreatedAt: now},
		{ID: "a", TaskID: "task", Status: domain.TaskInteractionAnswered, CreatedAt: now},
		{ID: "c", TaskID: "task", Status: domain.TaskInteractionPending, CreatedAt: now.Add(time.Minute)},
		{ID: "foreign", TaskID: "other", Status: domain.TaskInteractionPending, CreatedAt: now},
	} {
		storage.interactions[interaction.ID] = interaction
	}
	all, err := storage.TaskInteractions(ctx, "task", "")
	if err != nil || len(all) != 3 || all[0].ID != "a" || all[1].ID != "b" || all[2].ID != "c" {
		t.Fatalf("sorted interactions=%+v err=%v", all, err)
	}
	pending, err := storage.TaskInteractions(ctx, "task", domain.TaskInteractionPending)
	if err != nil || len(pending) != 2 || pending[0].ID != "b" {
		t.Fatalf("pending interactions=%+v err=%v", pending, err)
	}
	if err := storage.CancelPendingTaskInteractions(ctx, "task", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if storage.interactions["foreign"].Status != domain.TaskInteractionPending || storage.interactions["a"].Status != domain.TaskInteractionAnswered {
		t.Fatal("cancel changed unrelated or answered interaction")
	}
}

// TestMemoryStoreSnapshotEventAndOutboxBranches 验证快照选择、事件过滤和 outbox 未命中路径。
func TestMemoryStoreSnapshotEventAndOutboxBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	storage := NewMemoryStore()
	if _, err := storage.LatestTaskGitTurnSnapshot(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing snapshot err=%v", err)
	}
	for _, snapshot := range []domain.TaskGitTurnSnapshot{
		{ID: "foreign", TaskID: "other", CreatedAt: now.Add(2 * time.Hour)},
		{ID: "latest", TaskID: "task", CreatedAt: now.Add(time.Hour)},
		{ID: "earliest", TaskID: "task", CreatedAt: now},
	} {
		storage.gitSnapshots[snapshot.ID] = snapshot
	}
	latest, err := storage.LatestTaskGitTurnSnapshot(ctx, "task")
	if err != nil || latest.ID != "latest" {
		t.Fatalf("latest snapshot=%+v err=%v", latest, err)
	}

	events := []domain.DomainEvent{
		{EventID: "match", EventType: "Updated", AggregateType: "Task", AggregateID: "task", Payload: []byte(`{"needle":true}`), OccurredAt: now},
		{EventID: "other-id", EventType: "Created", AggregateType: "Project", AggregateID: "project", OccurredAt: now},
	}
	if err := storage.AppendEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	filters := []domain.EventFilter{
		{AggregateID: "task"},
		{AggregateType: "Task"},
		{EventType: "Updated"},
		{Search: "needle"},
	}
	for index, filter := range filters {
		got, err := storage.DomainEvents(ctx, filter)
		if err != nil || len(got) != 1 || got[0].EventID != "match" {
			t.Fatalf("filter %d events=%+v err=%v", index, got, err)
		}
	}
	if got, _ := storage.DomainEvents(ctx, domain.EventFilter{Search: "absent"}); len(got) != 0 {
		t.Fatalf("nonmatching search=%+v", got)
	}
	if err := storage.MarkOutboxPublished(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := storage.MarkOutboxPublished(ctx, []string{"out_match", "missing"}, now); err != nil {
		t.Fatal(err)
	}
	pending, err := storage.OutboxMessages(ctx, false)
	if err != nil || len(pending) != 1 || pending[0].ID != "out_other-id" {
		t.Fatalf("pending outbox=%+v err=%v", pending, err)
	}
}

// TestMemoryCloneWorkerBranches 验证 Worker 深拷贝同时处理空和非空能力、运行时环境。
func TestMemoryCloneWorkerBranches(t *testing.T) {
	if cloned := cloneWorker(&domain.Worker{}); cloned.Capabilities != nil || cloneWorkerAgentRuntimeEnv(nil) != nil {
		t.Fatalf("empty clone=%+v", cloned)
	}
	original := &domain.Worker{
		Capabilities: map[string]string{"key": "value"},
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{
			AgentType: domain.AgentCodex, Vars: []domain.AgentRuntimeEnvVar{{Key: "TOKEN", Value: "secret"}},
		}},
	}
	cloned := cloneWorker(original)
	cloned.Capabilities["key"] = "changed"
	cloned.AgentRuntimeEnv[0].Vars[0].Value = "changed"
	if original.Capabilities["key"] != "value" || original.AgentRuntimeEnv[0].Vars[0].Value != "secret" {
		t.Fatal("clone aliases original worker")
	}
}
