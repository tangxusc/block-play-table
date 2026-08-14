package a2astore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

// CommandRecord 是 Worker command inbox 中的非敏感幂等记录；超过保留期后 Request 为 nil。
type CommandRecord struct {
	Owner       string
	CommandID   string
	PayloadHash string
	TaskID      string
	ContextID   string
	ExecutionID string
	Request     *a2aext.ExecutionRequest
	CreatedAt   time.Time
}

// ClaimCommand 原子声明 command ID，重复的相同请求返回既有记录。
// 参数：ctx 提供认证主体，request 为原始请求，taskID/contextID 为 SDK 分配的标识。
// 返回：持久化记录和 created 标志；created=false 表示幂等重放。
// 错误：请求无法脱敏或相同 command ID 的 payload 不同时返回错误。
func (s *Store) ClaimCommand(ctx context.Context, request *a2aext.ExecutionRequest, taskID, contextID string) (*CommandRecord, bool, error) {
	if taskID == "" || contextID == "" {
		return nil, false, errors.New("command、taskID 和 contextID 不能为空")
	}
	record, created, err := s.ReserveCommand(ctx, request)
	if err != nil {
		return nil, false, err
	}
	if !created {
		if record.TaskID == "" {
			return nil, false, ErrCommandPending
		}
		return record, false, nil
	}
	record, err = s.BindCommand(ctx, request, taskID, contextID)
	return record, true, err
}

