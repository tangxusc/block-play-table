package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

const (
	a2aRoundColumns  = `id, task_id, execution_id, attempt, turn, operation, worker_id, command_id, parent_round_id, a2a_task_id, context_id, remote_status, last_sequence, last_synced_at, last_network_at, unreachable_since, unknown_since, error_code, error_message, retryable, version, created_at, updated_at, completed_at`
	a2aIntentColumns = `id, round_id, task_id, execution_id, worker_id, command_id, operation, status, payload, attempt_count, available_at, last_attempt_at, sent_at, completed_at, error_code, error_message, version, created_at, updated_at`
)

// CommitA2ACommand 原子保存业务聚合、A2A round、下发意图和领域事件。
// 参数：ctx 用于取消事务，commit 描述完整写集合及其期望版本。
// 返回：提交成功时返回 nil。
// 错误：关联不一致、身份重复、OCC 冲突或数据库失败时返回错误。
func (s *SQLStore) CommitA2ACommand(ctx context.Context, commit A2ACommandCommit) error {
	if commit.Round == nil || commit.Intent == nil {
		return fmt.Errorf("a2a round and dispatch intent are required")
	}
	if err := validateA2AAssociation(commit.Round, commit.Intent); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := s.saveA2AAggregatesTx(ctx, tx, commit.Task, commit.ExpectedTaskVersion, commit.Worker, commit.ExpectedWorkerVersion); err != nil {
		return err
	}
	if commit.CreateRound {
		if !commit.Round.Operation.CreatesRound() || commit.Intent.CommandID != commit.Round.CommandID {
			return fmt.Errorf("%w: invalid round creation operation", domain.ErrConflict)
		}
		if err := s.insertA2ARound(ctx, tx, commit.Round); err != nil {
			return fmt.Errorf("%w: insert a2a round: %v", domain.ErrConflict, err)
		}
	} else {
		existing, err := s.a2aRoundByID(ctx, tx, commit.Round.ID)
		if err != nil {
			return err
		}
		if existing.TaskID != commit.Round.TaskID || existing.ExecutionID != commit.Round.ExecutionID || existing.WorkerID != commit.Round.WorkerID {
			return fmt.Errorf("%w: dispatch round association changed", domain.ErrConflict)
		}
		if commit.Intent.Operation.CreatesRound() {
			return fmt.Errorf("%w: round-creating operation requires a new round", domain.ErrConflict)
		}
	}
	if err := s.insertA2AIntent(ctx, tx, commit.Intent); err != nil {
		return fmt.Errorf("%w: insert a2a dispatch intent: %v", domain.ErrConflict, err)
	}
	if err := s.applyA2ACommonTx(ctx, tx, commit.Interaction, commit.Conversations, nil, commit.Events, commit.Task, commit.CancelPendingInteractions); err != nil {
		return err
	}
	return tx.Commit()
}

