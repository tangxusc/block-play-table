package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/tangxusc/block-play-table/pkg/domain"
	_ "modernc.org/sqlite"
)

const (
	SQLDriverMemory   = "memory"
	SQLDriverSQLite   = "sqlite"
	SQLDriverPostgres = "postgres"

	sqliteDialect   = "sqlite"
	postgresDialect = "postgres"
)

type SQLStore struct {
	db      *sql.DB
	dialect string
}

type Migration struct {
	Version string
	SQL     string
}

type migrationExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func OpenSQLStore(ctx context.Context, driver, dsn string) (*SQLStore, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("%s dsn is required", driver)
	}
	sqlDriver := ""
	dialect := ""
	switch driver {
	case "sqlite", "sqlite3":
		sqlDriver = "sqlite"
		dialect = sqliteDialect
		if err := ensureSQLiteDir(dsn); err != nil {
			return nil, err
		}
	case "postgres", "postgresql", "pgx":
		sqlDriver = "pgx"
		dialect = postgresDialect
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, err
	}
	if dialect == sqliteDialect {
		db.SetMaxOpenConns(1)
	}
	store := &SQLStore{db: db, dialect: dialect}
	if err := store.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLStore) Close() error {
	return s.db.Close()
}

func (s *SQLStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *SQLStore) Migrate(ctx context.Context, migration string) error {
	for _, statement := range strings.Split(migration, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if err := s.execMigrationStatement(ctx, s.db, statement); err != nil {
			return fmt.Errorf("run migration statement %q: %w", statement, err)
		}
	}
	return nil
}

func (s *SQLStore) MigrateVersioned(ctx context.Context, migrations []Migration) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return err
	}
	for _, migration := range migrations {
		if strings.TrimSpace(migration.Version) == "" || strings.TrimSpace(migration.SQL) == "" {
			continue
		}
		var applied string
		err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version = `+s.bind(1), migration.Version).Scan(&applied)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, statement := range strings.Split(migration.SQL, ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if err := s.execMigrationStatement(ctx, tx, statement); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("run migration %s statement %q: %w", migration.Version, statement, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (`+s.bindList(1, 2)+`)`, migration.Version, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStore) execMigrationStatement(ctx context.Context, exec migrationExecutor, statement string) error {
	if s.dialect == sqliteDialect {
		handled, err := s.execSQLiteDropColumnIfExists(ctx, exec, statement)
		if handled {
			return err
		}
		handled, err = s.execSQLiteAddColumnIfNotExists(ctx, exec, statement)
		if handled {
			return err
		}
	}
	_, err := exec.ExecContext(ctx, statement)
	return err
}

func (s *SQLStore) execSQLiteDropColumnIfExists(ctx context.Context, exec migrationExecutor, statement string) (bool, error) {
	fields := strings.Fields(statement)
	if len(fields) != 8 ||
		!strings.EqualFold(fields[0], "ALTER") ||
		!strings.EqualFold(fields[1], "TABLE") ||
		!strings.EqualFold(fields[3], "DROP") ||
		!strings.EqualFold(fields[4], "COLUMN") ||
		!strings.EqualFold(fields[5], "IF") ||
		!strings.EqualFold(fields[6], "EXISTS") {
		return false, nil
	}
	table := trimSQLIdentifier(fields[2])
	column := trimSQLIdentifier(fields[7])
	hasColumn, err := sqliteColumnExists(ctx, exec, table, column)
	if err != nil {
		return true, err
	}
	if !hasColumn {
		return true, nil
	}
	_, err = exec.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", quoteSQLiteIdentifier(table), quoteSQLiteIdentifier(column)))
	return true, err
}

func (s *SQLStore) execSQLiteAddColumnIfNotExists(ctx context.Context, exec migrationExecutor, statement string) (bool, error) {
	fields := strings.Fields(statement)
	if len(fields) < 9 ||
		!strings.EqualFold(fields[0], "ALTER") ||
		!strings.EqualFold(fields[1], "TABLE") ||
		!strings.EqualFold(fields[3], "ADD") ||
		!strings.EqualFold(fields[4], "COLUMN") ||
		!strings.EqualFold(fields[5], "IF") ||
		!strings.EqualFold(fields[6], "NOT") ||
		!strings.EqualFold(fields[7], "EXISTS") {
		return false, nil
	}
	table := trimSQLIdentifier(fields[2])
	column := trimSQLIdentifier(fields[8])
	hasColumn, err := sqliteColumnExists(ctx, exec, table, column)
	if err != nil {
		return true, err
	}
	if hasColumn {
		return true, nil
	}
	rest := strings.Join(fields[9:], " ")
	sql := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quoteSQLiteIdentifier(table), quoteSQLiteIdentifier(column))
	if rest != "" {
		sql += " " + rest
	}
	_, err = exec.ExecContext(ctx, sql)
	return true, err
}

