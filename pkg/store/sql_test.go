package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/migrations"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestSQLStoreSQLitePersistsAcrossOpen(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "manager.db")
	runSQLStorePersistenceContract(t, ctx, SQLDriverSQLite, dsn)
	runSQLStorePersistenceContract(t, ctx, SQLDriverSQLite, dsn)
}

func TestSQLStorePostgresPersistenceContract(t *testing.T) {
	dsn := os.Getenv("BPT_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("BPT_POSTGRES_TEST_DSN is not set")
	}
	runSQLStorePersistenceContract(t, context.Background(), SQLDriverPostgres, dsn)
}

func runSQLStorePersistenceContract(t *testing.T, ctx context.Context, driver, dsn string) {
	t.Helper()
	sqlStore, err := OpenSQLStore(ctx, driver, dsn)
	if err != nil {
		t.Fatalf("OpenSQLStore returned error: %v", err)
	}
	defer sqlStore.Close()
	if err := sqlStore.Migrate(ctx, migrations.SchemaSQL); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	projectID := "project-sql-" + suffix
	workerID := "worker-sql-" + suffix
	taskID := "task-sql-" + suffix
	messageID := "worker-message-" + suffix

	project, err := domain.NewProject(domain.NewProjectInput{
		ID:                 projectID,
		Name:               "SQL Project",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "sql",
		SetupCommands:      []string{"git status"},
		Now:                now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveProject(ctx, project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	worker, err := domain.NewWorker(domain.NewWorkerInput{
		ID:                 workerID,
		Name:               "SQL Worker",
		SupportedAgents:    []domain.AgentType{domain.AgentCodex},
		WorkDir:            "/tmp/worker",
		ProjectBindingMode: domain.WorkerSpecificProjects,
		BoundProjectIDs:    []string{projectID},
		Capabilities:       map[string]string{"os": "test"},
		Now:                now,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.Connect(now)
	if err := sqlStore.SaveWorker(ctx, worker); err != nil {
		t.Fatalf("SaveWorker returned error: %v", err)
	}
	task, err := domain.NewTask(domain.NewTaskInput{
		ID:           taskID,
		Title:        "SQL Task",
		ProjectID:    projectID,
		AgentType:    domain.AgentCodex,
		BaseBranch:   "main",
		TargetBranch: "task/sql",
		PreCommands:  []string{"make pre"},
		PostCommands: []string{"make post"},
		Now:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveTask(ctx, task); err != nil {
		t.Fatalf("SaveTask returned error: %v", err)
	}
	if err := sqlStore.AppendTaskLog(ctx, domain.TaskLog{ID: "log-" + suffix, TaskID: taskID, Stream: "stdout", Content: "hello", CreatedAt: now}); err != nil {
		t.Fatalf("AppendTaskLog returned error: %v", err)
	}
	if err := sqlStore.AppendConversation(ctx, domain.ConversationMessage{ID: "conv-" + suffix, TaskID: taskID, Role: "assistant", Content: "done", Metadata: map[string]string{"tool": "codex"}, CreatedAt: now}); err != nil {
		t.Fatalf("AppendConversation returned error: %v", err)
	}
	if err := sqlStore.AppendEvents(ctx, task.PullEvents()); err != nil {
		t.Fatalf("AppendEvents returned error: %v", err)
	}
	settings := domain.NewSettings(now)
	settings.UpdateAgentRuntimeEnvVars([]domain.AgentRuntimeEnvVar{{Key: "TOKEN_" + suffix, Value: "secret", Enabled: true, Sensitive: true}}, now)
	if err := sqlStore.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("SaveSettings returned error: %v", err)
	}
	if ok, err := sqlStore.MarkMessageProcessed(ctx, messageID); err != nil || !ok {
		t.Fatalf("first MarkMessageProcessed = %v, %v", ok, err)
	}

	reopened, err := OpenSQLStore(ctx, driver, dsn)
	if err != nil {
		t.Fatalf("reopen OpenSQLStore returned error: %v", err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(ctx, migrations.SchemaSQL); err != nil {
		t.Fatalf("reopen Migrate returned error: %v", err)
	}
	loadedTask, err := reopened.Task(ctx, taskID)
	if err != nil {
		t.Fatalf("Task returned error: %v", err)
	}
	if loadedTask.Title != "SQL Task" || len(loadedTask.PreCommands) != 1 || loadedTask.PreCommands[0] != "make pre" {
		t.Fatalf("loaded task = %+v", loadedTask)
	}
	loadedWorker, err := reopened.Worker(ctx, workerID)
	if err != nil {
		t.Fatalf("Worker returned error: %v", err)
	}
	if loadedWorker.Status != domain.WorkerOnline || len(loadedWorker.BoundProjectIDs) != 1 || loadedWorker.BoundProjectIDs[0] != projectID {
		t.Fatalf("loaded worker = %+v", loadedWorker)
	}
	loadedProject, err := reopened.Project(ctx, projectID)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}
	if loadedProject.WorktreeNamePrefix != "sql" || len(loadedProject.SetupCommands) != 1 {
		t.Fatalf("loaded project = %+v", loadedProject)
	}
	if logs, err := reopened.TaskLogs(ctx, taskID); err != nil || len(logs) != 1 || logs[0].Content != "hello" {
		t.Fatalf("TaskLogs = %+v, %v", logs, err)
	}
	if conversations, err := reopened.TaskConversations(ctx, taskID); err != nil || len(conversations) != 1 || conversations[0].Metadata["tool"] != "codex" {
		t.Fatalf("TaskConversations = %+v, %v", conversations, err)
	}
	if events, err := reopened.DomainEvents(ctx, domain.EventFilter{AggregateID: taskID}); err != nil || len(events) != 1 || events[0].EventType != "TaskCreated" {
		t.Fatalf("DomainEvents = %+v, %v", events, err)
	}
	outbox, err := reopened.OutboxMessages(ctx, false)
	if err != nil {
		t.Fatalf("OutboxMessages returned error: %v", err)
	}
	foundOutbox := ""
	for _, message := range outbox {
		if message.Event.AggregateID == taskID {
			foundOutbox = message.ID
			break
		}
	}
	if foundOutbox == "" {
		t.Fatalf("pending outbox did not include task %s: %+v", taskID, outbox)
	}
	if err := reopened.MarkOutboxPublished(ctx, []string{foundOutbox}, now.Add(time.Second)); err != nil {
		t.Fatalf("MarkOutboxPublished returned error: %v", err)
	}
	if ok, err := reopened.MarkMessageProcessed(ctx, messageID); err != nil || ok {
		t.Fatalf("duplicate MarkMessageProcessed = %v, %v", ok, err)
	}
	loadedSettings, err := reopened.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings returned error: %v", err)
	}
	if len(loadedSettings.AgentRuntimeEnvVars) == 0 {
		t.Fatal("settings env vars were not persisted")
	}
}
