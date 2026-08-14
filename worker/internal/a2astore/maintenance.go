package a2astore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

const runtimeStateFailed = "FAILED"

// CompactionStats 汇总一次 Worker 保留策略清理的结果。
type CompactionStats struct {
	Tasks    int
	Events   int64
	Commands int64
}

// StartupRecoveryStats 汇总进程重启后清理的无运行时持久化记录。
type StartupRecoveryStats struct {
	Commands int64
	Bindings int64
}

// RecoverStartup 回滚中断的 CONTINUE，并清理无法由新进程恢复的 pending command 与孤儿 binding。
// 参数：ctx 提供认证主体并控制单个恢复事务。
// 返回：删除的 command 和无法回滚的 binding 数量。
// 错误：认证、恢复记录冲突、SQLite 查询、更新、删除或事务提交失败时返回错误并回滚。
func (s *Store) RecoverStartup(ctx context.Context) (StartupRecoveryStats, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return StartupRecoveryStats{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StartupRecoveryStats{}, fmt.Errorf("开始 A2A 启动恢复事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := rollbackInterruptedContinuesTx(ctx, tx, owner, s.now().UTC()); err != nil {
		return StartupRecoveryStats{}, err
	}
	commandResult, err := tx.ExecContext(ctx, `
		DELETE FROM a2a_command_inbox
		WHERE owner = ? AND (
			task_id = '' OR NOT EXISTS (
				SELECT 1 FROM a2a_tasks
				WHERE a2a_tasks.owner = a2a_command_inbox.owner
					AND a2a_tasks.task_id = a2a_command_inbox.task_id
			)
		)
	`, owner)
	if err != nil {
		return StartupRecoveryStats{}, fmt.Errorf("清理 pending command: %w", err)
	}
	bindingResult, err := tx.ExecContext(ctx, `
		DELETE FROM a2a_runtime_bindings
		WHERE owner = ? AND NOT EXISTS (
			SELECT 1 FROM a2a_tasks
			WHERE a2a_tasks.owner = a2a_runtime_bindings.owner
				AND a2a_tasks.task_id = a2a_runtime_bindings.task_id
		)
	`, owner)
	if err != nil {
		return StartupRecoveryStats{}, fmt.Errorf("清理孤儿 runtime binding: %w", err)
	}
	commands, err := commandResult.RowsAffected()
	if err != nil {
		return StartupRecoveryStats{}, fmt.Errorf("读取 command 清理结果: %w", err)
	}
	bindings, err := bindingResult.RowsAffected()
	if err != nil {
		return StartupRecoveryStats{}, fmt.Errorf("读取 binding 清理结果: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StartupRecoveryStats{}, fmt.Errorf("提交 A2A 启动恢复事务: %w", err)
	}
	return StartupRecoveryStats{Commands: commands, Bindings: bindings}, nil
}

func rollbackInterruptedContinuesTx(ctx context.Context, tx *sql.Tx, owner string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT execution_id, local_task_id, worker_id, task_id, context_id, attempt, turn,
			agent_type, agent_session_id, worktree_path, state, last_sequence, updated_at
		FROM a2a_runtime_bindings AS binding
		WHERE owner = ? AND NOT EXISTS (
			SELECT 1 FROM a2a_tasks AS task
			WHERE task.owner = binding.owner AND task.task_id = binding.task_id
		)
		ORDER BY execution_id
	`, owner)
	if err != nil {
		return fmt.Errorf("查询中断 CONTINUE binding: %w", err)
	}
	var bindings []RuntimeBinding
	for rows.Next() {
		var binding RuntimeBinding
		var agentType string
		var updatedAt int64
		if err := rows.Scan(
			&binding.ExecutionID, &binding.LocalTaskID, &binding.WorkerID, &binding.TaskID,
			&binding.ContextID, &binding.Attempt, &binding.Turn, &agentType,
			&binding.AgentSessionID, &binding.WorktreePath, &binding.State,
			&binding.LastSequence, &updatedAt,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("扫描中断 CONTINUE binding: %w", err)
		}
		binding.Owner = owner
		binding.AgentType = a2aext.AgentType(agentType)
		binding.UpdatedAt = time.Unix(0, updatedAt).UTC()
		bindings = append(bindings, binding)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭中断 CONTINUE binding 查询: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历中断 CONTINUE binding: %w", err)
	}

	for _, binding := range bindings {
		request, ok, err := interruptedContinueRequestTx(ctx, tx, owner, binding)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		previous, err := previousTerminalTaskTx(ctx, tx, owner, binding, request)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE a2a_runtime_bindings
			SET task_id = ?, turn = ?, agent_session_id = ?, worktree_path = ?, state = ?, updated_at = ?
			WHERE owner = ? AND execution_id = ? AND task_id = ? AND context_id = ?
				AND local_task_id = ? AND worker_id = ? AND attempt = ? AND turn = ?
				AND agent_type = ? AND state = ? AND last_sequence = ?
		`, string(previous.ID), request.Scope.Turn-1, request.Resume.AgentSessionID,
			request.Resume.WorktreePath, normalizedState(string(previous.Status.State)), now.UnixNano(),
			owner, binding.ExecutionID, binding.TaskID, binding.ContextID, binding.LocalTaskID,
			binding.WorkerID, binding.Attempt, binding.Turn, string(binding.AgentType), binding.State,
			binding.LastSequence)
		if err != nil {
			return fmt.Errorf("回滚中断 CONTINUE binding: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取 CONTINUE binding 回滚结果: %w", err)
		}
		if changed != 1 {
			return taskstore.ErrConcurrentModification
		}
	}
	return nil
}