func sqliteColumnExists(ctx context.Context, exec migrationExecutor, table, column string) (bool, error) {
	rows, err := exec.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLiteIdentifier(table)))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func trimSQLIdentifier(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	return strings.Trim(value, "`")
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *SQLStore) SaveTask(ctx context.Context, task *domain.Task) error {
	preCommands, err := encodeJSON(task.PreCommands)
	if err != nil {
		return err
	}
	postCommands, err := encodeJSON(task.PostCommands)
	if err != nil {
		return err
	}
	agentConfig, err := encodeJSON(task.AgentConfig)
	if err != nil {
		return err
	}
	startDate := storeTaskDisplayDate(task.StartDate)
	if startDate.IsZero() {
		startDate = storeTaskDisplayDate(task.CreatedAt)
	}
	endDate := storeTaskDisplayDate(task.EndDate)
	if endDate.IsZero() {
		endDate = startDate
	}
	_, err = s.db.ExecContext(ctx, s.upsertSQL(
		"tasks",
		[]string{"id", "title", "description", "status", "project_id", "worker_id", "agent_type", "agent_config", "base_branch", "worktree_path", "agent_session_id", "pre_commands", "post_commands", "result", "start_date", "end_date", "version", "created_at", "updated_at"},
		[]string{"title", "description", "status", "project_id", "worker_id", "agent_type", "agent_config", "base_branch", "worktree_path", "agent_session_id", "pre_commands", "post_commands", "result", "start_date", "end_date", "version", "created_at", "updated_at"},
	),
		task.ID,
		task.Title,
		task.Description,
		task.Status,
		task.ProjectID,
		nullableString(task.WorkerID),
		task.AgentType,
		agentConfig,
		task.BaseBranch,
		nullableString(task.WorktreePath),
		nullableString(task.AgentSessionID),
		preCommands,
		postCommands,
		nullableString(task.Result),
		startDate,
		endDate,
		task.Version,
		task.CreatedAt,
		task.UpdatedAt,
	)
	return err
}

func (s *SQLStore) Task(ctx context.Context, id string) (*domain.Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, title, description, status, project_id, worker_id, agent_type, agent_config, base_branch, worktree_path, agent_session_id, pre_commands, post_commands, result, start_date, end_date, version, created_at, updated_at FROM tasks WHERE id = `+s.bind(1), id)
	task, err := scanTask(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
		}
		return nil, err
	}
	return task, nil
}

func (s *SQLStore) Tasks(ctx context.Context) ([]*domain.Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, description, status, project_id, worker_id, agent_type, agent_config, base_branch, worktree_path, agent_session_id, pre_commands, post_commands, result, start_date, end_date, version, created_at, updated_at FROM tasks ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *SQLStore) SaveWorker(ctx context.Context, worker *domain.Worker) error {
	capabilities, err := encodeJSON(worker.Capabilities)
	if err != nil {
		return err
	}
	supportedAgents, err := encodeJSON(worker.SupportedAgents)
	if err != nil {
		return err
	}
	currentTaskIDs, err := encodeJSON(worker.CurrentTaskIDs)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, s.upsertSQL(
		"workers",
		[]string{"id", "name", "status", "capabilities", "supported_agents", "work_dir", "startup_command", "project_binding_mode", "current_task_ids", "last_heartbeat_at", "version", "created_at", "updated_at"},
		[]string{"name", "status", "capabilities", "supported_agents", "work_dir", "startup_command", "project_binding_mode", "current_task_ids", "last_heartbeat_at", "version", "created_at", "updated_at"},
	),
		worker.ID,
		worker.Name,
		worker.Status,
		capabilities,
		supportedAgents,
		worker.WorkDir,
		nullableString(worker.StartupCommand),
		worker.ProjectBindingMode,
		currentTaskIDs,
		nullableTime(worker.LastHeartbeatAt),
		worker.Version,
		worker.CreatedAt,
		worker.UpdatedAt,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM worker_project_bindings WHERE worker_id = `+s.bind(1), worker.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM worker_agent_env_vars WHERE worker_id = `+s.bind(1), worker.ID); err != nil {
		return err
	}
	if worker.ProjectBindingMode == domain.WorkerSpecificProjects {
		for _, projectID := range worker.BoundProjectIDs {
			if _, err := tx.ExecContext(ctx, s.insertIgnoreSQL("worker_project_bindings", []string{"worker_id", "project_id", "created_at"}), worker.ID, projectID, worker.UpdatedAt); err != nil {
				return err
			}
		}
	}
	for _, group := range worker.AgentRuntimeEnv {
		for _, item := range group.Vars {
			if _, err := tx.ExecContext(ctx, s.workerAgentEnvUpsertSQL(), worker.ID, group.AgentType, item.Key, item.Value, nullableString(item.Description), item.Enabled, item.Sensitive, worker.CreatedAt, worker.UpdatedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *SQLStore) Worker(ctx context.Context, id string) (*domain.Worker, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, status, capabilities, supported_agents, work_dir, startup_command, project_binding_mode, current_task_ids, last_heartbeat_at, version, created_at, updated_at FROM workers WHERE id = `+s.bind(1), id)
	worker, err := s.scanWorker(ctx, row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: worker %s", domain.ErrNotFound, id)
		}
		return nil, err
	}
	env, err := s.workerAgentRuntimeEnv(ctx, worker.ID)
	if err != nil {
		return nil, err
	}
	worker.AgentRuntimeEnv = env
	return worker, nil
}

