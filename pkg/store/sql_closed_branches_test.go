package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestSQLStoreA2ARejectsInvalidPublicArgumentsBeforeTransaction(t *testing.T) {
	ctx := context.Background()
	storage, err := OpenSQLStore(ctx, SQLDriverSQLite, filepath.Join(t.TempDir(), "invalid-a2a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	now := time.Now().UTC()
	round := &domain.TaskA2ARound{
		ID: "round", TaskID: "task", ExecutionID: "execution", WorkerID: "worker",
		CommandID: "command", Operation: domain.TaskA2AOperationStart, Attempt: 1, Turn: 1,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	intent := &domain.TaskA2ADispatchIntent{
		ID: "intent", RoundID: round.ID, TaskID: round.TaskID, ExecutionID: round.ExecutionID,
		WorkerID: round.WorkerID, CommandID: round.CommandID, Operation: round.Operation,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}

	checks := []struct {
		name string
		run  func() error
	}{
		{"command missing round", func() error { return storage.CommitA2ACommand(ctx, A2ACommandCommit{}) }},
		{"command association", func() error {
			changed := *intent
			changed.RoundID = "other"
			return storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: round, Intent: &changed})
		}},
		{"command invalid create operation", func() error {
			changedRound := *round
			changedRound.Operation = domain.TaskA2AOperationCancel
			changedIntent := *intent
			changedIntent.Operation = domain.TaskA2AOperationCancel
			return storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: &changedRound, Intent: &changedIntent, CreateRound: true})
		}},
		{"projection missing round", func() error {
			_, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{})
			return err
		}},
		{"projection inbox association", func() error {
			_, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{
				Round: round, Inbox: &domain.A2AEventInbox{RoundID: "other", ExecutionID: round.ExecutionID},
			})
			return err
		}},
		{"migration missing task", func() error {
			_, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{})
			return err
		}},
		{"migration worker association", func() error {
			_, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{
				Task: &domain.Task{ID: "task", WorkerID: "worker"}, Worker: &domain.Worker{ID: "other"},
			})
			return err
		}},
		{"update missing round", func() error { return storage.UpdateA2ARound(ctx, nil, 0) }},
		{"update missing intent", func() error { return storage.UpdateA2ADispatchIntent(ctx, nil, 0) }},
		{"dispatch missing association", func() error { return storage.CommitA2ADispatchResult(ctx, nil, 0, nil, 0) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil {
				t.Fatal("非法 A2A 参数未失败")
			}
		})
	}
}

