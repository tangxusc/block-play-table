package a2astore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func (s *Store) initialize(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS a2a_tasks (
			owner TEXT NOT NULL,
			task_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			task_json BLOB NOT NULL,
			updated_at INTEGER NOT NULL,
			compacted_at INTEGER,
			PRIMARY KEY(owner, task_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_a2a_tasks_owner_updated ON a2a_tasks(owner, updated_at DESC, task_id DESC)`,
		`CREATE TABLE IF NOT EXISTS a2a_command_inbox (
			owner TEXT NOT NULL,
			command_id TEXT NOT NULL,
			payload_hash TEXT NOT NULL,
			task_id TEXT NOT NULL,
			context_id TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			request_json BLOB NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY(owner, command_id)
		)`,
		`CREATE TABLE IF NOT EXISTS a2a_runtime_bindings (
			owner TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			local_task_id TEXT NOT NULL,
			worker_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			context_id TEXT NOT NULL,
			attempt INTEGER NOT NULL,
			turn INTEGER NOT NULL,
			agent_type TEXT NOT NULL,
			agent_session_id TEXT NOT NULL DEFAULT '',
			worktree_path TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			last_sequence INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(owner, execution_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_a2a_bindings_task ON a2a_runtime_bindings(owner, task_id)`,
		`CREATE TABLE IF NOT EXISTS a2a_event_journal (
			owner TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			event_json BLOB NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY(owner, execution_id, event_id),
			UNIQUE(owner, execution_id, sequence)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_a2a_events_created ON a2a_event_journal(owner, created_at)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("初始化 A2A SQLite schema: %w", err)
		}
	}
	return nil
}

func validateTask(task *a2a.Task) error {
	if task == nil || task.ID == "" || task.ContextID == "" || task.Status.State == a2a.TaskStateUnspecified {
		return fmt.Errorf("A2A Task 缺少 id、contextId 或状态: %w", a2a.ErrInvalidParams)
	}
	return nil
}

func decodeTask(raw []byte) (*a2a.Task, error) {
	var task a2a.Task
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("解码 A2A Task: %w", err)
	}
	return &task, nil
}

// encodeTaskForStorage 创建 Task 深拷贝并脱敏 execution request DataPart。
// 参数：task 为 SDK 运行时 Task，敏感原值仍供当前 Runtime 使用。
// 返回：只包含可持久化数据的 JSON。
// 错误：Task 无法深拷贝或 request DataPart 无法规范化时返回错误。
func encodeTaskForStorage(task *a2a.Task) ([]byte, error) {
	raw, err := json.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("编码 A2A Task 副本: %w", err)
	}
	copy, err := decodeTask(raw)
	if err != nil {
		return nil, err
	}
	for _, message := range copy.History {
		if err := sanitizeMessage(message); err != nil {
			return nil, err
		}
	}
	if err := sanitizeMessage(copy.Status.Message); err != nil {
		return nil, err
	}
	for _, artifact := range copy.Artifacts {
		if artifact == nil {
			continue
		}
		if err := sanitizeParts(artifact.Parts); err != nil {
			return nil, err
		}
	}
	return json.Marshal(copy)
}

func sanitizeMessage(message *a2a.Message) error {
	if message == nil {
		return nil
	}
	return sanitizeParts(message.Parts)
}

func sanitizeParts(parts a2a.ContentParts) error {
	for _, part := range parts {
		if part == nil {
			continue
		}
		data, ok := part.Content.(a2a.Data)
		if !ok {
			continue
		}
		raw, err := json.Marshal(data.Value)
		if err != nil {
			return fmt.Errorf("编码 A2A DataPart: %w", err)
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &header); err != nil || header.Kind != a2aext.RequestKind {
			continue
		}
		request, err := a2aext.DecodeExecutionRequest(raw)
		if err != nil {
			return fmt.Errorf("解码 execution request DataPart: %w", err)
		}
		sanitized, err := a2aext.SanitizeRequest(request)
		if err != nil {
			return fmt.Errorf("脱敏 execution request DataPart: %w", err)
		}
		part.Content = a2a.Data{Value: sanitized}
	}
	return nil
}

func matchesTask(task *a2a.Task, request *a2a.ListTasksRequest) bool {
	if request.ContextID != "" && task.ContextID != request.ContextID {
		return false
	}
	if request.Status != a2a.TaskStateUnspecified && task.Status.State != request.Status {
		return false
	}
	if request.StatusTimestampAfter != nil && task.Status.Timestamp != nil && task.Status.Timestamp.Before(*request.StatusTimestampAfter) {
		return false
	}
	return true
}

func trimTask(task *a2a.Task, request *a2a.ListTasksRequest) {
	historyLength := 100
	if request.HistoryLength != nil {
		historyLength = *request.HistoryLength
	}
	if historyLength == 0 {
		task.History = []*a2a.Message{}
	} else if historyLength > 0 && len(task.History) > historyLength {
		task.History = task.History[len(task.History)-historyLength:]
	}
	if !request.IncludeArtifacts {
		task.Artifacts = nil
	}
}

func encodeCursor(updatedAt int64, taskID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(updatedAt, 10) + "|" + taskID))
}

func decodeCursor(token string) (int64, string, error) {
	if token == "" {
		return 0, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, "", a2a.ErrParseError
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, "", a2a.ErrParseError
	}
	updatedAt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || updatedAt <= 0 {
		return 0, "", a2a.ErrParseError
	}
	return updatedAt, parts[1], nil
}

func isUniqueViolation(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique constraint") || strings.Contains(text, "constraint failed")
}