// ReserveCommand 在 SDK 分配 Task ID 前原子占用语义 command ID。
// 参数：ctx 提供认证主体，request 为仍含运行时敏感值的原始请求。
// 返回：脱敏记录和 created 标志；created=false 表示相同请求已占用或完成。
// 错误：请求非法、内容冲突、认证或 SQLite 事务失败时返回错误。
func (s *Store) ReserveCommand(ctx context.Context, request *a2aext.ExecutionRequest) (*CommandRecord, bool, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, false, err
	}
	hash, sanitized, raw, err := commandPayload(request)
	if err != nil {
		return nil, false, err
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("开始 command reservation 事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := lookupCommandTx(ctx, tx, owner, request.Command.ID)
	if err == nil {
		if existing.PayloadHash != hash {
			return nil, false, ErrProtocolConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO a2a_command_inbox(
			owner, command_id, payload_hash, task_id, context_id, execution_id, request_json, created_at
		) VALUES(?, ?, ?, '', '', ?, ?, ?)
	`, owner, request.Command.ID, hash, request.Scope.ExecutionID, raw, now.UnixNano())
	if err != nil {
		if isUniqueViolation(err) {
			return nil, false, ErrCommandPending
		}
		return nil, false, fmt.Errorf("写入 command reservation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("提交 command reservation: %w", err)
	}
	return &CommandRecord{
		Owner: owner, CommandID: request.Command.ID, PayloadHash: hash,
		ExecutionID: request.Scope.ExecutionID, Request: sanitized, CreatedAt: now,
	}, true, nil
}

// BindCommand 将已占用 command 原子绑定到 SDK 分配的 Task/Context ID。
// 参数：ctx 提供认证主体，request 用于校验 reservation 内容，taskID/contextID 为 SDK 身份。
// 返回：已绑定的脱敏 command 记录。
// 错误：reservation 不存在、内容或既有绑定冲突、认证或 SQLite 更新失败时返回错误。
func (s *Store) BindCommand(ctx context.Context, request *a2aext.ExecutionRequest, taskID, contextID string) (*CommandRecord, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	if taskID == "" || contextID == "" {
		return nil, errors.New("绑定 command 的 taskID 和 contextID 不能为空")
	}
	hash, _, _, err := commandPayload(request)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始 command 绑定事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, err := lookupCommandTx(ctx, tx, owner, request.Command.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCommandPending
	}
	if err != nil {
		return nil, err
	}
	if record.PayloadHash != hash || (record.TaskID != "" && (record.TaskID != taskID || record.ContextID != contextID)) {
		return nil, ErrProtocolConflict
	}
	if record.TaskID == "" {
		result, err := tx.ExecContext(ctx, `
			UPDATE a2a_command_inbox SET task_id = ?, context_id = ?
			WHERE owner = ? AND command_id = ? AND payload_hash = ? AND task_id = ''
		`, taskID, contextID, owner, request.Command.ID, hash)
		if err != nil {
			return nil, fmt.Errorf("绑定 command Task: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("读取 command 绑定结果: %w", err)
		}
		if changed != 1 {
			return nil, ErrProtocolConflict
		}
		record.TaskID, record.ContextID = taskID, contextID
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交 command 绑定事务: %w", err)
	}
	return record, nil
}

// AbandonCommand 删除尚未绑定 Task 的失败 reservation。
// 参数：ctx 提供认证主体，request 用于确保只释放同一规范化内容。
// 返回：确实删除 reservation 时返回 true。
// 错误：请求非法、认证或 SQLite 删除失败时返回错误。
func (s *Store) AbandonCommand(ctx context.Context, request *a2aext.ExecutionRequest) (bool, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return false, err
	}
	hash, _, _, err := commandPayload(request)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM a2a_command_inbox
		WHERE owner = ? AND command_id = ? AND payload_hash = ? AND task_id = ''
	`, owner, request.Command.ID, hash)
	if err != nil {
		return false, fmt.Errorf("释放 command reservation: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

// AbandonBoundCommand 删除尚未产生业务更新的已绑定控制 command。
// 参数：ctx 提供认证主体，request 校验规范化 payload，taskID 限定原 A2A Task。
// 返回：CAS 删除成功时返回 true，记录已不存在时返回 false。
// 错误：请求非法、认证、内容冲突或 SQLite 删除失败时返回错误。
func (s *Store) AbandonBoundCommand(ctx context.Context, request *a2aext.ExecutionRequest, taskID string) (bool, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(taskID) == "" {
		return false, errors.New("已绑定 command 的 taskID 不能为空")
	}
	hash, _, _, err := commandPayload(request)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM a2a_command_inbox
		WHERE owner = ? AND command_id = ? AND payload_hash = ? AND task_id = ?
	`, owner, request.Command.ID, hash, taskID)
	if err != nil {
		return false, fmt.Errorf("释放已绑定 command: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取已绑定 command 删除结果: %w", err)
	}
	return changed == 1, nil
}

// AbandonFailedBoundCommand 释放首个 Task 明确创建失败后的绑定 command。
// 参数：ctx 提供认证主体，request 校验 payload，taskID 必须是本次 SDK 分配的 Task。
// 返回：Task 确认不存在且 CAS 删除成功时返回 true。
// 错误：请求非法、绑定冲突、认证或 SQLite 事务失败时返回错误；Task 已存在时安全返回 false。
func (s *Store) AbandonFailedBoundCommand(ctx context.Context, request *a2aext.ExecutionRequest, taskID string) (bool, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(taskID) == "" {
		return false, errors.New("失败 command 的 taskID 不能为空")
	}
	hash, _, _, err := commandPayload(request)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开始失败 command 清理事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, err := lookupCommandTx(ctx, tx, owner, request.Command.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.PayloadHash != hash || record.TaskID != taskID {
		return false, ErrProtocolConflict
	}
	var exists int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM a2a_tasks WHERE owner = ? AND task_id = ?
	`, owner, taskID).Scan(&exists)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("确认失败 command Task: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM a2a_command_inbox
		WHERE owner = ? AND command_id = ? AND payload_hash = ? AND task_id = ?
	`, owner, request.Command.ID, hash, taskID)
	if err != nil {
		return false, fmt.Errorf("删除失败 command: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取失败 command 删除结果: %w", err)
	}
	if changed != 1 {
		return false, ErrProtocolConflict
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM a2a_runtime_bindings
		WHERE owner = ? AND task_id = ? AND NOT EXISTS (
			SELECT 1 FROM a2a_tasks WHERE owner = ? AND task_id = ?
		)
	`, owner, taskID, owner, taskID)
	if err != nil {
		return false, fmt.Errorf("删除失败 command 孤儿 binding: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("提交失败 command 清理事务: %w", err)
	}
	return true, nil
}

// LookupCommand 按认证主体查询 command inbox。
// 参数：ctx 提供认证主体，commandID 为语义幂等键。
// 返回：已脱敏的 command 记录。
// 错误：记录不存在、认证失败或 SQLite 查询失败时返回错误。
func (s *Store) LookupCommand(ctx context.Context, commandID string) (*CommandRecord, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	record, err := lookupCommandQuery(ctx, s.db, owner, commandID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("command %s 不存在", commandID)
	}
	return record, err
}

// RuntimeBinding 保存 execution 与 A2A、CLI、worktree 的 Worker 权威关联。
type RuntimeBinding struct {
	Owner          string
	ExecutionID    string
	LocalTaskID    string
	WorkerID       string
	TaskID         string
	ContextID      string
	Attempt        int
	Turn           int
	AgentType      a2aext.AgentType
	AgentSessionID string
	WorktreePath   string
	State          string
	LastSequence   int64
	UpdatedAt      time.Time
}

// SaveBinding 新建或推进 execution runtime binding。
// 参数：ctx 提供认证主体，binding 为最新绑定；同 execution 的不可变字段必须一致。
// 返回：保存成功返回 nil。
// 错误：字段缺失、turn 回退、不可变字段冲突或 SQLite 写入失败时返回错误。
func (s *Store) SaveBinding(ctx context.Context, binding RuntimeBinding) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	if binding.ExecutionID == "" || binding.LocalTaskID == "" || binding.WorkerID == "" || binding.TaskID == "" ||
		binding.ContextID == "" || binding.Attempt < 1 || binding.Turn < 1 || binding.AgentType == "" {
		return errors.New("runtime binding 缺少必填字段")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始 runtime binding 事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := lookupBindingTx(ctx, tx, owner, binding.ExecutionID)
	if err == nil {
		if existing.LocalTaskID != binding.LocalTaskID || existing.WorkerID != binding.WorkerID ||
			existing.ContextID != binding.ContextID || existing.Attempt != binding.Attempt || existing.AgentType != binding.AgentType ||
			binding.Turn < existing.Turn {
			return ErrProtocolConflict
		}
		if binding.AgentSessionID == "" {
			binding.AgentSessionID = existing.AgentSessionID
		}
		if binding.WorktreePath == "" {
			binding.WorktreePath = existing.WorktreePath
		}
		binding.LastSequence = existing.LastSequence
		_, err = tx.ExecContext(ctx, `
			UPDATE a2a_runtime_bindings SET task_id = ?, turn = ?, agent_session_id = ?,
				worktree_path = ?, state = ?, updated_at = ?
			WHERE owner = ? AND execution_id = ?
		`, binding.TaskID, binding.Turn, binding.AgentSessionID, binding.WorktreePath,
			binding.State, s.now().UTC().UnixNano(), owner, binding.ExecutionID)
	} else if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO a2a_runtime_bindings(
				owner, execution_id, local_task_id, worker_id, task_id, context_id, attempt,
				turn, agent_type, agent_session_id, worktree_path, state, last_sequence, updated_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, owner, binding.ExecutionID, binding.LocalTaskID, binding.WorkerID, binding.TaskID,
			binding.ContextID, binding.Attempt, binding.Turn, string(binding.AgentType), binding.AgentSessionID,
			binding.WorktreePath, binding.State, binding.LastSequence, s.now().UTC().UnixNano())
	} else {
		return err
	}
	if err != nil {
		return fmt.Errorf("保存 runtime binding: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 runtime binding: %w", err)
	}
	return nil
}

// GetBinding 按认证主体读取 execution runtime binding。
// 参数：ctx 提供认证主体，executionID 为 Manager execution ID。
// 返回：Worker 权威绑定。
// 错误：绑定不存在、认证失败或 SQLite 查询失败时返回错误。
func (s *Store) GetBinding(ctx context.Context, executionID string) (*RuntimeBinding, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	binding, err := lookupBindingQuery(ctx, s.db, owner, executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("execution %s 的 runtime binding 不存在", executionID)
	}
	return binding, err
}

// GetBindingByTask 按当前 A2A Task ID 读取 execution runtime binding。
// 参数：ctx 提供认证主体，taskID 为 SDK Task ID。
// 返回：Task 当前对应的 Worker 权威绑定。
// 错误：绑定不存在、认证失败或 SQLite 查询失败时返回错误。
func (s *Store) GetBindingByTask(ctx context.Context, taskID string) (*RuntimeBinding, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	var executionID string
	err = s.db.QueryRowContext(ctx, `
		SELECT execution_id FROM a2a_runtime_bindings WHERE owner = ? AND task_id = ?
	`, owner, taskID).Scan(&executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("Task %s 的 runtime binding 不存在", taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询 Task runtime binding: %w", err)
	}
	return s.GetBinding(ctx, executionID)
}

// AppendEvent 以严格递增 sequence 写入 execution event journal。
// 参数：ctx 提供认证主体，event 为已脱敏事件。
// 返回：duplicate=true 表示相同 event ID 和内容已经存在。
// 错误：事件非法、sequence 有缺口、内容冲突或 SQLite 事务失败时返回错误。
func (s *Store) AppendEvent(ctx context.Context, event *a2aext.ExecutionEvent) (bool, error) {
	if err := a2aext.ValidateEvent(event); err != nil {
		return false, err
	}
	owner, err := s.owner(ctx)
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return false, fmt.Errorf("编码 execution event: %w", err)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开始 event journal 事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var existingHash string
	err = tx.QueryRowContext(ctx, `
		SELECT content_hash FROM a2a_event_journal
		WHERE owner = ? AND execution_id = ? AND event_id = ?
	`, owner, event.Scope.ExecutionID, event.Event.ID).Scan(&existingHash)
	if err == nil {
		if existingHash != hash {
			return false, ErrProtocolConflict
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("查询 event journal: %w", err)
	}
	var lastSequence int64
	err = tx.QueryRowContext(ctx, `
		SELECT last_sequence FROM a2a_runtime_bindings WHERE owner = ? AND execution_id = ?
	`, owner, event.Scope.ExecutionID).Scan(&lastSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("event 对应的 runtime binding 不存在")
	}
	if err != nil {
		return false, fmt.Errorf("读取 event sequence: %w", err)
	}
	if event.Event.Sequence != lastSequence+1 {
		return false, ErrProtocolConflict
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO a2a_event_journal(
			owner, execution_id, event_id, sequence, event_type, content_hash, event_json, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, owner, event.Scope.ExecutionID, event.Event.ID, event.Event.Sequence, string(event.Event.Type), hash, raw, event.Event.OccurredAt.UnixNano())
	if err != nil {
		if isUniqueViolation(err) {
			return false, ErrProtocolConflict
		}
		return false, fmt.Errorf("写入 event journal: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE a2a_runtime_bindings SET last_sequence = ?, updated_at = ?
		WHERE owner = ? AND execution_id = ? AND last_sequence = ?
	`, event.Event.Sequence, s.now().UTC().UnixNano(), owner, event.Scope.ExecutionID, lastSequence)
	if err != nil {
		return false, fmt.Errorf("更新 event sequence: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取 event sequence 更新结果: %w", err)
	}
	if changed != 1 {
		return false, taskstore.ErrConcurrentModification
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("提交 event journal: %w", err)
	}
	return false, nil
}

// ListEvents 返回 execution 在指定 sequence 之后的有序事件。
// 参数：ctx 提供认证主体，executionID 标识 execution，after 为已消费序号，limit 限制返回数量。
// 返回：按 sequence 递增的事件列表。
// 错误：limit 非法、认证、查询或解码失败时返回错误。
func (s *Store) ListEvents(ctx context.Context, executionID string, after int64, limit int) ([]*a2aext.ExecutionEvent, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("event limit 必须在 1 到 1000 之间")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_json FROM a2a_event_journal
		WHERE owner = ? AND execution_id = ? AND sequence > ?
		ORDER BY sequence ASC LIMIT ?
	`, owner, executionID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("查询 event journal: %w", err)
	}
	defer rows.Close()
	var events []*a2aext.ExecutionEvent
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("扫描 event journal: %w", err)
		}
		event, err := a2aext.DecodeExecutionEvent(raw)
		if err != nil {
			return nil, fmt.Errorf("解码 event journal: %w", err)
		}
		if err := a2aext.ValidateEvent(event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func lookupCommandQuery(ctx context.Context, q queryRower, owner, commandID string) (*CommandRecord, error) {
	var record CommandRecord
	var raw []byte
	var createdAt int64
	err := q.QueryRowContext(ctx, `
		SELECT payload_hash, task_id, context_id, execution_id, request_json, created_at
		FROM a2a_command_inbox WHERE owner = ? AND command_id = ?
	`, owner, commandID).Scan(&record.PayloadHash, &record.TaskID, &record.ContextID, &record.ExecutionID, &raw, &createdAt)
	if err != nil {
		return nil, err
	}
	var request *a2aext.ExecutionRequest
	if len(raw) > 0 {
		request, err = a2aext.DecodeExecutionRequest(raw)
		if err != nil {
			return nil, fmt.Errorf("解码 command inbox: %w", err)
		}
	}
	record.Owner, record.CommandID, record.Request = owner, commandID, request
	record.CreatedAt = time.Unix(0, createdAt).UTC()
	return &record, nil
}

func lookupCommandTx(ctx context.Context, tx *sql.Tx, owner, commandID string) (*CommandRecord, error) {
	return lookupCommandQuery(ctx, tx, owner, commandID)
}

func lookupBindingQuery(ctx context.Context, q queryRower, owner, executionID string) (*RuntimeBinding, error) {
	var binding RuntimeBinding
	var agentType string
	var updatedAt int64
	err := q.QueryRowContext(ctx, `
		SELECT local_task_id, worker_id, task_id, context_id, attempt, turn, agent_type,
			agent_session_id, worktree_path, state, last_sequence, updated_at
		FROM a2a_runtime_bindings WHERE owner = ? AND execution_id = ?
	`, owner, executionID).Scan(&binding.LocalTaskID, &binding.WorkerID, &binding.TaskID, &binding.ContextID,
		&binding.Attempt, &binding.Turn, &agentType, &binding.AgentSessionID, &binding.WorktreePath,
		&binding.State, &binding.LastSequence, &updatedAt)
	if err != nil {
		return nil, err
	}
	binding.Owner, binding.ExecutionID = owner, executionID
	binding.AgentType = a2aext.AgentType(agentType)
	binding.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return &binding, nil
}

func lookupBindingTx(ctx context.Context, tx *sql.Tx, owner, executionID string) (*RuntimeBinding, error) {
	return lookupBindingQuery(ctx, tx, owner, executionID)
}

func commandPayload(request *a2aext.ExecutionRequest) (string, *a2aext.ExecutionRequest, []byte, error) {
	if err := a2aext.ValidateRequest(request, ""); err != nil {
		return "", nil, nil, err
	}
	hash, err := a2aext.CanonicalPayloadHash(request)
	if err != nil {
		return "", nil, nil, err
	}
	sanitized, err := a2aext.SanitizeRequest(request)
	if err != nil {
		return "", nil, nil, err
	}
	raw, err := json.Marshal(sanitized)
	if err != nil {
		return "", nil, nil, fmt.Errorf("编码 command inbox 请求: %w", err)
	}
	return hash, sanitized, raw, nil
}

func normalizedState(state string) string {
	state = strings.TrimSpace(strings.ToUpper(state))
	return strings.TrimPrefix(state, "TASK_STATE_")
}