func (s *SQLStore) Workers(ctx context.Context) ([]*domain.Worker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, status, capabilities, supported_agents, work_dir, startup_command, project_binding_mode, current_task_ids, last_heartbeat_at, version, created_at, updated_at FROM workers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Worker, 0)
	for rows.Next() {
		worker, err := scanWorkerFields(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, worker)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, worker := range out {
		boundIDs, err := s.boundProjectIDs(ctx, worker.ID)
		if err != nil {
			return nil, err
		}
		worker.BoundProjectIDs = boundIDs
		env, err := s.workerAgentRuntimeEnv(ctx, worker.ID)
		if err != nil {
			return nil, err
		}
		worker.AgentRuntimeEnv = env
	}
	return out, nil
}

func (s *SQLStore) DeleteWorker(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM worker_project_bindings WHERE worker_id = `+s.bind(1), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM worker_agent_env_vars WHERE worker_id = `+s.bind(1), id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM workers WHERE id = `+s.bind(1), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: worker %s", domain.ErrNotFound, id)
	}
	return tx.Commit()
}

func (s *SQLStore) SaveProject(ctx context.Context, project *domain.Project) error {
	_, err := s.db.ExecContext(ctx, s.upsertSQL(
		"projects",
		[]string{"id", "name", "git_url", "default_branch", "worktree_name_prefix", "archived", "version", "created_at", "updated_at"},
		[]string{"name", "git_url", "default_branch", "worktree_name_prefix", "archived", "version", "created_at", "updated_at"},
	),
		project.ID,
		project.Name,
		project.GitURL,
		project.DefaultBranch,
		project.WorktreeNamePrefix,
		project.Archived,
		project.Version,
		project.CreatedAt,
		project.UpdatedAt,
	)
	return err
}

func (s *SQLStore) Project(ctx context.Context, id string) (*domain.Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, git_url, default_branch, worktree_name_prefix, archived, version, created_at, updated_at FROM projects WHERE id = `+s.bind(1), id)
	project, err := scanProject(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: project %s", domain.ErrNotFound, id)
		}
		return nil, err
	}
	return project, nil
}

func (s *SQLStore) Projects(ctx context.Context) ([]*domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, git_url, default_branch, worktree_name_prefix, archived, version, created_at, updated_at FROM projects ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, project)
	}
	return out, rows.Err()
}

