package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		{Version: "003_drop_task_target_branch", SQL: migrations.DropTaskBranchSQL},
		{Version: "004_worker_agent_runtime_env", SQL: migrations.WorkerAgentRuntimeEnvSQL},
		{Version: "005_task_agent_session", SQL: migrations.TaskAgentSessionSQL},
		{Version: "006_task_display_dates", SQL: migrations.TaskDisplayDatesSQL},
		{Version: "007_task_agent_config", SQL: migrations.TaskAgentConfigSQL},
		{Version: "008_worker_current_task_ids", SQL: migrations.WorkerCurrentTaskIDsSQL},
		{Version: "009_task_interactions", SQL: migrations.TaskInteractionsSQL},
		{Version: "010_unique_worker_name", SQL: migrations.UniqueWorkerNameSQL},
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
	hasTargetBranch, err := sqliteTableHasColumn(ctx, sqlStore, "tasks", "target_branch")
	if err != nil {
		t.Fatal(err)
	}
	if hasTargetBranch {
		t.Fatal("tasks.target_branch should be absent after versioned migrations")
	}
	hasWorkerAgentEnv, err := sqliteTableHasColumn(ctx, sqlStore, "worker_agent_env_vars", "agent_type")
	if err != nil {
		t.Fatal(err)
	}
	if !hasWorkerAgentEnv {
		t.Fatal("worker_agent_env_vars.agent_type should exist after versioned migrations")
	}
	hasTaskAgentSession, err := sqliteTableHasColumn(ctx, sqlStore, "tasks", "agent_session_id")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTaskAgentSession {
		t.Fatal("tasks.agent_session_id should exist after versioned migrations")
	}
	hasTaskStartDate, err := sqliteTableHasColumn(ctx, sqlStore, "tasks", "start_date")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTaskStartDate {
		t.Fatal("tasks.start_date should exist after versioned migrations")
	}
	hasTaskEndDate, err := sqliteTableHasColumn(ctx, sqlStore, "tasks", "end_date")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTaskEndDate {
		t.Fatal("tasks.end_date should exist after versioned migrations")
	}
	hasTaskAgentConfig, err := sqliteTableHasColumn(ctx, sqlStore, "tasks", "agent_config")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTaskAgentConfig {
		t.Fatal("tasks.agent_config should exist after versioned migrations")
	}
	hasWorkerCurrentTaskID, err := sqliteTableHasColumn(ctx, sqlStore, "workers", "current_task_id")
	if err != nil {
		t.Fatal(err)
	}
	if hasWorkerCurrentTaskID {
		t.Fatal("workers.current_task_id should be absent after versioned migrations")
	}
	hasWorkerCurrentTaskIDs, err := sqliteTableHasColumn(ctx, sqlStore, "workers", "current_task_ids")
	if err != nil {
		t.Fatal(err)
	}
	if !hasWorkerCurrentTaskIDs {
		t.Fatal("workers.current_task_ids should exist after versioned migrations")
	}
	hasTaskInteractionStatus, err := sqliteTableHasColumn(ctx, sqlStore, "task_interactions", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTaskInteractionStatus {
		t.Fatal("task_interactions.status should exist after versioned migrations")
	}
	hasWorkerNameUniqueIndex, err := sqliteIndexExists(ctx, sqlStore, "idx_workers_name_unique")
	if err != nil {
		t.Fatal(err)
	}
	if !hasWorkerNameUniqueIndex {
		t.Fatal("workers.name unique index should exist after versioned migrations")
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
	if err := sqlStore.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("SaveSettings returned error: %v", err)
	}
	loadedSettings, err := sqlStore.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSettings.WorkerHeartbeat != "45s" {
		t.Fatalf("loaded settings = %+v", loadedSettings)
	}

	project, err := domain.NewProject(domain.NewProjectInput{ID: "project-list", Name: "P", GitURL: "git://repo", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := domain.NewWorker(domain.NewWorkerInput{
		ID:                 "worker-list",
		Name:               "W",
		SupportedAgents:    []domain.AgentType{domain.AgentCodex},
		WorkDir:            "/tmp",
		StartupCommand:     "boot",
		ProjectBindingMode: domain.WorkerSpecificProjects,
		BoundProjectIDs:    []string{project.ID},
		Capabilities:       map[string]string{"os": "test"},
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{
			AgentType: domain.AgentCodex,
			Vars: []domain.AgentRuntimeEnvVar{
				{Key: "TOKEN", Value: "secret", Description: "api token", Enabled: true, Sensitive: true},
			},
		}},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.Connect(now)
	task, err := domain.NewTask(domain.NewTaskInput{ID: "task-list", Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex, BaseBranch: "main", PreCommands: []string{"pre"}, PostCommands: []string{"post"}, StartDate: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	task.AgentSessionID = "session-list"
	task.AgentConfig = domain.AgentExecutionConfig{
		WorkMode: domain.AgentWorkModeImplement,
		Codex: domain.CodexExecutionConfig{
			Model:          "gpt-5.4",
			SandboxMode:    domain.CodexSandboxWorkspaceWrite,
			ApprovalPolicy: domain.CodexApprovalNever,
		},
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
	interaction := domain.TaskInteraction{
		ID:             "interaction-list",
		TaskID:         task.ID,
		Kind:           domain.TaskInteractionCommandApproval,
		Status:         domain.TaskInteractionPending,
		Title:          "Approve",
		Body:           "Run command",
		RawPayload:     `{"command":"make test"}`,
		AgentSessionID: "session-list",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := sqlStore.SaveTaskInteraction(ctx, interaction); err != nil {
		t.Fatalf("SaveTaskInteraction returned error: %v", err)
	}
	loadedInteraction, err := sqlStore.TaskInteraction(ctx, interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedInteraction.Kind != domain.TaskInteractionCommandApproval || loadedInteraction.RawPayload != interaction.RawPayload {
		t.Fatalf("TaskInteraction = %+v", loadedInteraction)
	}
	filteredInteractions, err := sqlStore.TaskInteractions(ctx, task.ID, domain.TaskInteractionPending)
	if err != nil || len(filteredInteractions) != 1 {
		t.Fatalf("TaskInteractions filtered = %+v, %v", filteredInteractions, err)
	}
	if err := sqlStore.CancelPendingTaskInteractions(ctx, task.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("CancelPendingTaskInteractions returned error: %v", err)
	}
	canceledInteraction, err := sqlStore.TaskInteraction(ctx, interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceledInteraction.Status != domain.TaskInteractionCanceled || canceledInteraction.ResponseDecision != domain.TaskInteractionCancel {
		t.Fatalf("canceled interaction = %+v", canceledInteraction)
	}
	if projects, err := sqlStore.Projects(ctx); err != nil || len(projects) != 1 || projects[0].ID != project.ID {
		t.Fatalf("Projects = %+v, %v", projects, err)
	}
	if workers, err := sqlStore.Workers(ctx); err != nil || len(workers) != 1 || workers[0].StartupCommand != "boot" || workers[0].LastHeartbeatAt == nil || workers[0].EnabledRuntimeEnv(domain.AgentCodex)[0].Value != "secret" {
		t.Fatalf("Workers = %+v, %v", workers, err)
	}
	if tasks, err := sqlStore.Tasks(ctx); err != nil || len(tasks) != 1 || len(tasks[0].PostCommands) != 1 || tasks[0].AgentSessionID != "session-list" || tasks[0].AgentConfig.Codex.Model != "gpt-5.4" || !tasks[0].StartDate.Equal(time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)) || !tasks[0].EndDate.Equal(time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)) {
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
	searched, err := sqlStore.DomainEvents(ctx, domain.EventFilter{Search: "taskcreated"})
	if err != nil || len(searched) != 1 || searched[0].EventType != "TaskCreated" {
		t.Fatalf("DomainEvents searched = %+v, %v", searched, err)
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
	var envRows int
	if err := sqlStore.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM worker_agent_env_vars WHERE worker_id = `+sqlStore.bind(1), worker.ID).Scan(&envRows); err != nil {
		t.Fatal(err)
	}
	if envRows != 0 {
		t.Fatalf("worker env rows after delete = %d, want 0", envRows)
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

func TestUniqueWorkerNameMigrationRenamesExistingDuplicates(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "manager.db")
	sqlStore, err := OpenSQLStore(ctx, SQLDriverSQLite, dsn)
	if err != nil {
		t.Fatalf("OpenSQLStore returned error: %v", err)
	}
	defer sqlStore.Close()
	schemaBeforeUniqueName := strings.Replace(migrations.SchemaSQL, "CREATE UNIQUE INDEX IF NOT EXISTS idx_workers_name_unique ON workers(name);", "", 1)
	if err := sqlStore.MigrateVersioned(ctx, []Migration{{Version: "001_init", SQL: schemaBeforeUniqueName}}); err != nil {
		t.Fatalf("initial migration returned error: %v", err)
	}
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	for _, workerID := range []string{"worker-a", "worker-b"} {
		worker, err := domain.NewWorker(domain.NewWorkerInput{
			ID:              workerID,
			Name:            "Duplicate Worker",
			SupportedAgents: []domain.AgentType{domain.AgentCodex},
			WorkDir:         "/tmp/" + workerID,
			Now:             now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlStore.SaveWorker(ctx, worker); err != nil {
			t.Fatalf("SaveWorker %s returned error: %v", workerID, err)
		}
	}
	if err := sqlStore.MigrateVersioned(ctx, []Migration{{Version: "010_unique_worker_name", SQL: migrations.UniqueWorkerNameSQL}}); err != nil {
		t.Fatalf("unique name migration returned error: %v", err)
	}
	first, err := sqlStore.Worker(ctx, "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := sqlStore.Worker(ctx, "worker-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.Name == second.Name {
		t.Fatalf("duplicate worker names were not made unique: %q", first.Name)
	}
	duplicate, err := domain.NewWorker(domain.NewWorkerInput{
		ID:              "worker-c",
		Name:            first.Name,
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker-c",
		Now:             now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveWorker(ctx, duplicate); err == nil {
		t.Fatal("unique worker name index should reject a new duplicate name")
	}
}

func TestSQLStoreDeleteTaskRemovesTaskAndDetailRows(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "manager.db")
	sqlStore, err := OpenSQLStore(ctx, SQLDriverSQLite, dsn)
	if err != nil {
		t.Fatalf("OpenSQLStore returned error: %v", err)
	}
	defer sqlStore.Close()
	if err := sqlStore.Migrate(ctx, migrations.SchemaSQL); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	project, err := domain.NewProject(domain.NewProjectInput{ID: "project-delete-task", Name: "P", GitURL: "git://repo", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	task, err := domain.NewTask(domain.NewTaskInput{ID: "task-delete", Title: "Delete Me", ProjectID: project.ID, AgentType: domain.AgentCodex, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.AppendTaskLog(ctx, domain.TaskLog{ID: "log-delete", TaskID: task.ID, Stream: "stdout", Content: "hello", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.AppendConversation(ctx, domain.ConversationMessage{ID: "conv-delete", TaskID: task.ID, Role: "assistant", Content: "done", Metadata: map[string]string{"tool": "codex"}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.SaveTaskInteraction(ctx, domain.TaskInteraction{
		ID:        "interaction-delete",
		TaskID:    task.ID,
		Kind:      domain.TaskInteractionCommandApproval,
		Status:    domain.TaskInteractionPending,
		Title:     "Approve",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := sqlStore.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask returned error: %v", err)
	}
	if _, err := sqlStore.Task(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted Task err = %v, want not found", err)
	}
	if logs, err := sqlStore.TaskLogs(ctx, task.ID); err != nil || len(logs) != 0 {
		t.Fatalf("TaskLogs after delete = %+v, %v", logs, err)
	}
	if messages, err := sqlStore.TaskConversations(ctx, task.ID); err != nil || len(messages) != 0 {
		t.Fatalf("TaskConversations after delete = %+v, %v", messages, err)
	}
	if interactions, err := sqlStore.TaskInteractions(ctx, task.ID, ""); err != nil || len(interactions) != 0 {
		t.Fatalf("TaskInteractions after delete = %+v, %v", interactions, err)
	}
	if _, err := sqlStore.TaskInteraction(ctx, "interaction-delete"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted TaskInteraction err = %v, want not found", err)
	}
	if err := sqlStore.DeleteTask(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteTask missing err = %v, want not found", err)
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
		Name:               "SQL Worker " + suffix,
		SupportedAgents:    []domain.AgentType{domain.AgentCodex},
		WorkDir:            "/tmp/worker",
		ProjectBindingMode: domain.WorkerSpecificProjects,
		BoundProjectIDs:    []string{projectID},
		Capabilities:       map[string]string{"os": "test"},
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{
			AgentType: domain.AgentCodex,
			Vars: []domain.AgentRuntimeEnvVar{
				{Key: "TOKEN_" + suffix, Value: "secret", Enabled: true, Sensitive: true},
			},
		}},
		Now: now,
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
		PreCommands:  []string{"make pre"},
		PostCommands: []string{"make post"},
		StartDate:    time.Date(2026, 4, 26, 8, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2026, 4, 27, 8, 0, 0, 0, time.UTC),
		Now:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	task.AgentSessionID = "session-sql"
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
	if loadedTask.Title != "SQL Task" || len(loadedTask.PreCommands) != 1 || loadedTask.PreCommands[0] != "make pre" || loadedTask.AgentSessionID != "session-sql" || !loadedTask.StartDate.Equal(time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)) || !loadedTask.EndDate.Equal(time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("loaded task = %+v", loadedTask)
	}
	loadedWorker, err := reopened.Worker(ctx, workerID)
	if err != nil {
		t.Fatalf("Worker returned error: %v", err)
	}
	if loadedWorker.Status != domain.WorkerOnline || len(loadedWorker.BoundProjectIDs) != 1 || loadedWorker.BoundProjectIDs[0] != projectID {
		t.Fatalf("loaded worker = %+v", loadedWorker)
	}
	if runtime := loadedWorker.EnabledRuntimeEnv(domain.AgentCodex); len(runtime) != 1 || runtime[0].Value != "secret" {
		t.Fatalf("loaded worker runtime env = %+v", loadedWorker.AgentRuntimeEnv)
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

func sqliteIndexExists(ctx context.Context, sqlStore *SQLStore, indexName string) (bool, error) {
	rows, err := sqlStore.db.QueryContext(ctx, `PRAGMA index_list(workers)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if name == indexName && unique == 1 {
			return true, nil
		}
	}
	return false, rows.Err()
}