func interruptedContinueRequestTx(ctx context.Context, tx *sql.Tx, owner string, binding RuntimeBinding) (*a2aext.ExecutionRequest, bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT request_json
		FROM a2a_command_inbox
		WHERE owner = ? AND execution_id = ? AND task_id = ? AND context_id = ?
			AND length(request_json) > 0
		ORDER BY created_at DESC, command_id DESC
	`, owner, binding.ExecutionID, binding.TaskID, binding.ContextID)
	if err != nil {
		return nil, false, fmt.Errorf("查询中断 CONTINUE command: %w", err)
	}
	defer rows.Close()
	var selected *a2aext.ExecutionRequest
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, false, fmt.Errorf("扫描中断 CONTINUE command: %w", err)
		}
		request, err := a2aext.DecodeExecutionRequest(raw)
		if err != nil {
			return nil, false, fmt.Errorf("解码中断 CONTINUE command: %w", err)
		}
		if request.Command.Operation != a2aext.OperationContinue {
			continue
		}
		if err := a2aext.ValidateRequest(request, binding.WorkerID); err != nil {
			return nil, false, fmt.Errorf("校验中断 CONTINUE command: %w", err)
		}
		if request.Scope.ExecutionID != binding.ExecutionID || request.Scope.LocalTaskID != binding.LocalTaskID ||
			request.Scope.Attempt != binding.Attempt || request.Scope.Turn != binding.Turn ||
			request.Agent.Type != binding.AgentType || request.Resume == nil || binding.Turn <= 1 ||
			(binding.AgentSessionID != "" && request.Resume.AgentSessionID != binding.AgentSessionID) ||
			(binding.WorktreePath != "" && request.Resume.WorktreePath != binding.WorktreePath) {
			return nil, false, fmt.Errorf("中断 CONTINUE command 与 runtime binding 不一致: %w", ErrProtocolConflict)
		}
		if selected != nil {
			return nil, false, fmt.Errorf("同一缺失 Task 存在多个 CONTINUE command: %w", ErrProtocolConflict)
		}
		selected = request
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("遍历中断 CONTINUE command: %w", err)
	}
	return selected, selected != nil, nil
}

func previousTerminalTaskTx(ctx context.Context, tx *sql.Tx, owner string, binding RuntimeBinding, request *a2aext.ExecutionRequest) (*a2a.Task, error) {
	commandTaskIDs := map[string]struct{}{}
	rows, err := tx.QueryContext(ctx, `
		SELECT task_id, request_json
		FROM a2a_command_inbox
		WHERE owner = ? AND execution_id = ? AND context_id = ? AND task_id != ''
			AND task_id != ? AND length(request_json) > 0
	`, owner, binding.ExecutionID, binding.ContextID, binding.TaskID)
	if err != nil {
		return nil, fmt.Errorf("查询上一 turn command: %w", err)
	}
	for rows.Next() {
		var taskID string
		var raw []byte
		if err := rows.Scan(&taskID, &raw); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描上一 turn command: %w", err)
		}
		candidate, err := a2aext.DecodeExecutionRequest(raw)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("解码上一 turn command: %w", err)
		}
		if candidate.Scope.ExecutionID == binding.ExecutionID && candidate.Scope.LocalTaskID == binding.LocalTaskID &&
			candidate.Scope.ExpectedWorkerID == binding.WorkerID && candidate.Scope.Attempt == binding.Attempt &&
			candidate.Scope.Turn == request.Scope.Turn-1 && candidate.Agent.Type == binding.AgentType {
			commandTaskIDs[taskID] = struct{}{}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("关闭上一 turn command 查询: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历上一 turn command: %w", err)
	}

	// command 明细可能已按保留策略缩减，因此同时从终态 Task 的事件和 Artifact 恢复 turn 身份。
	taskRows, err := tx.QueryContext(ctx, `SELECT task_id, task_json FROM a2a_tasks WHERE owner = ?`, owner)
	if err != nil {
		return nil, fmt.Errorf("查询上一 turn Task: %w", err)
	}
	candidates := map[string]*a2a.Task{}
	for taskRows.Next() {
		var taskID string
		var raw []byte
		if err := taskRows.Scan(&taskID, &raw); err != nil {
			_ = taskRows.Close()
			return nil, fmt.Errorf("扫描上一 turn Task: %w", err)
		}
		task, err := decodeTask(raw)
		if err != nil {
			_ = taskRows.Close()
			return nil, err
		}
		if task.ContextID != binding.ContextID || !task.Status.State.Terminal() {
			continue
		}
		_, commandMatch := commandTaskIDs[taskID]
		turn, hasTurn, err := taskRetentionTurn(task)
		if err != nil {
			_ = taskRows.Close()
			return nil, err
		}
		metadataMatch := hasTurn && turn == (retentionTurn{executionID: binding.ExecutionID, turn: request.Scope.Turn - 1})
		if commandMatch || metadataMatch {
			candidates[taskID] = task
		}
	}
	if err := taskRows.Close(); err != nil {
		return nil, fmt.Errorf("关闭上一 turn Task 查询: %w", err)
	}
	if err := taskRows.Err(); err != nil {
		return nil, fmt.Errorf("遍历上一 turn Task: %w", err)
	}
	if len(candidates) != 1 {
		return nil, fmt.Errorf("上一 turn 终态 Task 数量为 %d: %w", len(candidates), ErrProtocolConflict)
	}
	for _, task := range candidates {
		return task, nil
	}
	panic("上一 turn Task 计数与集合不一致")
}

// FailNonTerminal 将当前主体的未终态 Task 原子标记为 Worker 重启失败。
// 参数：ctx 提供认证主体并控制事务。
// 返回：本次转为失败状态的 Task 数量。
// 错误：认证、Task 解码、journal 或 SQLite 更新失败时返回错误并回滚全部修改。
func (s *Store) FailNonTerminal(ctx context.Context) (int, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("开始 Worker 重启恢复事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapshots, err := listTaskSnapshots(ctx, tx, owner, "")
	if err != nil {
		return 0, err
	}
	now := s.now().UTC()
	failed := 0
	for _, snapshot := range snapshots {
		task, err := decodeTask(snapshot.raw)
		if err != nil {
			return 0, err
		}
		if task.Status.State.Terminal() {
			continue
		}
		binding, bindingErr := lookupBindingByTaskTx(ctx, tx, owner, string(task.ID))
		if errors.Is(bindingErr, sql.ErrNoRows) {
			// 崩溃可能发生在 SDK Task 落盘与 binding 提交之间，使用已脱敏 command 重建身份。
			binding, bindingErr = recoverRestartBindingTx(ctx, tx, owner, task, now)
		}
		if bindingErr != nil {
			return 0, bindingErr
		}
		diagnostic, terminal, err := restartFailureEvents(binding, now)
		if err != nil {
			return 0, err
		}
		if err := appendRestartEventTx(ctx, tx, owner, diagnostic, now); err != nil {
			return 0, err
		}
		if err := appendRestartEventTx(ctx, tx, owner, terminal, now); err != nil {
			return 0, err
		}
		diagnosticArtifact, err := restartArtifact(diagnostic, a2aext.ArtifactDiagnostic)
		if err != nil {
			return 0, err
		}
		terminalArtifact, err := restartArtifact(terminal, a2aext.ArtifactManifest)
		if err != nil {
			return 0, err
		}
		task.Artifacts = append(task.Artifacts, diagnosticArtifact, terminalArtifact)
		message := a2a.NewMessage(
			a2a.MessageRoleAgent,
			a2a.NewTextPart("Worker 已重启，内存运行时无法恢复"),
			a2a.NewDataPart(terminal),
		)
		message.TaskID = task.ID
		message.ContextID = task.ContextID
		message.Extensions = []string{a2aext.ExtensionURI}
		task.Status = a2a.TaskStatus{State: a2a.TaskStateFailed, Message: message, Timestamp: &now}
		raw, err := encodeTaskForStorage(task)
		if err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE a2a_tasks SET version = ?, task_json = ?, updated_at = ?
			WHERE owner = ? AND task_id = ? AND version = ?
		`, snapshot.version+1, raw, now.UnixNano(), owner, string(task.ID), snapshot.version)
		if err != nil {
			return 0, fmt.Errorf("标记重启 Task 失败: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("读取重启 Task 更新结果: %w", err)
		}
		if changed != 1 {
			return 0, taskstore.ErrConcurrentModification
		}
		failed++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交 Worker 重启恢复事务: %w", err)
	}
	return failed, nil
}