func (s *SQLStore) SaveSettings(ctx context.Context, settings *domain.Settings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, s.upsertSQL(
		"system_settings",
		[]string{"id", "worker_heartbeat_timeout", "security_policy", "version", "created_at", "updated_at"},
		[]string{"worker_heartbeat_timeout", "security_policy", "version", "updated_at"},
	), settings.ID, settings.WorkerHeartbeat, settings.SecurityPolicy, settings.Version, settings.CreatedAt, settings.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) Settings(ctx context.Context) (*domain.Settings, error) {
	settings := domain.NewSettings(time.Now().UTC())
	row := s.db.QueryRowContext(ctx, `SELECT id, worker_heartbeat_timeout, security_policy, version, created_at, updated_at FROM system_settings WHERE id = `+s.bind(1), "settings")
	if err := row.Scan(&settings.ID, &settings.WorkerHeartbeat, &settings.SecurityPolicy, &settings.Version, &settings.CreatedAt, &settings.UpdatedAt); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return settings, nil
}

func (s *SQLStore) AppendTaskLog(ctx context.Context, log domain.TaskLog) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO task_logs (id, task_id, stream, content, created_at) VALUES (`+s.bindList(1, 5)+`)`, log.ID, log.TaskID, log.Stream, log.Content, log.CreatedAt)
	return err
}

func (s *SQLStore) TaskLogs(ctx context.Context, taskID string) ([]domain.TaskLog, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, stream, content, created_at FROM task_logs WHERE task_id = `+s.bind(1)+` ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TaskLog, 0)
	for rows.Next() {
		var log domain.TaskLog
		if err := rows.Scan(&log.ID, &log.TaskID, &log.Stream, &log.Content, &log.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

func (s *SQLStore) AppendConversation(ctx context.Context, message domain.ConversationMessage) error {
	metadata, err := encodeJSON(message.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO task_conversations (id, task_id, role, content, metadata, created_at) VALUES (`+s.bindList(1, 6)+`)`, message.ID, message.TaskID, message.Role, message.Content, metadata, message.CreatedAt)
	return err
}

func (s *SQLStore) TaskConversations(ctx context.Context, taskID string) ([]domain.ConversationMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, role, content, metadata, created_at FROM task_conversations WHERE task_id = `+s.bind(1)+` ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ConversationMessage, 0)
	for rows.Next() {
		var message domain.ConversationMessage
		var metadata string
		if err := rows.Scan(&message.ID, &message.TaskID, &message.Role, &message.Content, &metadata, &message.CreatedAt); err != nil {
			return nil, err
		}
		if err := decodeJSON(metadata, &message.Metadata); err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

func (s *SQLStore) SaveTaskInteraction(ctx context.Context, interaction domain.TaskInteraction) error {
	_, err := s.db.ExecContext(ctx, s.upsertSQL(
		"task_interactions",
		[]string{"id", "task_id", "kind", "status", "title", "body", "raw_payload", "agent_session_id", "response_decision", "response_message", "response_payload", "created_at", "updated_at"},
		[]string{"task_id", "kind", "status", "title", "body", "raw_payload", "agent_session_id", "response_decision", "response_message", "response_payload", "updated_at"},
	),
		interaction.ID,
		interaction.TaskID,
		interaction.Kind,
		interaction.Status,
		interaction.Title,
		interaction.Body,
		interaction.RawPayload,
		nullableString(interaction.AgentSessionID),
		nullableString(string(interaction.ResponseDecision)),
		nullableString(interaction.ResponseMessage),
		interaction.ResponsePayload,
		interaction.CreatedAt,
		interaction.UpdatedAt,
	)
	return err
}

func (s *SQLStore) TaskInteraction(ctx context.Context, id string) (*domain.TaskInteraction, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, task_id, kind, status, title, body, raw_payload, agent_session_id, response_decision, response_message, response_payload, created_at, updated_at FROM task_interactions WHERE id = `+s.bind(1), id)
	return scanTaskInteraction(row)
}

func (s *SQLStore) TaskInteractions(ctx context.Context, taskID string, status domain.TaskInteractionStatus) ([]domain.TaskInteraction, error) {
	query := `SELECT id, task_id, kind, status, title, body, raw_payload, agent_session_id, response_decision, response_message, response_payload, created_at, updated_at FROM task_interactions WHERE task_id = ` + s.bind(1)
	args := []any{taskID}
	if status != "" {
		args = append(args, status)
		query += ` AND status = ` + s.bind(len(args))
	}
	query += ` ORDER BY created_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TaskInteraction, 0)
	for rows.Next() {
		interaction, err := scanTaskInteraction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *interaction)
	}
	return out, rows.Err()
}

