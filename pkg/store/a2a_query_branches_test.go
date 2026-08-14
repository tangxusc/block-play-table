package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestMemoryA2AQueryExcludesForeignFutureTerminalAndLimitsResults(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	storage := NewMemoryStore()
	completedAt := now
	storage.a2aRounds["foreign"] = domain.TaskA2ARound{ID: "foreign", TaskID: "other", UpdatedAt: now}
	storage.a2aRounds["unbound"] = domain.TaskA2ARound{ID: "unbound", TaskID: "task", UpdatedAt: now}
	storage.a2aRounds["terminal"] = domain.TaskA2ARound{
		ID: "terminal", TaskID: "task", A2ATaskID: "remote", ContextID: "context", CompletedAt: &completedAt, UpdatedAt: now,
	}
	storage.a2aRounds["active"] = domain.TaskA2ARound{
		ID: "active", TaskID: "task", A2ATaskID: "remote-active", ContextID: "context-active", UpdatedAt: now,
	}
	rounds, err := storage.TaskA2ARounds(ctx, "task")
	if err != nil || len(rounds) != 3 {
		t.Fatalf("Task rounds = %+v, %v", rounds, err)
	}
	if _, err := storage.LatestTaskA2ARound(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失 latest round 错误 = %v", err)
	}
	reconcile, err := storage.A2ARoundsForReconcile(ctx)
	if err != nil || len(reconcile) != 1 || reconcile[0].ID != "active" {
		t.Fatalf("待对账 rounds = %+v, %v", reconcile, err)
	}

	lastAttempt := now.Add(-time.Minute)
	storage.a2aIntents = map[string]domain.TaskA2ADispatchIntent{
		"future": {ID: "future", Status: domain.A2ADispatchPending, AvailableAt: now.Add(time.Hour), CreatedAt: now},
		"sent":   {ID: "sent", Status: domain.A2ADispatchSent, AvailableAt: now.Add(-time.Hour), CreatedAt: now},
		"later":  {ID: "later", Status: domain.A2ADispatchPending, AvailableAt: now.Add(-time.Minute), CreatedAt: now},
		"early":  {ID: "early", Status: domain.A2ADispatchPending, AvailableAt: now.Add(-time.Hour), CreatedAt: now.Add(time.Minute)},
		"stale":  {ID: "stale", Status: domain.A2ADispatchSending, LastAttemptAt: &lastAttempt, AvailableAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Minute)},
	}
	due, err := storage.A2ADispatchIntentsDue(ctx, now, 2)
	if err != nil || len(due) != 2 || due[0].ID != "stale" || due[1].ID != "early" {
		t.Fatalf("到期 intents = %+v, %v", due, err)
	}
	if _, err := storage.A2ADispatchIntent(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失 intent 错误 = %v", err)
	}
}

func TestMemoryA2AEqualTimeSortAndUnboundOCCBranches(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 30, 0, 0, time.UTC)
	rounds := []domain.TaskA2ARound{
		*memoryBranchRound("z", "task", "execution-z", "worker", "command-z", 1, 1, now),
		*memoryBranchRound("a", "task", "execution-a", "worker", "command-a", 1, 1, now),
	}
	sortA2ARounds(rounds)
	if rounds[0].ID != "a" || rounds[1].ID != "z" {
		t.Fatalf("同时间 round 排序 = %+v", rounds)
	}
	storage := NewMemoryStore()
	storage.a2aRounds["a"] = rounds[0]
	if err := storage.validateA2AOCCLocked(nil, 0, nil, 0, &rounds[0], rounds[0].Version, nil, 0); err != nil {
		t.Fatalf("未绑定 round OCC = %v", err)
	}
	storage.interactions["other"] = domain.TaskInteraction{ID: "other", TaskID: "other-task", Status: domain.TaskInteractionPending}
	storage.applyA2ACommonLocked(&domain.Task{ID: "task"}, nil, nil, nil, nil, nil, true)
	if storage.interactions["other"].Status != domain.TaskInteractionPending {
		t.Fatal("其他 Task 的交互被取消")
	}
}

func TestOpenSQLStoreDriverAliasAndInvalidPostgresBranches(t *testing.T) {
	ctx := context.Background()
	storage, err := OpenSQLStore(ctx, "sqlite3", filepath.Join(t.TempDir(), "alias.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	for _, driver := range []string{"postgres", "postgresql", "pgx"} {
		if _, err := OpenSQLStore(ctx, driver, "invalid://dsn"); err == nil {
			t.Fatalf("%s 非法 DSN 未失败", driver)
		}
	}
}