func TestSQLStorePublicOperationsPropagateClosedDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	storage, err := OpenSQLStore(ctx, SQLDriverSQLite, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := &domain.Task{ID: "task", ProjectID: "project", Version: 1, CreatedAt: now, UpdatedAt: now}
	worker := &domain.Worker{ID: "worker", Version: 1, CreatedAt: now, UpdatedAt: now}
	project := &domain.Project{ID: "project", Version: 1, CreatedAt: now, UpdatedAt: now}
	settings := domain.NewSettings(now)
	round := &domain.TaskA2ARound{
		ID: "round", TaskID: task.ID, ExecutionID: "execution", WorkerID: worker.ID,
		CommandID: "command", Operation: domain.TaskA2AOperationStart, Attempt: 1, Turn: 1,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	intent := &domain.TaskA2ADispatchIntent{
		ID: "intent", RoundID: round.ID, TaskID: round.TaskID, ExecutionID: round.ExecutionID,
		WorkerID: round.WorkerID, CommandID: round.CommandID, Operation: round.Operation,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}

	operations := []struct {
		name string
		run  func() error
	}{
		{"ping", func() error { return storage.Ping(ctx) }},
		{"migrate", func() error { return storage.Migrate(ctx, "CREATE TABLE x (id TEXT)") }},
		{"migrate versioned", func() error { return storage.MigrateVersioned(ctx, []Migration{{Version: "1", SQL: "SELECT 1"}}) }},
		{"save task", func() error { return storage.SaveTask(ctx, task) }},
		{"task", func() error { _, err := storage.Task(ctx, task.ID); return err }},
		{"tasks", func() error { _, err := storage.Tasks(ctx); return err }},
		{"delete task", func() error { return storage.DeleteTask(ctx, task.ID) }},
		{"save worker", func() error { return storage.SaveWorker(ctx, worker) }},
		{"worker", func() error { _, err := storage.Worker(ctx, worker.ID); return err }},
		{"workers", func() error { _, err := storage.Workers(ctx); return err }},
		{"delete worker", func() error { return storage.DeleteWorker(ctx, worker.ID) }},
		{"save project", func() error { return storage.SaveProject(ctx, project) }},
		{"project", func() error { _, err := storage.Project(ctx, project.ID); return err }},
		{"projects", func() error { _, err := storage.Projects(ctx); return err }},
		{"save settings", func() error { return storage.SaveSettings(ctx, settings) }},
		{"settings", func() error { _, err := storage.Settings(ctx); return err }},
		{"append log", func() error {
			return storage.AppendTaskLog(ctx, domain.TaskLog{ID: "log", TaskID: task.ID, CreatedAt: now})
		}},
		{"logs", func() error { _, err := storage.TaskLogs(ctx, task.ID); return err }},
		{"append conversation", func() error {
			return storage.AppendConversation(ctx, domain.ConversationMessage{ID: "message", TaskID: task.ID, CreatedAt: now})
		}},
		{"conversations", func() error { _, err := storage.TaskConversations(ctx, task.ID); return err }},
		{"save interaction", func() error {
			return storage.SaveTaskInteraction(ctx, domain.TaskInteraction{ID: "interaction", TaskID: task.ID, CreatedAt: now, UpdatedAt: now})
		}},
		{"interaction", func() error { _, err := storage.TaskInteraction(ctx, "interaction"); return err }},
		{"interactions", func() error { _, err := storage.TaskInteractions(ctx, task.ID, ""); return err }},
		{"cancel interactions", func() error { return storage.CancelPendingTaskInteractions(ctx, task.ID, now) }},
		{"save backup", func() error {
			return storage.SaveTaskGitBackup(ctx, domain.TaskGitBackup{ID: "backup", TaskID: task.ID, CreatedAt: now})
		}},
		{"backups", func() error { _, err := storage.TaskGitBackups(ctx, task.ID); return err }},
		{"save snapshot", func() error {
			return storage.SaveTaskGitTurnSnapshot(ctx, domain.TaskGitTurnSnapshot{ID: "snapshot", TaskID: task.ID, CreatedAt: now})
		}},
		{"snapshot", func() error { _, err := storage.LatestTaskGitTurnSnapshot(ctx, task.ID); return err }},
		{"append events", func() error {
			return storage.AppendEvents(ctx, []domain.DomainEvent{{EventID: "event", AggregateID: task.ID, OccurredAt: now}})
		}},
		{"events", func() error { _, err := storage.DomainEvents(ctx, domain.EventFilter{}); return err }},
		{"outbox", func() error { _, err := storage.OutboxMessages(ctx, true); return err }},
		{"publish outbox", func() error { return storage.MarkOutboxPublished(ctx, []string{"event"}, now) }},
		{"commit command", func() error {
			return storage.CommitA2ACommand(ctx, A2ACommandCommit{Round: round, Intent: intent, CreateRound: true})
		}},
		{"commit projection", func() error {
			_, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{Round: round})
			return err
		}},
		{"commit migration", func() error {
			_, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{Task: task})
			return err
		}},
		{"event inbox", func() error { _, err := storage.A2AEventInbox(ctx, round.ID, "event"); return err }},
		{"rounds", func() error { _, err := storage.TaskA2ARounds(ctx, task.ID); return err }},
		{"round", func() error { _, err := storage.TaskA2ARound(ctx, round.ID); return err }},
		{"latest round", func() error { _, err := storage.LatestTaskA2ARound(ctx, task.ID); return err }},
		{"reconcile rounds", func() error { _, err := storage.A2ARoundsForReconcile(ctx); return err }},
		{"due intents", func() error { _, err := storage.A2ADispatchIntentsDue(ctx, now, 0); return err }},
		{"intent", func() error { _, err := storage.A2ADispatchIntent(ctx, intent.ID); return err }},
		{"update round", func() error { return storage.UpdateA2ARound(ctx, round, round.Version) }},
		{"update intent", func() error { return storage.UpdateA2ADispatchIntent(ctx, intent, intent.Version) }},
		{"commit dispatch", func() error {
			return storage.CommitA2ADispatchResult(ctx, round, round.Version, intent, intent.Version)
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err == nil {
				t.Fatal("关闭数据库后操作未返回错误")
			}
		})
	}
}