func (s *SQLStore) CancelPendingTaskInteractions(ctx context.Context, taskID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE task_interactions SET status = `+s.bind(1)+`, response_decision = `+s.bind(2)+`, updated_at = `+s.bind(3)+` WHERE task_id = `+s.bind(4)+` AND status = `+s.bind(5),
		domain.TaskInteractionCanceled,
		domain.TaskInteractionCancel,
		now,
		taskID,
		domain.TaskInteractionPending,
	)
	return err
}

func (s *SQLStore) AppendEvents(ctx context.Context, events []domain.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, event := range events {
		payload := string(event.Payload)
		if _, err := tx.ExecContext(ctx, s.insertIgnoreSQL("domain_events", []string{"id", "event_type", "aggregate_type", "aggregate_id", "aggregate_version", "payload", "occurred_at", "correlation_id", "causation_id"}), event.EventID, event.EventType, event.AggregateType, event.AggregateID, event.AggregateVersion, payload, event.OccurredAt, nullableString(event.CorrelationID), nullableString(event.CausationID)); err != nil {
			return err
		}
		outboxID := "out_" + event.EventID
		if _, err := tx.ExecContext(ctx, s.insertIgnoreSQL("outbox_messages", []string{"id", "event_id", "event_type", "payload", "status", "created_at", "published_at"}), outboxID, event.EventID, event.EventType, payload, domain.OutboxPending, event.OccurredAt, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStore) DomainEvents(ctx context.Context, filter domain.EventFilter) ([]domain.DomainEvent, error) {
	query := `SELECT id, event_type, aggregate_type, aggregate_id, aggregate_version, payload, occurred_at, correlation_id, causation_id FROM domain_events`
	var args []any
	var where []string
	if filter.AggregateID != "" {
		args = append(args, filter.AggregateID)
		where = append(where, "aggregate_id = "+s.bind(len(args)))
	}
	if filter.AggregateType != "" {
		args = append(args, filter.AggregateType)
		where = append(where, "aggregate_type = "+s.bind(len(args)))
	}
	if filter.EventType != "" {
		args = append(args, filter.EventType)
		where = append(where, "event_type = "+s.bind(len(args)))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY occurred_at, id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.DomainEvent, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *SQLStore) OutboxMessages(ctx context.Context, includePublished bool) ([]domain.OutboxMessage, error) {
	query := `SELECT o.id, o.status, o.created_at, o.published_at, e.id, e.event_type, e.aggregate_type, e.aggregate_id, e.aggregate_version, e.payload, e.occurred_at, e.correlation_id, e.causation_id FROM outbox_messages o JOIN domain_events e ON e.id = o.event_id`
	var args []any
	if !includePublished {
		args = append(args, domain.OutboxPending)
		query += " WHERE o.status = " + s.bind(len(args))
	}
	query += " ORDER BY o.created_at, o.id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OutboxMessage, 0)
	for rows.Next() {
		message, err := scanOutboxMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

func (s *SQLStore) MarkOutboxPublished(ctx context.Context, ids []string, publishedAt time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, domain.OutboxPublished, publishedAt)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE outbox_messages SET status = `+s.bind(1)+`, published_at = `+s.bind(2)+` WHERE id IN (`+s.bindList(3, len(ids))+`)`, args...)
	return err
}

func (s *SQLStore) MarkMessageProcessed(ctx context.Context, messageID string) (bool, error) {
	if messageID == "" {
		return true, nil
	}
	result, err := s.db.ExecContext(ctx, s.insertIgnoreSQL("processed_worker_messages", []string{"message_id", "processed_at"}), messageID, time.Now().UTC())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (s *SQLStore) scanWorker(ctx context.Context, scanner interface{ Scan(...any) error }) (*domain.Worker, error) {
	worker, err := scanWorkerFields(scanner)
	if err != nil {
		return nil, err
	}
	boundIDs, err := s.boundProjectIDs(ctx, worker.ID)
	if err != nil {
		return nil, err
	}
	worker.BoundProjectIDs = boundIDs
	return worker, nil
}

func (s *SQLStore) boundProjectIDs(ctx context.Context, workerID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id FROM worker_project_bindings WHERE worker_id = `+s.bind(1)+` ORDER BY project_id`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SQLStore) workerAgentRuntimeEnv(ctx context.Context, workerID string) ([]domain.WorkerAgentRuntimeEnv, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT agent_type, key, value, description, enabled, sensitive FROM worker_agent_env_vars WHERE worker_id = `+s.bind(1)+` ORDER BY agent_type, key`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]domain.WorkerAgentRuntimeEnv, 0)
	groupIndex := map[domain.AgentType]int{}
	for rows.Next() {
		var agent domain.AgentType
		var item domain.AgentRuntimeEnvVar
		var description sql.NullString
		if err := rows.Scan(&agent, &item.Key, &item.Value, &description, &item.Enabled, &item.Sensitive); err != nil {
			return nil, err
		}
		item.Description = fromNullString(description)
		index, ok := groupIndex[agent]
		if !ok {
			index = len(groups)
			groupIndex[agent] = index
			groups = append(groups, domain.WorkerAgentRuntimeEnv{AgentType: agent})
		}
		groups[index].Vars = append(groups[index].Vars, item)
	}
	return groups, rows.Err()
}

