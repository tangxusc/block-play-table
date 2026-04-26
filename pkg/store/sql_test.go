package store

import (
	"context"
	"database/sql"
	"errors"
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

func TestSQLStoreVersionedMigrationListsDeletionAndHelpers(t *testing.T) {
	ctx := context.Background()
	if _, err := OpenSQLStore(ctx, SQLDriverSQLite, ""); err == nil {
		t.Fatal("empty sqlite dsn should fail")
	}
	if _, err := OpenSQLStore(ctx, "unknown", "db"); err == nil {
		t.Fatal("unknown SQL driver should fail")
	}
	dsn := filepath.Join(t.TempDir(), "nested", "manager.db")
	sqlStore, err := OpenSQLStore(ctx, SQLDriverSQLite, dsn)
	if err != nil {
		t.Fatalf("OpenSQLStore returned error: %v", err)
	}
	defer sqlStore.Close()
	versioned := []Migration{
		{Version: "", SQL: ""},
		{Version: "001_init", SQL: migrations.SchemaSQL},
		{Version: "002_drop_project_setup_commands", SQL: migrations.DropProjectSetupCommandsSQL},
	}
	if err := sqlStore.MigrateVersioned(ctx, versioned); err != nil {
		t.Fatalf("MigrateVersioned returned error: %v", err)
	}
	if err := sqlStore.MigrateVersioned(ctx, versioned); err != nil {
		t.Fatalf("second MigrateVersioned returned error: %v", err)
	}
	hasSetupCommands, err := sqliteTableHasColumn(ctx, sqlStore, "projects", "setup_commands")
	if err != nil {
		t.Fatal(err)
	}
	if hasSetupCommands {
		t.Fatal("projects.setup_commands should be removed after versioned migrations")
	}

	defaultSettings, err := sqlStore.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defaultSettings.WorkerHeartbeat != "90s" {
		t.Fatalf("default settings = %+v", defaultSettings)
	}
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	settings := domain.NewSettings(now)
	settings.UpdateWorkerHeartbeatTimeout("45s", now)
	settings.UpdateAgentRuntimeEnvVars([]domain.AgentRuntimeEnvVar{{Key: "TOKEN", Value: "secret", Description: "api token", Enabled: true, Sensitive: true}}, now)
	if err := sqlStore.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("SaveSettings returned error: %v", err)
	}
	loadedSettings, err := sqlStore.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSettings.WorkerHeartbeat != "45s" || len(loadedSettings.AgentRuntimeEnvVars) != 1 || loadedSettings.AgentRuntimeEnvVars[0].Description != "api token" {
		t.Fatalf("loaded settings = %+v", loadedSettings)
	}

	project, err := domain.NewProject(domain.NewProjectInput{ID: "project-list", Name: "P", GitURL: "git://repo", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := domain.NewWorker(domain.NewWorkerInput{ID: "worker-list", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", StartupCommand: "boot", ProjectBindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{project.ID}, Capabilities: map[string]string{"os": "test"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	worker.Connect(now)
	task, err := domain.NewTask(domain.NewTaskInput{ID: "task-list", Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex, BaseBranch: "main", TargetBranch: "task/list", PreCommands: []string{"pre"}, PostCommands: []string{"post"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if projects, err := sqlStore.Projects(ctx); err != nil || len(projects) != 1 || projects[0].ID != project.ID {
		t.Fatalf("Projects = %+v, %v", projects, err)
	}
	if workers, err := sqlStore.Workers(ctx); err != nil || len(workers) != 1 || workers[0].StartupCommand != "boot" || workers[0].LastHeartbeatAt == nil {
		t.Fatalf("Workers = %+v, %v", workers, err)
	}
	if tasks, err := sqlStore.Tasks(ctx); err != nil || len(tasks) != 1 || len(tasks[0].PostCommands) != 1 {
		t.Fatalf("Tasks = %+v, %v", tasks, err)
	}
	if err := sqlStore.AppendEvents(ctx, nil); err != nil {
		t.Fatalf("AppendEvents(nil) returned error: %v", err)
	}
	events := task.PullEvents()
	if err := sqlStore.AppendEvents(ctx, events); err != nil {
		t.Fatalf("AppendEvents returned error: %v", err)
	}
	if err := sqlStore.AppendEvents(ctx, events); err != nil {
		t.Fatalf("duplicate AppendEvents returned error: %v", err)
	}
	filtered, err := sqlStore.DomainEvents(ctx, domain.EventFilter{AggregateType: "Task", EventType: "TaskCreated"})
	if err != nil || len(filtered) != 1 || filtered[0].AggregateID != task.ID {
		t.Fatalf("DomainEvents filtered = %+v, %v", filtered, err)
	}
	outbox, err := sqlStore.OutboxMessages(ctx, true)
	if err != nil || len(outbox) != 1 {
		t.Fatalf("OutboxMessages = %+v, %v", outbox, err)
	}
	if err := sqlStore.MarkOutboxPublished(ctx, nil, now); err != nil {
		t.Fatalf("MarkOutboxPublished(nil) returned error: %v", err)
	}
	if ok, err := sqlStore.MarkMessageProcessed(ctx, ""); err != nil || !ok {
		t.Fatalf("empty MarkMessageProcessed = %v, %v", ok, err)
	}
	if err := sqlStore.DeleteWorker(ctx, worker.ID); err != nil {
		t.Fatalf("DeleteWorker returned error: %v", err)
	}
	if _, err := sqlStore.Worker(ctx, worker.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted Worker err = %v, want not found", err)
	}
	if err := sqlStore.DeleteWorker(ctx, worker.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteWorker missing err = %v, want not found", err)
	}
	if _, err := sqlStore.Task(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing Task err = %v, want not found", err)
	}
	if _, err := sqlStore.Project(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing Project err = %v, want not found", err)
	}

	postgres := &SQLStore{dialect: postgresDialect}
	if got := postgres.bind(2); got != "$2" {
		t.Fatalf("postgres bind = %q", got)
	}
	if got := postgres.bindList(2, 3); got != "$2, $3, $4" {
		t.Fatalf("postgres bindList = %q", got)
	}
	if _, err := encodeJSON(func() {}); err == nil {
		t.Fatal("encodeJSON of function should fail")
	}
	var decoded []string
	if err := decodeJSON("", &decoded); err != nil {
		t.Fatalf("decodeJSON empty returned error: %v", err)
	}
	if nullableString("") != nil || nullableTime(nil) != nil {
		t.Fatal("empty nullable helpers should return nil")
	}
	if nullableString("x") == nil || nullableTime(&now) == nil {
		t.Fatal("non-empty nullable helpers should return value")
	}
	if got := fromNullString(sql.NullString{String: "value", Valid: true}); got != "value" {
		t.Fatalf("fromNullString valid = %q", got)
	}
	for _, dsn := range []string{":memory:", "file:test.db?mode=memory&cache=shared", "manager.db"} {
		if err := ensureSQLiteDir(dsn); err != nil {
			t.Fatalf("ensureSQLiteDir(%q) returned error: %v", dsn, err)
		}
	}
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
	if loadedProject.WorktreeNamePrefix != "sql" {
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

func sqliteTableHasColumn(ctx context.Context, sqlStore *SQLStore, table, column string) (bool, error) {
	rows, err := sqlStore.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