// CommitA2AProjection 原子登记远端事件并保存其全部业务投影。
// 参数：ctx 用于取消事务，commit 包含 inbox、OCC 快照和投影数据。
// 返回：首次处理返回 APPLIED，同内容重放返回 DUPLICATE。
// 错误：事件内容冲突、execution sequence 冲突、OCC 冲突或数据库失败时返回错误。
func (s *SQLStore) CommitA2AProjection(ctx context.Context, commit A2AProjectionCommit) (A2AEventDisposition, error) {
	if commit.Round == nil {
		return "", fmt.Errorf("a2a round is required")
	}
	if commit.Inbox != nil && (commit.Inbox.RoundID != commit.Round.ID || commit.Inbox.ExecutionID != commit.Round.ExecutionID) {
		return "", fmt.Errorf("%w: inbox association does not match round", domain.ErrConflict)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer rollback(tx)
	if commit.Inbox != nil {
		var existingHash string
		err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM a2a_event_inbox WHERE round_id = `+s.bind(1)+` AND event_id = `+s.bind(2), commit.Inbox.RoundID, commit.Inbox.EventID).Scan(&existingHash)
		if err == nil {
			if existingHash != commit.Inbox.PayloadHash {
				return "", fmt.Errorf("%w: event %s payload hash changed", domain.ErrConflict, commit.Inbox.EventID)
			}
			if commit.Intent != nil {
				// 首包并发竞态必须重载最新快照，不能把未完成 intent 当成已原子确认。
				return "", fmt.Errorf("%w: dispatch event was projected concurrently", ErrA2AConcurrentModification)
			}
			return A2AEventDuplicate, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
		var sequenceEventID string
		err = tx.QueryRowContext(ctx, `SELECT event_id FROM a2a_event_inbox WHERE execution_id = `+s.bind(1)+` AND event_sequence = `+s.bind(2), commit.Inbox.ExecutionID, commit.Inbox.Sequence).Scan(&sequenceEventID)
		if err == nil {
			return "", fmt.Errorf("%w: sequence %d already belongs to event %s", domain.ErrConflict, commit.Inbox.Sequence, sequenceEventID)
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}
	if err := s.saveA2AAggregatesTx(ctx, tx, commit.Task, commit.ExpectedTaskVersion, commit.Worker, commit.ExpectedWorkerVersion); err != nil {
		return "", err
	}
	if err := s.updateA2ARound(ctx, tx, commit.Round, commit.ExpectedRoundVersion); err != nil {
		return "", err
	}
	if commit.Intent != nil {
		if err := s.updateA2AIntent(ctx, tx, commit.Intent, commit.ExpectedIntentVersion); err != nil {
			return "", err
		}
	}
	if err := s.applyA2ACommonTx(ctx, tx, commit.Interaction, commit.Conversations, commit.Logs, commit.Events, commit.Task, commit.CancelPendingInteractions); err != nil {
		return "", err
	}
	if commit.Inbox != nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO a2a_event_inbox (round_id, execution_id, event_id, payload_hash, event_sequence, event_type, projection_status, projected_at, created_at) VALUES (`+s.bindList(1, 9)+`)`,
			commit.Inbox.RoundID, commit.Inbox.ExecutionID, commit.Inbox.EventID, commit.Inbox.PayloadHash, commit.Inbox.Sequence, commit.Inbox.EventType, commit.Inbox.ProjectionStatus, commit.Inbox.ProjectedAt, commit.Inbox.CreatedAt)
		if err != nil {
			return "", fmt.Errorf("%w: insert a2a event inbox: %v", ErrA2AConcurrentModification, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return A2AEventApplied, nil
}

// CommitA2AMigrationFailure 原子结束没有 A2A round 的旧活跃任务并释放 Worker。
// 参数：ctx 用于取消事务，commit 包含失败后的 Task、可选 Worker 和领域事件。
// 返回：实际迁移返回 true；任务已有 round 时返回 false。
// 错误：关联非法、聚合版本冲突或数据库写入失败时返回错误。
func (s *SQLStore) CommitA2AMigrationFailure(ctx context.Context, commit A2AMigrationCommit) (bool, error) {
	if commit.Task == nil {
		return false, fmt.Errorf("migration task is required")
	}
	if commit.Worker != nil && commit.Task.WorkerID != commit.Worker.ID {
		return false, fmt.Errorf("%w: migration worker does not match task", domain.ErrConflict)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	// 先锁定 Task，避免迁移与新的 A2A 命令同时决定控制权。
	if err := s.saveA2AAggregatesTx(ctx, tx, commit.Task, commit.ExpectedTaskVersion, commit.Worker, commit.ExpectedWorkerVersion); err != nil {
		return false, err
	}
	var marker int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM task_a2a_rounds WHERE task_id = `+s.bind(1)+` LIMIT 1`, commit.Task.ID).Scan(&marker)
	if err == nil {
		return false, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	if err := s.applyA2ACommonTx(ctx, tx, nil, nil, nil, commit.Events, commit.Task, commit.CancelPendingInteractions); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// A2AEventInbox 查询一个 round 内已投影事件的幂等证明。
// 参数：ctx 用于取消查询，roundID/eventID 是事件复合身份。
// 返回：找到时返回 inbox 快照。
// 错误：事件不存在或数据库查询失败时返回错误。
func (s *SQLStore) A2AEventInbox(ctx context.Context, roundID, eventID string) (*domain.A2AEventInbox, error) {
	var inbox domain.A2AEventInbox
	err := s.db.QueryRowContext(ctx, `SELECT round_id, execution_id, event_id, payload_hash, event_sequence, event_type, projection_status, projected_at, created_at FROM a2a_event_inbox WHERE round_id = `+s.bind(1)+` AND event_id = `+s.bind(2), roundID, eventID).Scan(
		&inbox.RoundID, &inbox.ExecutionID, &inbox.EventID, &inbox.PayloadHash, &inbox.Sequence, &inbox.EventType, &inbox.ProjectionStatus, &inbox.ProjectedAt, &inbox.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: a2a event %s", domain.ErrNotFound, eventID)
	}
	if err != nil {
		return nil, err
	}
	return &inbox, nil
}

// TaskA2ARounds 返回指定 Manager Task 的全部 A2A 执行轮次。
// 参数：ctx 用于取消查询，taskID 是 Manager Task ID。
// 返回：按 attempt、turn 和创建时间升序排列的 round。
// 错误：数据库查询或扫描失败时返回错误。
func (s *SQLStore) TaskA2ARounds(ctx context.Context, taskID string) ([]domain.TaskA2ARound, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+a2aRoundColumns+` FROM task_a2a_rounds WHERE task_id = `+s.bind(1)+` ORDER BY attempt, turn, created_at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanA2ARounds(rows)
}

// TaskA2ARound 按本地 round ID 查询 A2A 执行轮次。
// 参数：ctx 用于取消查询，id 是 round ID。
// 返回：找到时返回 round 快照。
// 错误：round 不存在或数据库查询失败时返回错误。
func (s *SQLStore) TaskA2ARound(ctx context.Context, id string) (*domain.TaskA2ARound, error) {
	return s.a2aRoundByID(ctx, s.db, id)
}

// LatestTaskA2ARound 返回任务最新的 A2A 执行轮次。
// 参数：ctx 用于取消查询，taskID 是 Manager Task ID。
// 返回：按 attempt、turn 和创建时间确定的最新 round。
// 错误：任务没有 round 或数据库查询失败时返回错误。
func (s *SQLStore) LatestTaskA2ARound(ctx context.Context, taskID string) (*domain.TaskA2ARound, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+a2aRoundColumns+` FROM task_a2a_rounds WHERE task_id = `+s.bind(1)+` ORDER BY attempt DESC, turn DESC, created_at DESC, id DESC LIMIT 1`, taskID)
	round, err := scanA2ARound(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: a2a round for task %s", domain.ErrNotFound, taskID)
	}
	return round, err
}

// A2ARoundsForReconcile 返回需要远端状态对账的非终态 round。
// 参数：ctx 用于取消查询。
// 返回：按更新时间升序排列的已绑定远端 Task round。
// 错误：数据库查询或扫描失败时返回错误。
func (s *SQLStore) A2ARoundsForReconcile(ctx context.Context) ([]domain.TaskA2ARound, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+a2aRoundColumns+` FROM task_a2a_rounds WHERE completed_at IS NULL AND a2a_task_id IS NOT NULL AND context_id IS NOT NULL ORDER BY updated_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanA2ARounds(rows)
}

// A2ADispatchIntentsDue 返回当前可领取或发送锁已超时的下发意图。
// 参数：ctx 用于取消查询，now 是领取基准时间，limit 是最大条数。
// 返回：按可用时间和创建时间排序的意图快照。
// 错误：数据库查询或扫描失败时返回错误。
func (s *SQLStore) A2ADispatchIntentsDue(ctx context.Context, now time.Time, limit int) ([]domain.TaskA2ADispatchIntent, error) {
	if limit <= 0 {
		limit = 100
	}
	staleSending := now.Add(-15 * time.Second)
	rows, err := s.db.QueryContext(ctx, `SELECT `+a2aIntentColumns+` FROM a2a_dispatch_intents WHERE (status = `+s.bind(1)+` AND available_at <= `+s.bind(2)+`) OR (status = `+s.bind(3)+` AND last_attempt_at <= `+s.bind(4)+`) ORDER BY available_at, created_at, id LIMIT `+s.bind(5),
		domain.A2ADispatchPending, now, domain.A2ADispatchSending, staleSending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TaskA2ADispatchIntent, 0)
	for rows.Next() {
		intent, err := scanA2AIntent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *intent)
	}
	return out, rows.Err()
}

// A2ADispatchIntent 按 ID 查询下发意图。
// 参数：ctx 用于取消查询，id 是 dispatch intent ID。
// 返回：找到时返回意图快照。
// 错误：意图不存在或数据库查询失败时返回错误。
func (s *SQLStore) A2ADispatchIntent(ctx context.Context, id string) (*domain.TaskA2ADispatchIntent, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+a2aIntentColumns+` FROM a2a_dispatch_intents WHERE id = `+s.bind(1), id)
	intent, err := scanA2AIntent(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: a2a dispatch intent %s", domain.ErrNotFound, id)
	}
	return intent, err
}

// UpdateA2ARound 以 OCC 方式更新 A2A round。
// 参数：ctx 用于取消更新，round 是新快照，expectedVersion 是数据库期望旧版本。
// 返回：更新成功时返回 nil。
// 错误：版本冲突、round 不存在或数据库失败时返回错误。
func (s *SQLStore) UpdateA2ARound(ctx context.Context, round *domain.TaskA2ARound, expectedVersion int) error {
	return s.updateA2ARound(ctx, s.db, round, expectedVersion)
}

// UpdateA2ADispatchIntent 以 OCC 方式更新下发意图。
// 参数：ctx 用于取消更新，intent 是新快照，expectedVersion 是数据库期望旧版本。
// 返回：更新成功时返回 nil。
// 错误：版本冲突、意图不存在或数据库失败时返回错误。
func (s *SQLStore) UpdateA2ADispatchIntent(ctx context.Context, intent *domain.TaskA2ADispatchIntent, expectedVersion int) error {
	return s.updateA2AIntent(ctx, s.db, intent, expectedVersion)
}

// CommitA2ADispatchResult 原子保存网络发送后的 round 绑定和意图状态。
// 参数：ctx 用于取消事务，round/intent 是新快照，expected 参数是数据库旧版本。
// 返回：提交成功时返回 nil。
// 错误：关联不一致、OCC 冲突或数据库失败时返回错误。
func (s *SQLStore) CommitA2ADispatchResult(ctx context.Context, round *domain.TaskA2ARound, expectedRoundVersion int, intent *domain.TaskA2ADispatchIntent, expectedIntentVersion int) error {
	if err := validateA2AAssociation(round, intent); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := s.updateA2ARound(ctx, tx, round, expectedRoundVersion); err != nil {
		return err
	}
	if err := s.updateA2AIntent(ctx, tx, intent, expectedIntentVersion); err != nil {
		return err
	}
	return tx.Commit()
}

type a2aSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLStore) saveA2AAggregatesTx(ctx context.Context, tx *sql.Tx, task *domain.Task, expectedTaskVersion int, worker *domain.Worker, expectedWorkerVersion int) error {
	if task != nil {
		if err := s.lockAggregateVersion(ctx, tx, "tasks", task.ID, expectedTaskVersion); err != nil {
			return err
		}
		if err := s.saveTask(ctx, tx, task); err != nil {
			return err
		}
	}
	if worker != nil {
		if err := s.lockAggregateVersion(ctx, tx, "workers", worker.ID, expectedWorkerVersion); err != nil {
			return err
		}
		if err := s.saveWorker(ctx, tx, worker); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStore) lockAggregateVersion(ctx context.Context, tx *sql.Tx, table, id string, expected int) error {
	query := `SELECT version FROM ` + table + ` WHERE id = ` + s.bind(1)
	if s.dialect == postgresDialect {
		query += ` FOR UPDATE`
	}
	var actual int
	if err := tx.QueryRowContext(ctx, query, id).Scan(&actual); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: %s %s", domain.ErrNotFound, table, id)
		}
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: %s %s expected version %d, got %d", ErrA2AConcurrentModification, table, id, expected, actual)
	}
	return nil
}

func (s *SQLStore) applyA2ACommonTx(ctx context.Context, tx *sql.Tx, interaction *domain.TaskInteraction, conversations []domain.ConversationMessage, logs []domain.TaskLog, events []domain.DomainEvent, task *domain.Task, cancelPending bool) error {
	if interaction != nil {
		if err := s.saveTaskInteraction(ctx, tx, *interaction); err != nil {
			return err
		}
	}
	for _, message := range conversations {
		if err := s.appendConversation(ctx, tx, message); err != nil {
			return err
		}
	}
	for _, log := range logs {
		if err := s.appendTaskLog(ctx, tx, log); err != nil {
			return err
		}
	}
	if cancelPending && task != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE task_interactions SET status = `+s.bind(1)+`, response_decision = `+s.bind(2)+`, updated_at = `+s.bind(3)+` WHERE task_id = `+s.bind(4)+` AND status = `+s.bind(5), domain.TaskInteractionCanceled, domain.TaskInteractionCancel, task.UpdatedAt, task.ID, domain.TaskInteractionPending); err != nil {
			return err
		}
	}
	return s.appendEventsTx(ctx, tx, events)
}

func validateA2AAssociation(round *domain.TaskA2ARound, intent *domain.TaskA2ADispatchIntent) error {
	if round == nil || intent == nil {
		return fmt.Errorf("a2a round and dispatch intent are required")
	}
	if round.ID != intent.RoundID || round.TaskID != intent.TaskID || round.ExecutionID != intent.ExecutionID || round.WorkerID != intent.WorkerID {
		return fmt.Errorf("%w: a2a round and intent identity mismatch", domain.ErrConflict)
	}
	return nil
}

func (s *SQLStore) insertA2ARound(ctx context.Context, exec sqlExecer, round *domain.TaskA2ARound) error {
	_, err := exec.ExecContext(ctx, `INSERT INTO task_a2a_rounds (`+a2aRoundColumns+`) VALUES (`+s.bindList(1, 24)+`)`,
		round.ID, round.TaskID, round.ExecutionID, round.Attempt, round.Turn, round.Operation, round.WorkerID, round.CommandID,
		nullableString(round.ParentRoundID), nullableString(round.A2ATaskID), nullableString(round.ContextID), round.RemoteStatus, round.LastSequence,
		nullableTime(round.LastSyncedAt), nullableTime(round.LastNetworkAt), nullableTime(round.UnreachableSince), nullableTime(round.UnknownSince), round.ErrorCode, round.ErrorMessage, round.Retryable,
		round.Version, round.CreatedAt, round.UpdatedAt, nullableTime(round.CompletedAt))
	return err
}

func (s *SQLStore) insertA2AIntent(ctx context.Context, exec sqlExecer, intent *domain.TaskA2ADispatchIntent) error {
	_, err := exec.ExecContext(ctx, `INSERT INTO a2a_dispatch_intents (`+a2aIntentColumns+`) VALUES (`+s.bindList(1, 19)+`)`,
		intent.ID, intent.RoundID, intent.TaskID, intent.ExecutionID, intent.WorkerID, intent.CommandID, intent.Operation, intent.Status, string(intent.Payload),
		intent.AttemptCount, intent.AvailableAt, nullableTime(intent.LastAttemptAt), nullableTime(intent.SentAt), nullableTime(intent.CompletedAt),
		intent.ErrorCode, intent.ErrorMessage, intent.Version, intent.CreatedAt, intent.UpdatedAt)
	return err
}

func (s *SQLStore) updateA2ARound(ctx context.Context, exec sqlExecer, round *domain.TaskA2ARound, expectedVersion int) error {
	if round == nil {
		return fmt.Errorf("a2a round is required")
	}
	result, err := exec.ExecContext(ctx, `UPDATE task_a2a_rounds SET a2a_task_id = `+s.bind(1)+`, context_id = `+s.bind(2)+`, remote_status = `+s.bind(3)+`, last_sequence = `+s.bind(4)+`, last_synced_at = `+s.bind(5)+`, last_network_at = `+s.bind(6)+`, unreachable_since = `+s.bind(7)+`, unknown_since = `+s.bind(8)+`, error_code = `+s.bind(9)+`, error_message = `+s.bind(10)+`, retryable = `+s.bind(11)+`, version = `+s.bind(12)+`, updated_at = `+s.bind(13)+`, completed_at = `+s.bind(14)+` WHERE id = `+s.bind(15)+` AND version = `+s.bind(16),
		nullableString(round.A2ATaskID), nullableString(round.ContextID), round.RemoteStatus, round.LastSequence, nullableTime(round.LastSyncedAt), nullableTime(round.LastNetworkAt), nullableTime(round.UnreachableSince), nullableTime(round.UnknownSince), round.ErrorCode, round.ErrorMessage, round.Retryable, round.Version, round.UpdatedAt, nullableTime(round.CompletedAt), round.ID, expectedVersion)
	if err != nil {
		return err
	}
	return requireA2AOCCRow(result, "round", round.ID)
}

func (s *SQLStore) updateA2AIntent(ctx context.Context, exec sqlExecer, intent *domain.TaskA2ADispatchIntent, expectedVersion int) error {
	if intent == nil {
		return fmt.Errorf("a2a dispatch intent is required")
	}
	result, err := exec.ExecContext(ctx, `UPDATE a2a_dispatch_intents SET status = `+s.bind(1)+`, payload = `+s.bind(2)+`, attempt_count = `+s.bind(3)+`, available_at = `+s.bind(4)+`, last_attempt_at = `+s.bind(5)+`, sent_at = `+s.bind(6)+`, completed_at = `+s.bind(7)+`, error_code = `+s.bind(8)+`, error_message = `+s.bind(9)+`, version = `+s.bind(10)+`, updated_at = `+s.bind(11)+` WHERE id = `+s.bind(12)+` AND version = `+s.bind(13),
		intent.Status, string(intent.Payload), intent.AttemptCount, intent.AvailableAt, nullableTime(intent.LastAttemptAt), nullableTime(intent.SentAt), nullableTime(intent.CompletedAt), intent.ErrorCode, intent.ErrorMessage, intent.Version, intent.UpdatedAt, intent.ID, expectedVersion)
	if err != nil {
		return err
	}
	return requireA2AOCCRow(result, "dispatch intent", intent.ID)
}

func requireA2AOCCRow(result sql.Result, kind, id string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: %s %s", ErrA2AConcurrentModification, kind, id)
	}
	return nil
}