func scanWorkerFields(scanner interface{ Scan(...any) error }) (*domain.Worker, error) {
	var worker domain.Worker
	var capabilities string
	var supportedAgents string
	var startupCommand sql.NullString
	var currentTaskIDs string
	var lastHeartbeatAt sql.NullTime
	if err := scanner.Scan(&worker.ID, &worker.Name, &worker.Status, &capabilities, &supportedAgents, &worker.WorkDir, &startupCommand, &worker.ProjectBindingMode, &currentTaskIDs, &lastHeartbeatAt, &worker.Version, &worker.CreatedAt, &worker.UpdatedAt); err != nil {
		return nil, err
	}
	if err := decodeJSON(capabilities, &worker.Capabilities); err != nil {
		return nil, err
	}
	if err := decodeJSON(supportedAgents, &worker.SupportedAgents); err != nil {
		return nil, err
	}
	worker.StartupCommand = fromNullString(startupCommand)
	if err := decodeJSON(currentTaskIDs, &worker.CurrentTaskIDs); err != nil {
		return nil, err
	}
	if lastHeartbeatAt.Valid {
		worker.LastHeartbeatAt = &lastHeartbeatAt.Time
	}
	return &worker, nil
}

func (s *SQLStore) upsertSQL(table string, columns, updateColumns []string) string {
	var builder strings.Builder
	builder.WriteString("INSERT INTO ")
	builder.WriteString(table)
	builder.WriteString(" (")
	builder.WriteString(strings.Join(columns, ", "))
	builder.WriteString(") VALUES (")
	builder.WriteString(s.bindList(1, len(columns)))
	builder.WriteString(") ON CONFLICT (")
	builder.WriteString(columns[0])
	builder.WriteString(") DO UPDATE SET ")
	for i, column := range updateColumns {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(column)
		builder.WriteString(" = excluded.")
		builder.WriteString(column)
	}
	return builder.String()
}

func (s *SQLStore) workerAgentEnvUpsertSQL() string {
	return `INSERT INTO worker_agent_env_vars (worker_id, agent_type, key, value, description, enabled, sensitive, created_at, updated_at) VALUES (` +
		s.bindList(1, 9) +
		`) ON CONFLICT (worker_id, agent_type, key) DO UPDATE SET value = EXCLUDED.value, description = EXCLUDED.description, enabled = EXCLUDED.enabled, sensitive = EXCLUDED.sensitive, updated_at = EXCLUDED.updated_at`
}

func (s *SQLStore) insertIgnoreSQL(table string, columns []string) string {
	return "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + s.bindList(1, len(columns)) + ") ON CONFLICT DO NOTHING"
}

func (s *SQLStore) bind(index int) string {
	if s.dialect == postgresDialect {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func (s *SQLStore) bindList(start, count int) string {
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, s.bind(start+i))
	}
	return strings.Join(out, ", ")
}

