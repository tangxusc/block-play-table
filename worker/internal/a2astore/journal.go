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
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func syncTaskEventTx(ctx context.Context, tx *sql.Tx, owner string, task *a2a.Task, trigger a2a.Event, now time.Time) error {
	events, err := executionEvents(trigger)
	if err != nil {
		return err
	}
	if len(events) == 0 && trigger == task {
		events, err = executionEventsFromTask(task)
		if err != nil {
			return err
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Event.Sequence < events[j].Event.Sequence
	})
	for _, event := range events {
		if err := appendJournalEventTx(ctx, tx, owner, event, normalizedState(string(task.Status.State)), now); err != nil {
			return err
		}
	}
	return nil
}

func executionEvents(event a2a.Event) ([]*a2aext.ExecutionEvent, error) {
	switch value := event.(type) {
	case *a2a.Task:
		return executionEventsFromTask(value)
	case *a2a.TaskArtifactUpdateEvent:
		if value == nil || value.Artifact == nil {
			return nil, nil
		}
		return executionEventsFromParts(value.Artifact.Parts)
	case *a2a.TaskStatusUpdateEvent:
		if value == nil {
			return nil, nil
		}
		return executionEventsFromMessage(value.Status.Message)
	case *a2a.Message:
		return executionEventsFromMessage(value)
	case nil:
		return nil, nil
	default:
		return nil, nil
	}
}

func executionEventsFromTask(task *a2a.Task) ([]*a2aext.ExecutionEvent, error) {
	if task == nil {
		return nil, nil
	}
	result, err := executionEventsFromMessage(task.Status.Message)
	if err != nil {
		return nil, err
	}
	for _, artifact := range task.Artifacts {
		if artifact == nil {
			continue
		}
		events, err := executionEventsFromParts(artifact.Parts)
		if err != nil {
			return nil, err
		}
		result = append(result, events...)
	}
	return result, nil
}

func executionEventsFromMessage(message *a2a.Message) ([]*a2aext.ExecutionEvent, error) {
	if message == nil {
		return nil, nil
	}
	return executionEventsFromParts(message.Parts)
}

func executionEventsFromParts(parts a2a.ContentParts) ([]*a2aext.ExecutionEvent, error) {
	result := make([]*a2aext.ExecutionEvent, 0)
	for _, part := range parts {
		if part == nil || part.Data() == nil {
			continue
		}
		raw, err := json.Marshal(part.Data())
		if err != nil {
			return nil, fmt.Errorf("编码 execution event DataPart: %w", err)
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil || header.Kind != a2aext.EventKind {
			continue
		}
		event, err := a2aext.DecodeExecutionEvent(raw)
		if err != nil {
			return nil, fmt.Errorf("解码 execution event DataPart: %w", err)
		}
		if err := a2aext.ValidateEvent(event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, nil
}

func appendJournalEventTx(ctx context.Context, tx *sql.Tx, owner string, event *a2aext.ExecutionEvent, state string, now time.Time) error {
	if err := a2aext.ValidateEvent(event); err != nil {
		return err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("编码 execution event: %w", err)
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	var existingHash string
	err = tx.QueryRowContext(ctx, `
		SELECT content_hash FROM a2a_event_journal
		WHERE owner = ? AND execution_id = ? AND event_id = ?
	`, owner, event.Scope.ExecutionID, event.Event.ID).Scan(&existingHash)
	if err == nil {
		if existingHash != hash {
			return ErrProtocolConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("查询 execution event: %w", err)
	}
	var lastSequence int64
	err = tx.QueryRowContext(ctx, `
		SELECT last_sequence FROM a2a_runtime_bindings
		WHERE owner = ? AND execution_id = ?
	`, owner, event.Scope.ExecutionID).Scan(&lastSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("execution event 对应的 runtime binding 不存在")
	}
	if err != nil {
		return fmt.Errorf("读取 execution sequence: %w", err)
	}
	if event.Event.Sequence != lastSequence+1 {
		return ErrProtocolConflict
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO a2a_event_journal(
			owner, execution_id, event_id, sequence, event_type, content_hash, event_json, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, owner, event.Scope.ExecutionID, event.Event.ID, event.Event.Sequence, string(event.Event.Type), hash, raw, event.Event.OccurredAt.UnixNano())
	if err != nil {
		if isUniqueViolation(err) {
			return ErrProtocolConflict
		}
		return fmt.Errorf("写入 execution event: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE a2a_runtime_bindings SET state = ?, last_sequence = ?, updated_at = ?
		WHERE owner = ? AND execution_id = ? AND last_sequence = ?
	`, state, event.Event.Sequence, now.UnixNano(), owner, event.Scope.ExecutionID, lastSequence)
	if err != nil {
		return fmt.Errorf("推进 execution sequence: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取 execution sequence 更新结果: %w", err)
	}
	if changed != 1 {
		return taskstore.ErrConcurrentModification
	}
	return nil
}
