package a2astore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	_ "modernc.org/sqlite"
)

const defaultPageSize = 50

// ErrProtocolConflict 表示相同幂等标识对应了不同内容或序号。
var ErrProtocolConflict = errors.New("A2A 协议内容冲突")

// ErrCommandPending 表示相同 command 已被占用但尚未绑定 A2A Task。
var ErrCommandPending = errors.New("A2A command 正在创建 Task")

// Config 描述 SQLite Store 的文件、认证和时钟依赖。
type Config struct {
	Path          string
	Authenticator taskstore.Authenticator
	Now           func() time.Time
}

// Store 持久化 SDK Task 快照以及 Worker 扩展执行数据。
type Store struct {
	db            *sql.DB
	authenticator taskstore.Authenticator
	now           func() time.Time
}

var _ taskstore.Store = (*Store)(nil)

// Open 打开 SQLite 文件、应用 schema 并返回持久化 Store。
// 参数：ctx 控制初始化，config 提供数据库路径、认证器和时钟。
// 返回：可供 a2asrv.WithTaskStore 使用的 Store。
// 错误：路径为空、目录创建、数据库连接或 schema 初始化失败时返回错误。
func Open(ctx context.Context, config Config) (*Store, error) {
	if strings.TrimSpace(config.Path) == "" {
		return nil, errors.New("A2A SQLite 路径不能为空")
	}
	if config.Authenticator == nil {
		return nil, errors.New("A2A TaskStore 认证器不能为空")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
			return nil, fmt.Errorf("创建 A2A SQLite 目录: %w", err)
		}
	}
	db, err := sql.Open("sqlite", config.Path)
	if err != nil {
		return nil, fmt.Errorf("打开 A2A SQLite: %w", err)
	}
	// 单连接让 OCC 事务和内存数据库具有一致语义，也避免 SQLite 写锁抖动。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, authenticator: config.Authenticator, now: config.Now}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close 关闭 SQLite 连接。