func (s *SQLStore) a2aRoundByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (*domain.TaskA2ARound, error) {
	row := queryer.QueryRowContext(ctx, `SELECT `+a2aRoundColumns+` FROM task_a2a_rounds WHERE id = `+s.bind(1), id)
	round, err := scanA2ARound(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: a2a round %s", domain.ErrNotFound, id)
	}
	return round, err
}

func scanA2ARounds(rows *sql.Rows) ([]domain.TaskA2ARound, error) {
	out := make([]domain.TaskA2ARound, 0)
	for rows.Next() {
		round, err := scanA2ARound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *round)
	}
	return out, rows.Err()
}

func scanA2ARound(scanner interface{ Scan(...any) error }) (*domain.TaskA2ARound, error) {
	var round domain.TaskA2ARound
	var parentRoundID, a2aTaskID, contextID sql.NullString
	var lastSyncedAt, lastNetworkAt, unreachableSince, unknownSince, completedAt sql.NullTime
	if err := scanner.Scan(&round.ID, &round.TaskID, &round.ExecutionID, &round.Attempt, &round.Turn, &round.Operation, &round.WorkerID, &round.CommandID,
		&parentRoundID, &a2aTaskID, &contextID, &round.RemoteStatus, &round.LastSequence, &lastSyncedAt, &lastNetworkAt, &unreachableSince, &unknownSince,
		&round.ErrorCode, &round.ErrorMessage, &round.Retryable, &round.Version, &round.CreatedAt, &round.UpdatedAt, &completedAt); err != nil {
		return nil, err
	}
	round.ParentRoundID = fromNullString(parentRoundID)
	round.A2ATaskID = fromNullString(a2aTaskID)
	round.ContextID = fromNullString(contextID)
	round.LastSyncedAt = fromNullTime(lastSyncedAt)
	round.LastNetworkAt = fromNullTime(lastNetworkAt)
	round.UnreachableSince = fromNullTime(unreachableSince)
	round.UnknownSince = fromNullTime(unknownSince)
	round.CompletedAt = fromNullTime(completedAt)
	return &round, nil
}

func scanA2AIntent(scanner interface{ Scan(...any) error }) (*domain.TaskA2ADispatchIntent, error) {
	var intent domain.TaskA2ADispatchIntent
	var payload string
	var lastAttemptAt, sentAt, completedAt sql.NullTime
	if err := scanner.Scan(&intent.ID, &intent.RoundID, &intent.TaskID, &intent.ExecutionID, &intent.WorkerID, &intent.CommandID, &intent.Operation,
		&intent.Status, &payload, &intent.AttemptCount, &intent.AvailableAt, &lastAttemptAt, &sentAt, &completedAt, &intent.ErrorCode,
		&intent.ErrorMessage, &intent.Version, &intent.CreatedAt, &intent.UpdatedAt); err != nil {
		return nil, err
	}
	intent.Payload = []byte(payload)
	intent.LastAttemptAt = fromNullTime(lastAttemptAt)
	intent.SentAt = fromNullTime(sentAt)
	intent.CompletedAt = fromNullTime(completedAt)
	return &intent, nil
}

func fromNullTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