// CompactBefore 压缩当前主体在 cutoff 之前的终态 Task 与终态 turn 事件明细。
// 参数：ctx 提供认证主体，cutoff 是完整明细的保留边界。
// 返回：压缩 Task 数量和删除事件数量。
// 错误：cutoff 非法、认证、Task/事件解码或 SQLite 事务失败时返回错误并回滚。
func (s *Store) CompactBefore(ctx context.Context, cutoff time.Time) (CompactionStats, error) {
	if cutoff.IsZero() {
		return CompactionStats{}, errors.New("压缩截止时间不能为空")
	}
	owner, err := s.owner(ctx)
	if err != nil {
		return CompactionStats{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompactionStats{}, fmt.Errorf("开始 A2A 压缩事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapshots, err := listTaskSnapshots(ctx, tx, owner, `AND updated_at < ? AND compacted_at IS NULL`, cutoff.UTC().UnixNano())
	if err != nil {
		return CompactionStats{}, err
	}
	terminalTaskIDs, terminalTurns, err := retentionTargets(ctx, tx, owner)
	if err != nil {
		return CompactionStats{}, err
	}
	now := s.now().UTC()
	compactedTasks := 0
	for _, snapshot := range snapshots {
		task, err := decodeTask(snapshot.raw)
		if err != nil {
			return CompactionStats{}, err
		}
		if !task.Status.State.Terminal() {
			continue
		}
		if err := compactTask(task); err != nil {
			return CompactionStats{}, err
		}
		raw, err := encodeTaskForStorage(task)
		if err != nil {
			return CompactionStats{}, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE a2a_tasks
			SET version = ?, task_json = ?, updated_at = ?, compacted_at = ?
			WHERE owner = ? AND task_id = ? AND version = ? AND compacted_at IS NULL
		`, snapshot.version+1, raw, now.UnixNano(), now.UnixNano(), owner, string(task.ID), snapshot.version)
		if err != nil {
			return CompactionStats{}, fmt.Errorf("压缩 A2A Task: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return CompactionStats{}, fmt.Errorf("读取 Task 压缩结果: %w", err)
		}
		if changed != 1 {
			return CompactionStats{}, taskstore.ErrConcurrentModification
		}
		compactedTasks++
	}
	deletedEvents, err := deleteExpiredTerminalTurnEvents(ctx, tx, owner, cutoff.UTC(), terminalTurns)
	if err != nil {
		return CompactionStats{}, err
	}
	var compactedCommands int64
	for _, taskID := range terminalTaskIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE a2a_command_inbox SET request_json = ?
			WHERE owner = ? AND task_id = ? AND created_at < ? AND length(request_json) > 0
		`, []byte{}, owner, taskID, cutoff.UTC().UnixNano())
		if err != nil {
			return CompactionStats{}, fmt.Errorf("缩减过期 command 请求: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return CompactionStats{}, fmt.Errorf("读取 command 缩减结果: %w", err)
		}
		compactedCommands += changed
	}
	if err := tx.Commit(); err != nil {
		return CompactionStats{}, fmt.Errorf("提交 A2A 压缩事务: %w", err)
	}
	return CompactionStats{Tasks: compactedTasks, Events: deletedEvents, Commands: compactedCommands}, nil
}

type retentionTurn struct {
	executionID string
	turn        int
}

func retentionTargets(ctx context.Context, tx *sql.Tx, owner string) ([]string, map[retentionTurn]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `SELECT task_id, task_json FROM a2a_tasks WHERE owner = ?`, owner)
	if err != nil {
		return nil, nil, fmt.Errorf("查询保留策略 Task: %w", err)
	}
	terminalTasks := map[string]struct{}{}
	activeTasks := map[string]struct{}{}
	terminalTurns := map[retentionTurn]struct{}{}
	activeTurns := map[retentionTurn]struct{}{}
	for rows.Next() {
		var taskID string
		var raw []byte
		if err := rows.Scan(&taskID, &raw); err != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("扫描保留策略 Task: %w", err)
		}
		task, err := decodeTask(raw)
		if err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		turn, ok, err := taskRetentionTurn(task)
		if err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if task.Status.State.Terminal() {
			terminalTasks[taskID] = struct{}{}
			if ok {
				terminalTurns[turn] = struct{}{}
			}
		} else {
			activeTasks[taskID] = struct{}{}
			if ok {
				activeTurns[turn] = struct{}{}
			}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, fmt.Errorf("关闭保留策略 Task 查询: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("遍历保留策略 Task: %w", err)
	}
	taskIDs := make([]string, 0, len(terminalTasks))
	for taskID := range terminalTasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	bindingRows, err := tx.QueryContext(ctx, `
		SELECT execution_id, task_id, turn FROM a2a_runtime_bindings WHERE owner = ?
	`, owner)
	if err != nil {
		return nil, nil, fmt.Errorf("查询保留策略 binding: %w", err)
	}
	for bindingRows.Next() {
		var executionID, taskID string
		var turn int
		if err := bindingRows.Scan(&executionID, &taskID, &turn); err != nil {
			_ = bindingRows.Close()
			return nil, nil, fmt.Errorf("扫描保留策略 binding: %w", err)
		}
		if _, terminal := terminalTasks[taskID]; terminal {
			terminalTurns[retentionTurn{executionID: executionID, turn: turn}] = struct{}{}
		}
		if _, active := activeTasks[taskID]; active {
			activeTurns[retentionTurn{executionID: executionID, turn: turn}] = struct{}{}
		}
	}
	if err := bindingRows.Close(); err != nil {
		return nil, nil, fmt.Errorf("关闭保留策略 binding 查询: %w", err)
	}
	if err := bindingRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("遍历保留策略 binding: %w", err)
	}
	// 数据异常时宁可延后压缩，也不能删除仍由活跃 Task 引用的 turn 明细。
	for turn := range activeTurns {
		delete(terminalTurns, turn)
	}
	return taskIDs, terminalTurns, nil
}

func taskRetentionTurn(task *a2a.Task) (retentionTurn, bool, error) {
	if task == nil {
		return retentionTurn{}, false, nil
	}
	turns := map[retentionTurn]struct{}{}
	events, err := executionEventsFromTask(task)
	if err != nil {
		return retentionTurn{}, false, err
	}
	for _, event := range events {
		turns[retentionTurn{executionID: event.Scope.ExecutionID, turn: event.Scope.Turn}] = struct{}{}
	}
	for _, artifact := range task.Artifacts {
		metadata, _, ok, err := decodeArtifactMetadata(artifact)
		if err != nil {
			return retentionTurn{}, false, err
		}
		if ok {
			turns[retentionTurn{executionID: metadata.ExecutionID, turn: metadata.Turn}] = struct{}{}
		}
	}
	if len(turns) == 0 {
		return retentionTurn{}, false, nil
	}
	if len(turns) != 1 {
		return retentionTurn{}, false, fmt.Errorf("A2A Task 包含多个 execution/turn 身份: %w", ErrProtocolConflict)
	}
	for turn := range turns {
		return turn, true, nil
	}
	panic("Task turn 计数与集合不一致")
}

func deleteExpiredTerminalTurnEvents(ctx context.Context, tx *sql.Tx, owner string, cutoff time.Time, terminalTurns map[retentionTurn]struct{}) (int64, error) {
	if len(terminalTurns) == 0 {
		return 0, nil
	}
	const pageSize = 256
	type journalRow struct {
		executionID string
		eventID     string
		raw         []byte
		createdAt   int64
	}
	cutoffNanos := cutoff.UnixNano()
	var deleted int64
	var cursor journalRow
	firstPage := true
	for {
		var rows *sql.Rows
		var err error
		if firstPage {
			rows, err = tx.QueryContext(ctx, `
				SELECT execution_id, event_id, event_json, created_at
				FROM a2a_event_journal
				WHERE owner = ? AND created_at < ?
				ORDER BY created_at, execution_id, event_id
				LIMIT ?
			`, owner, cutoffNanos, pageSize)
		} else {
			rows, err = tx.QueryContext(ctx, `
				SELECT execution_id, event_id, event_json, created_at
				FROM a2a_event_journal
				WHERE owner = ? AND created_at < ? AND (
					created_at > ? OR
					(created_at = ? AND execution_id > ?) OR
					(created_at = ? AND execution_id = ? AND event_id > ?)
				)
				ORDER BY created_at, execution_id, event_id
				LIMIT ?
			`, owner, cutoffNanos, cursor.createdAt, cursor.createdAt, cursor.executionID,
				cursor.createdAt, cursor.executionID, cursor.eventID, pageSize)
		}
		if err != nil {
			return 0, fmt.Errorf("分页查询过期 event journal: %w", err)
		}
		page := make([]journalRow, 0, pageSize)
		for rows.Next() {
			var item journalRow
			if err := rows.Scan(&item.executionID, &item.eventID, &item.raw, &item.createdAt); err != nil {
				_ = rows.Close()
				return 0, fmt.Errorf("扫描过期 event journal: %w", err)
			}
			page = append(page, item)
		}
		if err := rows.Close(); err != nil {
			return 0, fmt.Errorf("关闭过期 event journal 查询: %w", err)
		}
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("遍历过期 event journal: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			event, err := a2aext.DecodeExecutionEvent(item.raw)
			if err != nil {
				return 0, fmt.Errorf("解码过期 event journal: %w", err)
			}
			if err := a2aext.ValidateEvent(event); err != nil {
				return 0, err
			}
			if event.Scope.ExecutionID != item.executionID || event.Event.ID != item.eventID {
				return 0, fmt.Errorf("event journal 身份与内容不一致: %w", ErrProtocolConflict)
			}
			if _, terminal := terminalTurns[retentionTurn{executionID: event.Scope.ExecutionID, turn: event.Scope.Turn}]; !terminal {
				continue
			}
			result, err := tx.ExecContext(ctx, `
				DELETE FROM a2a_event_journal
				WHERE owner = ? AND execution_id = ? AND event_id = ? AND created_at < ?
			`, owner, item.executionID, item.eventID, cutoffNanos)
			if err != nil {
				return 0, fmt.Errorf("删除终态 turn 过期事件: %w", err)
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return 0, fmt.Errorf("读取终态 turn 事件删除结果: %w", err)
			}
			if changed != 1 {
				return 0, taskstore.ErrConcurrentModification
			}
			deleted += changed
		}
		cursor = page[len(page)-1]
		firstPage = false
		if len(page) < pageSize {
			break
		}
	}
	return deleted, nil
}

type taskSnapshot struct {
	version int64
	raw     []byte
}

func listTaskSnapshots(ctx context.Context, tx *sql.Tx, owner, suffix string, args ...any) ([]taskSnapshot, error) {
	queryArgs := append([]any{owner}, args...)
	rows, err := tx.QueryContext(ctx, `
		SELECT version, task_json FROM a2a_tasks WHERE owner = ? `+suffix+`
	`, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("查询 A2A Task 快照: %w", err)
	}
	defer rows.Close()
	var snapshots []taskSnapshot
	for rows.Next() {
		var snapshot taskSnapshot
		if err := rows.Scan(&snapshot.version, &snapshot.raw); err != nil {
			return nil, fmt.Errorf("扫描 A2A Task 快照: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 A2A Task 快照: %w", err)
	}
	return snapshots, nil
}

func lookupBindingByTaskTx(ctx context.Context, tx *sql.Tx, owner, taskID string) (*RuntimeBinding, error) {
	var executionID string
	err := tx.QueryRowContext(ctx, `
		SELECT execution_id FROM a2a_runtime_bindings WHERE owner = ? AND task_id = ?
	`, owner, taskID).Scan(&executionID)
	if err != nil {
		return nil, err
	}
	return lookupBindingTx(ctx, tx, owner, executionID)
}

func recoverRestartBindingTx(ctx context.Context, tx *sql.Tx, owner string, task *a2a.Task, now time.Time) (*RuntimeBinding, error) {
	var raw []byte
	var contextID, executionID string
	err := tx.QueryRowContext(ctx, `
		SELECT request_json, context_id, execution_id
		FROM a2a_command_inbox
		WHERE owner = ? AND task_id = ? AND length(request_json) > 0
		ORDER BY created_at DESC, command_id DESC
		LIMIT 1
	`, owner, string(task.ID)).Scan(&raw, &contextID, &executionID)
	if err != nil {
		return nil, fmt.Errorf("恢复缺失的 runtime binding: %w", err)
	}
	request, err := a2aext.DecodeExecutionRequest(raw)
	if err != nil {
		return nil, fmt.Errorf("解码恢复 command: %w", err)
	}
	if err := a2aext.ValidateRequest(request, request.Scope.ExpectedWorkerID); err != nil {
		return nil, fmt.Errorf("校验恢复 command: %w", err)
	}
	if contextID != task.ContextID || executionID != request.Scope.ExecutionID {
		return nil, ErrProtocolConflict
	}
	binding := &RuntimeBinding{
		Owner: owner, ExecutionID: request.Scope.ExecutionID, LocalTaskID: request.Scope.LocalTaskID,
		WorkerID: request.Scope.ExpectedWorkerID, TaskID: string(task.ID), ContextID: task.ContextID,
		Attempt: request.Scope.Attempt, Turn: request.Scope.Turn, AgentType: request.Agent.Type,
		State: normalizedState(string(task.Status.State)), UpdatedAt: now,
	}
	if request.Resume != nil {
		binding.AgentSessionID = request.Resume.AgentSessionID
		binding.WorktreePath = request.Resume.WorktreePath
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO a2a_runtime_bindings(
			owner, execution_id, local_task_id, worker_id, task_id, context_id, attempt,
			turn, agent_type, agent_session_id, worktree_path, state, last_sequence, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)
	`, owner, binding.ExecutionID, binding.LocalTaskID, binding.WorkerID, binding.TaskID, binding.ContextID,
		binding.Attempt, binding.Turn, string(binding.AgentType), binding.AgentSessionID, binding.WorktreePath,
		binding.State, now.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("重建 runtime binding: %w", err)
	}
	return binding, nil
}

func restartFailureEvents(binding *RuntimeBinding, now time.Time) (*a2aext.ExecutionEvent, *a2aext.ExecutionEvent, error) {
	diagnostic, err := restartEvent(binding, binding.LastSequence+1, a2aext.EventExecutionDiagnostic, map[string]any{
		"errorCode": string(a2aext.ErrorWorkerRestarted),
		"message":   "Worker 已重启，内存运行时无法恢复",
		"retryable": false,
	}, now)
	if err != nil {
		return nil, nil, err
	}
	terminal, err := restartEvent(binding, diagnostic.Event.Sequence+1, a2aext.EventExecutionTerminal, map[string]any{
		"status":    string(a2aext.TerminalFailed),
		"errorCode": string(a2aext.ErrorWorkerRestarted),
		"message":   "Worker 已重启，内存运行时无法恢复",
	}, now)
	if err != nil {
		return nil, nil, err
	}
	return diagnostic, terminal, nil
}

func restartEvent(binding *RuntimeBinding, sequence int64, eventType a2aext.EventType, payload map[string]any, now time.Time) (*a2aext.ExecutionEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("生成重启 event ID: %w", err)
	}
	event := &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event: a2aext.EventHeader{
			ID:         eventID.String(),
			Sequence:   sequence,
			Type:       eventType,
			OccurredAt: now,
		},
		Scope: a2aext.EventScope{
			LocalTaskID: binding.LocalTaskID,
			ExecutionID: binding.ExecutionID,
			Attempt:     binding.Attempt,
			Turn:        binding.Turn,
			WorkerID:    binding.WorkerID,
		},
		Payload: payload,
	}
	if binding.AgentSessionID != "" || binding.WorktreePath != "" {
		event.Runtime = &a2aext.RuntimeInfo{
			AgentSessionID: binding.AgentSessionID,
			WorktreePath:   binding.WorktreePath,
		}
	}
	if err := a2aext.ValidateEvent(event); err != nil {
		return nil, err
	}
	return event, nil
}

func restartArtifact(event *a2aext.ExecutionEvent, role a2aext.ArtifactRole) (*a2a.Artifact, error) {
	metadata := a2aext.ArtifactMetadata{
		Kind: a2aext.ArtifactKind, Version: a2aext.Version, Role: role,
		ExecutionID: event.Scope.ExecutionID, Attempt: event.Scope.Attempt, Turn: event.Scope.Turn,
		EventID: event.Event.ID, Sequence: event.Event.Sequence, FinalChunk: true,
		MIMEType: "application/json", CreatedAt: event.Event.OccurredAt,
	}
	if err := a2aext.ValidateArtifact(&metadata); err != nil {
		return nil, err
	}
	return &a2a.Artifact{
		ID: a2a.ArtifactID(event.Event.ID), Name: string(role), Extensions: []string{a2aext.ExtensionURI},
		Metadata: map[string]any{a2aext.ExtensionURI: metadata},
		Parts:    a2a.ContentParts{a2a.NewDataPart(event)},
	}, nil
}

func appendRestartEventTx(ctx context.Context, tx *sql.Tx, owner string, event *a2aext.ExecutionEvent, now time.Time) error {
	if err := a2aext.ValidateEvent(event); err != nil {
		return err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("编码重启诊断事件: %w", err)
	}
	digest := sha256.Sum256(raw)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO a2a_event_journal(
			owner, execution_id, event_id, sequence, event_type, content_hash, event_json, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, owner, event.Scope.ExecutionID, event.Event.ID, event.Event.Sequence, string(event.Event.Type),
		hex.EncodeToString(digest[:]), raw, now.UnixNano())
	if err != nil {
		return fmt.Errorf("写入重启诊断事件: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE a2a_runtime_bindings
		SET state = ?, last_sequence = ?, updated_at = ?
		WHERE owner = ? AND execution_id = ? AND last_sequence = ?
	`, runtimeStateFailed, event.Event.Sequence, now.UnixNano(), owner, event.Scope.ExecutionID, event.Event.Sequence-1)
	if err != nil {
		return fmt.Errorf("更新重启 runtime binding: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取重启 runtime binding 结果: %w", err)
	}
	if changed != 1 {
		return taskstore.ErrConcurrentModification
	}
	return nil
}

func compactTask(task *a2a.Task) error {
	task.History = nil
	task.Metadata = nil
	type candidate struct {
		artifact *a2a.Artifact
		metadata a2aext.ArtifactMetadata
		nested   bool
		index    int
	}
	latest := map[a2aext.ArtifactRole]candidate{}
	for index, artifact := range task.Artifacts {
		metadata, nested, ok, err := decodeArtifactMetadata(artifact)
		if err != nil {
			return err
		}
		if !ok || (metadata.Role != a2aext.ArtifactManifest && metadata.Role != a2aext.ArtifactResult && metadata.Role != a2aext.ArtifactDiagnostic) {
			continue
		}
		current, exists := latest[metadata.Role]
		if !exists || metadata.Sequence > current.metadata.Sequence || (metadata.Sequence == current.metadata.Sequence && index > current.index) {
			latest[metadata.Role] = candidate{artifact: artifact, metadata: metadata, nested: nested, index: index}
		}
	}
	selected := make([]candidate, 0, len(latest))
	for _, item := range latest {
		selected = append(selected, item)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].index < selected[j].index })
	task.Artifacts = make([]*a2a.Artifact, 0, len(selected))
	for _, item := range selected {
		item.metadata.Compacted = true
		if err := setArtifactMetadata(item.artifact, item.metadata, item.nested); err != nil {
			return err
		}
		task.Artifacts = append(task.Artifacts, item.artifact)
	}
	return nil
}

func decodeArtifactMetadata(artifact *a2a.Artifact) (a2aext.ArtifactMetadata, bool, bool, error) {
	if artifact == nil || artifact.Metadata == nil {
		return a2aext.ArtifactMetadata{}, false, false, nil
	}
	if metadata, ok, err := decodeArtifactMetadataValue(artifact.Metadata); err != nil || ok {
		return metadata, false, ok, err
	}
	value, exists := artifact.Metadata[a2aext.ExtensionURI]
	if !exists {
		return a2aext.ArtifactMetadata{}, false, false, nil
	}
	metadata, ok, err := decodeArtifactMetadataValue(value)
	return metadata, true, ok, err
}

func decodeArtifactMetadataValue(value any) (a2aext.ArtifactMetadata, bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return a2aext.ArtifactMetadata{}, false, fmt.Errorf("编码 Artifact metadata: %w", err)
	}
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &header); err != nil || header.Kind != a2aext.ArtifactKind {
		return a2aext.ArtifactMetadata{}, false, nil
	}
	metadata, err := a2aext.DecodeArtifactMetadata(raw)
	if err != nil {
		return a2aext.ArtifactMetadata{}, false, fmt.Errorf("解码 Artifact metadata: %w", err)
	}
	if err := a2aext.ValidateArtifact(metadata); err != nil {
		return a2aext.ArtifactMetadata{}, false, err
	}
	return *metadata, true, nil
}

func setArtifactMetadata(artifact *a2a.Artifact, metadata a2aext.ArtifactMetadata, nested bool) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("编码压缩 Artifact metadata: %w", err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("解码压缩 Artifact metadata: %w", err)
	}
	if nested {
		artifact.Metadata[a2aext.ExtensionURI] = value
	} else {
		artifact.Metadata = value
	}
	return nil
}