// 参数：无。
// 返回：关闭成功返回 nil。
// 错误：底层数据库关闭失败时返回错误。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Create 新建 Task 快照并将 OCC version 初始化为 1。
// 参数：ctx 提供认证主体，task 为 SDK Task。
// 返回：创建后的 TaskVersion。
// 错误：Task 非法、认证失败、重复或 SQLite 写入失败时返回错误。
func (s *Store) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	if err := validateTask(task); err != nil {
		return taskstore.TaskVersionMissing, err
	}
	owner, err := s.owner(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	raw, err := encodeTaskForStorage(task)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("编码 A2A Task: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("开始创建 A2A Task 事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO a2a_tasks(owner, task_id, version, task_json, updated_at)
		VALUES(?, ?, 1, ?, ?)
	`, owner, string(task.ID), raw, s.now().UTC().UnixNano())
	if err != nil {
		if isUniqueViolation(err) {
			return taskstore.TaskVersionMissing, taskstore.ErrTaskAlreadyExists
		}
		return taskstore.TaskVersionMissing, fmt.Errorf("创建 A2A Task: %w", err)
	}
	if err := syncTaskEventTx(ctx, tx, owner, task, task, s.now().UTC()); err != nil {
		return taskstore.TaskVersionMissing, err
	}
	if err := tx.Commit(); err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("提交创建 A2A Task 事务: %w", err)
	}
	return taskstore.TaskVersion(1), nil
}

// Update 使用 PrevVersion 执行乐观并发更新。
// 参数：ctx 提供认证主体，request 包含新旧 Task、触发事件和期望 version。
// 返回：递增后的 TaskVersion。
// 错误：Task 不存在、所有者不匹配、版本冲突或 SQLite 写入失败时返回错误。
func (s *Store) Update(ctx context.Context, request *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	if request == nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("更新请求不能为空: %w", a2a.ErrInvalidParams)
	}
	if err := validateTask(request.Task); err != nil {
		return taskstore.TaskVersionMissing, err
	}
	// Worker 的 SQLite Store 始终启用版本追踪，缺失版本会绕过强 OCC。
	if request.PrevVersion == taskstore.TaskVersionMissing {
		return taskstore.TaskVersionMissing, taskstore.ErrConcurrentModification
	}
	owner, err := s.owner(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("开始 Task 更新事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT version FROM a2a_tasks WHERE owner = ? AND task_id = ?`, owner, string(request.Task.ID)).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return taskstore.TaskVersionMissing, a2a.ErrTaskNotFound
	}
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("读取 Task version: %w", err)
	}
	if int64(request.PrevVersion) != current {
		return taskstore.TaskVersionMissing, taskstore.ErrConcurrentModification
	}
	raw, err := encodeTaskForStorage(request.Task)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("编码 A2A Task: %w", err)
	}
	next := current + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE a2a_tasks SET version = ?, task_json = ?, updated_at = ?
		WHERE owner = ? AND task_id = ? AND version = ?
	`, next, raw, s.now().UTC().UnixNano(), owner, string(request.Task.ID), current)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("更新 A2A Task: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("读取 Task 更新结果: %w", err)
	}
	if changed != 1 {
		return taskstore.TaskVersionMissing, taskstore.ErrConcurrentModification
	}
	if err := syncTaskEventTx(ctx, tx, owner, request.Task, request.Event, s.now().UTC()); err != nil {
		return taskstore.TaskVersionMissing, err
	}
	if err := tx.Commit(); err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("提交 Task 更新事务: %w", err)
	}
	return taskstore.TaskVersion(next), nil
}

// Get 按认证主体读取 Task 快照和 OCC version。
// 参数：ctx 提供认证主体，taskID 为 A2A Task ID。
// 返回：深拷贝后的 StoredTask。
// 错误：Task 不存在、所有者不匹配、认证或解码失败时返回错误。
func (s *Store) Get(ctx context.Context, taskID a2a.TaskID) (*taskstore.StoredTask, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	var version int64
	var raw []byte
	err = s.db.QueryRowContext(ctx, `
		SELECT version, task_json FROM a2a_tasks WHERE owner = ? AND task_id = ?
	`, owner, string(taskID)).Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, a2a.ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取 A2A Task: %w", err)
	}
	task, err := decodeTask(raw)
	if err != nil {
		return nil, err
	}
	return &taskstore.StoredTask{Task: task, Version: taskstore.TaskVersion(version), User: owner}, nil
}

// List 按认证主体、context、状态和时间分页查询 Task。
// 参数：ctx 提供认证主体，request 提供筛选、分页和裁剪选项。
// 返回：符合 A2A ListTasksResponse 语义的结果。
// 错误：未认证、分页参数非法、游标非法或 Task 解码失败时返回错误。
func (s *Store) List(ctx context.Context, request *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("ListTasks 请求不能为空: %w", a2a.ErrInvalidParams)
	}
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	pageSize := request.PageSize
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, fmt.Errorf("pageSize 必须在 1 到 100 之间: %w", a2a.ErrInvalidRequest)
	}
	cursorTime, cursorID, err := decodeCursor(request.PageToken)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, task_json, updated_at FROM a2a_tasks
		WHERE owner = ? ORDER BY updated_at DESC, task_id DESC
	`, owner)
	if err != nil {
		return nil, fmt.Errorf("查询 A2A Task 列表: %w", err)
	}
	defer rows.Close()
	type candidate struct {
		task      *a2a.Task
		updatedAt int64
	}
	var filtered []candidate
	for rows.Next() {
		var taskID string
		var raw []byte
		var updatedAt int64
		if err := rows.Scan(&taskID, &raw, &updatedAt); err != nil {
			return nil, fmt.Errorf("扫描 A2A Task 列表: %w", err)
		}
		if cursorTime != 0 && (updatedAt > cursorTime || (updatedAt == cursorTime && taskID >= cursorID)) {
			continue
		}
		task, err := decodeTask(raw)
		if err != nil {
			return nil, err
		}
		if !matchesTask(task, request) {
			continue
		}
		filtered = append(filtered, candidate{task: task, updatedAt: updatedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 A2A Task 列表: %w", err)
	}
	total := len(filtered)
	nextToken := ""
	if len(filtered) > pageSize {
		last := filtered[pageSize-1]
		nextToken = encodeCursor(last.updatedAt, string(last.task.ID))
		filtered = filtered[:pageSize]
	}
	tasks := make([]*a2a.Task, 0, len(filtered))
	for _, item := range filtered {
		trimTask(item.task, request)
		tasks = append(tasks, item.task)
	}
	return &a2a.ListTasksResponse{Tasks: tasks, TotalSize: total, PageSize: pageSize, NextPageToken: nextToken}, nil
}

func (s *Store) owner(ctx context.Context) (string, error) {
	owner, err := s.authenticator(ctx)
	if err != nil || strings.TrimSpace(owner) == "" {
		return "", a2a.ErrUnauthenticated
	}
	return owner, nil
}