func scanTask(scanner interface{ Scan(...any) error }) (*domain.Task, error) {
	var task domain.Task
	var workerID sql.NullString
	var worktreePath sql.NullString
	var agentSessionID sql.NullString
	var result sql.NullString
	var startDate sql.NullTime
	var endDate sql.NullTime
	var preCommands string
	var postCommands string
	var agentConfig string
	if err := scanner.Scan(&task.ID, &task.Title, &task.Description, &task.Status, &task.ProjectID, &workerID, &task.AgentType, &agentConfig, &task.BaseBranch, &worktreePath, &agentSessionID, &preCommands, &postCommands, &result, &startDate, &endDate, &task.Version, &task.CreatedAt, &task.UpdatedAt); err != nil {
		return nil, err
	}
	task.WorkerID = fromNullString(workerID)
	task.WorktreePath = fromNullString(worktreePath)
	task.AgentSessionID = fromNullString(agentSessionID)
	task.Result = fromNullString(result)
	if startDate.Valid {
		task.StartDate = storeTaskDisplayDate(startDate.Time)
	}
	if task.StartDate.IsZero() {
		task.StartDate = storeTaskDisplayDate(task.CreatedAt)
	}
	if endDate.Valid {
		task.EndDate = storeTaskDisplayDate(endDate.Time)
	}
	if task.EndDate.IsZero() {
		task.EndDate = task.StartDate
	}
	if err := decodeJSON(preCommands, &task.PreCommands); err != nil {
		return nil, err
	}
	if err := decodeJSON(postCommands, &task.PostCommands); err != nil {
		return nil, err
	}
	if strings.TrimSpace(agentConfig) == "" {
		agentConfig = "{}"
	}
	if err := decodeJSON(agentConfig, &task.AgentConfig); err != nil {
		return nil, err
	}
	return &task, nil
}

func scanTaskInteraction(scanner interface{ Scan(...any) error }) (*domain.TaskInteraction, error) {
	var interaction domain.TaskInteraction
	var agentSessionID sql.NullString
	var responseDecision sql.NullString
	var responseMessage sql.NullString
	if err := scanner.Scan(
		&interaction.ID,
		&interaction.TaskID,
		&interaction.Kind,
		&interaction.Status,
		&interaction.Title,
		&interaction.Body,
		&interaction.RawPayload,
		&agentSessionID,
		&responseDecision,
		&responseMessage,
		&interaction.ResponsePayload,
		&interaction.CreatedAt,
		&interaction.UpdatedAt,
	); err != nil {
		return nil, err
	}
	interaction.AgentSessionID = fromNullString(agentSessionID)
	interaction.ResponseDecision = domain.TaskInteractionDecision(fromNullString(responseDecision))
	interaction.ResponseMessage = fromNullString(responseMessage)
	return &interaction, nil
}

func storeTaskDisplayDate(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	utc := value.UTC()
	year, month, day := utc.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func scanProject(scanner interface{ Scan(...any) error }) (*domain.Project, error) {
	var project domain.Project
	if err := scanner.Scan(&project.ID, &project.Name, &project.GitURL, &project.DefaultBranch, &project.WorktreeNamePrefix, &project.Archived, &project.Version, &project.CreatedAt, &project.UpdatedAt); err != nil {
		return nil, err
	}
	return &project, nil
}

func scanEvent(scanner interface{ Scan(...any) error }) (domain.DomainEvent, error) {
	var event domain.DomainEvent
	var payload string
	var correlationID sql.NullString
	var causationID sql.NullString
	if err := scanner.Scan(&event.EventID, &event.EventType, &event.AggregateType, &event.AggregateID, &event.AggregateVersion, &payload, &event.OccurredAt, &correlationID, &causationID); err != nil {
		return event, err
	}
	event.Payload = json.RawMessage(payload)
	event.CorrelationID = fromNullString(correlationID)
	event.CausationID = fromNullString(causationID)
	return event, nil
}

func scanOutboxMessage(scanner interface{ Scan(...any) error }) (domain.OutboxMessage, error) {
	var message domain.OutboxMessage
	var publishedAt sql.NullTime
	var event domain.DomainEvent
	var payload string
	var correlationID sql.NullString
	var causationID sql.NullString
	if err := scanner.Scan(&message.ID, &message.Status, &message.CreatedAt, &publishedAt, &event.EventID, &event.EventType, &event.AggregateType, &event.AggregateID, &event.AggregateVersion, &payload, &event.OccurredAt, &correlationID, &causationID); err != nil {
		return message, err
	}
	event.Payload = json.RawMessage(payload)
	event.CorrelationID = fromNullString(correlationID)
	event.CausationID = fromNullString(causationID)
	message.Event = event
	if publishedAt.Valid {
		message.PublishedAt = &publishedAt.Time
	}
	return message, nil
}

func encodeJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeJSON(data string, out any) error {
	if strings.TrimSpace(data) == "" {
		data = "null"
	}
	return json.Unmarshal([]byte(data), out)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func fromNullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}

func ensureSQLiteDir(dsn string) error {
	if dsn == ":memory:" || strings.HasPrefix(dsn, "file:") {
		return nil
	}
	dir := filepath.Dir(dsn)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
