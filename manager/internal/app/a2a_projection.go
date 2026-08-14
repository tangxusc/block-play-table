package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

// ErrA2ASequenceGap 表示实时流缺少 execution 中间事件，调用方必须先通过 Artifact 补齐。
var ErrA2ASequenceGap = errors.New("a2a execution sequence gap")

// ErrA2AProtocolConflict 表示远端 A2A 响应违反已协商协议，调用方必须终止 round。
var ErrA2AProtocolConflict = errors.New("a2a protocol conflict")

// ErrA2AProjection 表示远端更新尚未可靠写入本地投影，调用方应等待幂等重放。
var ErrA2AProjection = errors.New("a2a projection failed")

// ApplyA2ARemoteUpdate 原子投影标准 A2A 状态和可选 execution 事件。
// 参数：ctx 用于取消事务，roundID 选择本地 round，update 是 SDK 解码后的远端更新。
// 返回：投影或幂等重放成功时返回 nil。
// 错误：远端身份冲突、sequence 缺口、事件非法、业务状态冲突或 Store 提交失败时返回错误。
func (s *Service) ApplyA2ARemoteUpdate(ctx context.Context, roundID string, update A2ARemoteUpdate) error {
	return s.applyA2ARemoteUpdate(ctx, roundID, "", update)
}

// ApplyA2ADispatchUpdate 原子确认 intent、绑定远端身份并投影首条 A2A 更新。
// 参数：ctx 用于取消事务，intentID 选择正在发送的命令，update 是首条远端更新。
// 返回：投影或幂等重放成功时返回 nil。
// 错误：身份、事件或状态违反协议时返回 ErrA2AProtocolConflict；本地读写失败时返回 ErrA2AProjection。
func (s *Service) ApplyA2ADispatchUpdate(ctx context.Context, intentID string, update A2ARemoteUpdate) error {
	intent, err := s.store.A2ADispatchIntent(ctx, intentID)
	if err != nil {
		return a2aProjectionError(err)
	}
	return s.applyA2ARemoteUpdate(ctx, intent.RoundID, intentID, update)
}

func (s *Service) applyA2ARemoteUpdate(ctx context.Context, roundID, intentID string, update A2ARemoteUpdate) error {
	round, err := s.store.TaskA2ARound(ctx, roundID)
	if err != nil {
		return a2aProjectionError(err)
	}
	previousRound := *round
	if strings.TrimSpace(update.TaskID) == "" || strings.TrimSpace(update.ContextID) == "" {
		return a2aProtocolError(fmt.Errorf("%w: a2a remote task and context identity are required", domain.ErrConflict))
	}
	now := s.clock()
	expectedRoundVersion := round.Version
	var intent *domain.TaskA2ADispatchIntent
	expectedIntentVersion := 0
	if intentID != "" {
		intent, err = s.store.A2ADispatchIntent(ctx, intentID)
		if err != nil {
			return a2aProjectionError(err)
		}
		expectedIntentVersion = intent.Version
		if intent.RoundID != round.ID || intent.TaskID != round.TaskID || intent.ExecutionID != round.ExecutionID || intent.WorkerID != round.WorkerID {
			return a2aProtocolError(fmt.Errorf("%w: a2a dispatch association changed", domain.ErrConflict))
		}
	}
	if round.A2ATaskID == "" || round.ContextID == "" {
		if intent == nil {
			return a2aProtocolError(fmt.Errorf("%w: a2a round is not bound", domain.ErrConflict))
		}
		// 仅绑定身份，状态由下方统一观测，避免无状态 execution event 开启 UNKNOWN 窗口。
		if err := round.BindRemote(update.TaskID, update.ContextID, "", now); err != nil {
			return a2aProtocolError(err)
		}
	}
	if update.TaskID != round.A2ATaskID {
		return a2aProtocolError(fmt.Errorf("%w: a2a task id changed", domain.ErrConflict))
	}
	if update.ContextID != round.ContextID {
		return a2aProtocolError(fmt.Errorf("%w: a2a context id changed", domain.ErrConflict))
	}
	if err := validateA2ATerminalPair(round, update); err != nil {
		return a2aProtocolError(err)
	}
	var inbox *domain.A2AEventInbox
	duplicateEvent := false
	if update.Event != nil {
		if err := a2aext.ValidateEvent(update.Event); err != nil {
			return a2aProtocolError(fmt.Errorf("validate a2a execution event: %w", err))
		}
		hash, err := hashA2AEvent(update.Event)
		if err != nil {
			return a2aProtocolError(err)
		}
		existing, findErr := s.store.A2AEventInbox(ctx, round.ID, update.Event.Event.ID)
		if findErr == nil {
			if existing.PayloadHash != hash {
				return a2aProtocolError(fmt.Errorf("%w: event %s payload hash changed", domain.ErrConflict, update.Event.Event.ID))
			}
			duplicateEvent = true
			update.Sequence = round.LastSequence
		}
		if findErr != nil && !errors.Is(findErr, domain.ErrNotFound) {
			return a2aProjectionError(findErr)
		}
		if !duplicateEvent {
			if err := validateA2AEventScope(round, update.Event); err != nil {
				return a2aProtocolError(err)
			}
			if update.Event.Event.Sequence != round.LastSequence+1 {
				return a2aProtocolError(fmt.Errorf("%w: got %d after %d", ErrA2ASequenceGap, update.Event.Event.Sequence, round.LastSequence))
			}
			update.Sequence = update.Event.Event.Sequence
			inbox = &domain.A2AEventInbox{
				RoundID: round.ID, ExecutionID: round.ExecutionID, EventID: update.Event.Event.ID, PayloadHash: hash,
				Sequence: update.Event.Event.Sequence, EventType: string(update.Event.Event.Type), ProjectionStatus: domain.A2AProjectionProjected,
				ProjectedAt: now, CreatedAt: update.Event.Event.OccurredAt,
			}
		}
	} else {
		// 标准状态快照不能越过尚未通过 Artifact 投影的 execution 事件。
		if update.Sequence > round.LastSequence {
			return a2aProtocolError(fmt.Errorf("%w: snapshot reports %d after %d", ErrA2ASequenceGap, update.Sequence, round.LastSequence))
		}
		update.Sequence = round.LastSequence
	}
	statusEvent := update.Event
	if duplicateEvent {
		statusEvent = nil
	}
	statusObserved := update.Status != ""
	if statusObserved {
		if _, err := round.ApplyRemoteSnapshot(update.Status, update.Sequence, now); err != nil {
			return a2aProtocolError(err)
		}
	} else if _, err := round.ApplyRemoteEvent(update.Sequence, now); err != nil {
		return a2aProtocolError(err)
	}
	task, err := s.store.Task(ctx, round.TaskID)
	if err != nil {
		return a2aProjectionError(err)
	}
	expectedTaskVersion := task.Version
	projection := a2aBusinessProjection{}
	if update.Event != nil && !duplicateEvent {
		if err := s.projectA2AEvent(ctx, task, round, update.Event, &projection, now); err != nil {
			if errors.Is(err, ErrA2AProjection) {
				return err
			}
			return a2aProtocolError(err)
		}
	}
	if err := applyA2AStatus(task, round, update.Status, statusEvent, now); err != nil {
		return a2aProtocolError(err)
	}
	var worker *domain.Worker
	expectedWorkerVersion := 0
	cancelPending := false
	if isTaskTerminal(task.Status) {
		worker, err = s.store.Worker(ctx, round.WorkerID)
		if err != nil {
			return a2aProjectionError(err)
		}
		expectedWorkerVersion = worker.Version
		worker.ReleaseTask(task.ID, now)
		cancelPending = true
	}
	if a2aRoundPresentationChanged(previousRound, *round) {
		if err := task.RecordA2ARoundUpdated(round.ID, round.RemoteStatus, round.LastSequence, now); err != nil {
			return a2aProtocolError(err)
		}
	}
	events := task.PullEvents()
	if worker != nil {
		events = append(events, worker.PullEvents()...)
	}
	if intent != nil {
		intent.Complete(now)
	}
	disposition, err := s.store.CommitA2AProjection(ctx, store.A2AProjectionCommit{
		Inbox: inbox, Task: task, ExpectedTaskVersion: expectedTaskVersion, Worker: worker, ExpectedWorkerVersion: expectedWorkerVersion,
		Round: round, ExpectedRoundVersion: expectedRoundVersion, Intent: intent, ExpectedIntentVersion: expectedIntentVersion,
		Interaction: projection.interaction, Logs: projection.logs,
		Conversations: projection.conversations, Events: events, CancelPendingInteractions: cancelPending,
	})
	if err != nil {
		return a2aProjectionError(err)
	}
	if disposition == store.A2AEventApplied {
		s.publishEvents(events)
	}
	return nil
}

func a2aRoundPresentationChanged(before, after domain.TaskA2ARound) bool {
	return before.A2ATaskID != after.A2ATaskID ||
		before.ContextID != after.ContextID ||
		before.RemoteStatus != after.RemoteStatus ||
		before.LastSequence != after.LastSequence ||
		before.ErrorCode != after.ErrorCode ||
		before.ErrorMessage != after.ErrorMessage ||
		before.Retryable != after.Retryable ||
		!timePointersEqual(before.CompletedAt, after.CompletedAt)
}

func timePointersEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func a2aProtocolError(err error) error {
	if err == nil || errors.Is(err, ErrA2AProtocolConflict) {
		return err
	}
	return errors.Join(ErrA2AProtocolConflict, err)
}

func a2aProjectionError(err error) error {
	if err == nil || errors.Is(err, ErrA2AProjection) {
		return err
	}
	return errors.Join(ErrA2AProjection, err)
}

func (s *Service) failA2ARoundWithIntent(ctx context.Context, roundID, intentID, code, message string) error {
	round, err := s.store.TaskA2ARound(ctx, roundID)
	if err != nil {
		return err
	}
	if round.CompletedAt != nil && round.ErrorCode == code {
		return nil
	}
	now := s.clock()
	expectedRoundVersion := round.Version
	round.RemoteStatus = domain.TaskA2ARemoteStatusFailed
	round.SetError(code, message, now)
	task, err := s.store.Task(ctx, round.TaskID)
	if err != nil {
		return err
	}
	expectedTaskVersion := task.Version
	if !isTaskTerminal(task.Status) {
		if err := task.Fail(code+": "+message, now); err != nil {
			return err
		}
	}
	worker, err := s.store.Worker(ctx, round.WorkerID)
	if err != nil {
		return err
	}
	expectedWorkerVersion := worker.Version
	worker.ReleaseTask(task.ID, now)
	events := task.PullEvents()
	events = append(events, worker.PullEvents()...)
	var intent *domain.TaskA2ADispatchIntent
	expectedIntentVersion := 0
	if intentID != "" {
		intent, err = s.store.A2ADispatchIntent(ctx, intentID)
		if err != nil {
			return err
		}
		expectedIntentVersion = intent.Version
		intent.Fail(code, message, now)
	}
	_, err = s.store.CommitA2AProjection(ctx, store.A2AProjectionCommit{
		Task: task, ExpectedTaskVersion: expectedTaskVersion, Worker: worker, ExpectedWorkerVersion: expectedWorkerVersion,
		Round: round, ExpectedRoundVersion: expectedRoundVersion, Intent: intent, ExpectedIntentVersion: expectedIntentVersion,
		Events: events, CancelPendingInteractions: true,
	})
	if err != nil {
		return err
	}
	s.publishEvents(events)
	return nil
}

type a2aBusinessProjection struct {
	interaction   *domain.TaskInteraction
	logs          []domain.TaskLog
	conversations []domain.ConversationMessage
}

func (s *Service) projectA2AEvent(ctx context.Context, task *domain.Task, round *domain.TaskA2ARound, event *a2aext.ExecutionEvent, projection *a2aBusinessProjection, now time.Time) error {
	runtime := event.Runtime
	sessionID := ""
	if runtime != nil {
		sessionID = runtime.AgentSessionID
	}
	switch event.Event.Type {
	case a2aext.EventExecutionAccepted:
		return nil
	case a2aext.EventWorkspaceReady:
		if runtime == nil || strings.TrimSpace(runtime.WorktreePath) == "" {
			return fmt.Errorf("%w: workspace.ready lacks worktreePath", domain.ErrConflict)
		}
		return task.MarkRunning(runtime.WorktreePath, now)
	case a2aext.EventAgentSessionStarted, a2aext.EventAgentSessionUpdated:
		if strings.TrimSpace(sessionID) == "" {
			return fmt.Errorf("%w: agent session event lacks agentSessionId", domain.ErrConflict)
		}
		task.RememberAgentSession(sessionID, now)
		return nil
	case a2aext.EventLogChunk:
		stream := payloadString(event.Payload, "stream")
		content := payloadString(event.Payload, "content", "text")
		if stream == "" {
			stream = string(a2aext.LogSystem)
		}
		if err := task.AppendLog(stream, content, now); err != nil {
			return err
		}
		projection.logs = append(projection.logs, domain.TaskLog{ID: "log_" + round.ID + "_" + event.Event.ID, TaskID: task.ID, Stream: stream, Content: content, CreatedAt: event.Event.OccurredAt})
		return nil
	case a2aext.EventConversationMessage:
		role := payloadString(event.Payload, "role")
		if role == "" {
			role = "assistant"
		}
		content := payloadString(event.Payload, "content", "text")
		if err := task.AppendConversation(role, content, now); err != nil {
			return err
		}
		projection.conversations = append(projection.conversations, domain.ConversationMessage{ID: "msg_" + round.ID + "_" + event.Event.ID, TaskID: task.ID, Role: role, Content: content, CreatedAt: event.Event.OccurredAt})
		return nil
	case a2aext.EventInteractionRequested:
		interactionID := payloadString(event.Payload, "interactionId", "id")
		kind := domain.TaskInteractionKind(payloadString(event.Payload, "kind"))
		if interactionID == "" || !kind.Valid() {
			return fmt.Errorf("%w: invalid interaction.requested payload", domain.ErrConflict)
		}
		if existing, findErr := s.store.TaskInteraction(ctx, interactionID); findErr == nil {
			return fmt.Errorf("%w: interaction %s already belongs to task %s", domain.ErrConflict, interactionID, existing.TaskID)
		} else if !errors.Is(findErr, domain.ErrNotFound) {
			return a2aProjectionError(findErr)
		}
		interaction, err := domain.NewTaskInteraction(domain.TaskInteraction{
			ID: interactionID, TaskID: task.ID, Kind: kind, Status: domain.TaskInteractionPending,
			Title: payloadString(event.Payload, "title"), Body: payloadString(event.Payload, "body", "message"),
			RawPayload: payloadJSON(event.Payload["rawPayload"]), AgentSessionID: sessionID, CreatedAt: event.Event.OccurredAt, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := task.RequestInteraction(interaction.ID, interaction.Kind, interaction.Title, now, sessionID); err != nil {
			return err
		}
		projection.interaction = interaction
		return nil
	case a2aext.EventInteractionResolved:
		interactionID := payloadString(event.Payload, "interactionId", "id")
		interaction, err := s.store.TaskInteraction(ctx, interactionID)
		if err != nil {
			return a2aProjectionError(err)
		}
		if interaction.TaskID != task.ID {
			return fmt.Errorf("%w: interaction %s belongs to task %s", domain.ErrConflict, interactionID, interaction.TaskID)
		}
		interaction.Status = domain.TaskInteractionAnswered
		interaction.UpdatedAt = now
		projection.interaction = interaction
		if err := task.RecordInteractionResolved(interactionID, now); err != nil {
			return err
		}
		if task.Status == domain.TaskWaitingInput {
			return task.Resume(now)
		}
		return nil
	case a2aext.EventResultUpdated:
		return task.RecordResult(payloadString(event.Payload, "result", "content"), now, sessionID)
	case a2aext.EventExecutionDiagnostic:
		code, message := updateA2ARoundDiagnostic(round, event)
		content := formatA2ADiagnostic(code, message)
		if err := task.AppendLog("system", content, now); err != nil {
			return err
		}
		projection.logs = append(projection.logs, domain.TaskLog{ID: "log_" + round.ID + "_" + event.Event.ID, TaskID: task.ID, Stream: "system", Content: content, CreatedAt: event.Event.OccurredAt})
		return nil
	case a2aext.EventExecutionTerminal:
		if terminalStatus := statusFromTerminalPayload(event); terminalStatus == domain.TaskA2ARemoteStatusFailed || terminalStatus == domain.TaskA2ARemoteStatusRejected {
			updateA2ARoundDiagnostic(round, event)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported execution event %s", domain.ErrConflict, event.Event.Type)
	}
}

func validateA2ATerminalPair(round *domain.TaskA2ARound, update A2ARemoteUpdate) error {
	eventTerminal := update.Event != nil && update.Event.Event.Type == a2aext.EventExecutionTerminal
	statusTerminal := update.Status.Terminal()
	if !eventTerminal && !statusTerminal {
		return nil
	}
	if eventTerminal && !statusTerminal {
		return fmt.Errorf("%w: execution terminal requires a standard terminal status", domain.ErrConflict)
	}
	if statusTerminal && !eventTerminal {
		if update.Status == domain.TaskA2ARemoteStatusRejected && round != nil && round.LastSequence == 0 && update.Event == nil {
			return nil
		}
		return fmt.Errorf("%w: standard terminal status requires an execution terminal event", domain.ErrConflict)
	}
	eventStatus := statusFromTerminalPayload(update.Event)
	if eventStatus != update.Status {
		return fmt.Errorf("%w: execution terminal status %s does not match standard status %s", domain.ErrConflict, eventStatus, update.Status)
	}
	return nil
}

func applyA2AStatus(task *domain.Task, round *domain.TaskA2ARound, status domain.TaskA2ARemoteStatus, event *a2aext.ExecutionEvent, now time.Time) error {
	if status == "" || status == domain.TaskA2ARemoteStatusUnknown || status == domain.TaskA2ARemoteStatusUnspecified {
		status = statusFromTerminalPayload(event)
	}
	switch status {
	case domain.TaskA2ARemoteStatusSubmitted:
		return nil
	case domain.TaskA2ARemoteStatusWorking:
		if task.Status == domain.TaskStarting {
			worktree := task.WorktreePath
			if event != nil && event.Runtime != nil && event.Runtime.WorktreePath != "" {
				worktree = event.Runtime.WorktreePath
			}
			return task.MarkRunning(worktree, now)
		}
		if task.Status == domain.TaskWaitingInput {
			return task.Resume(now)
		}
	case domain.TaskA2ARemoteStatusInputRequired, domain.TaskA2ARemoteStatusAuthRequired:
		if task.Status == domain.TaskRunning || task.Status == domain.TaskStarting {
			return task.WaitForInput(now)
		}
	case domain.TaskA2ARemoteStatusCompleted:
		if !isTaskTerminal(task.Status) {
			return task.Complete(payloadFromEvent(event, "result", "content"), now, runtimeSession(event))
		}
	case domain.TaskA2ARemoteStatusFailed, domain.TaskA2ARemoteStatusRejected:
		if !isTaskTerminal(task.Status) {
			reason := formatA2ADiagnostic(round.ErrorCode, round.ErrorMessage)
			if reason == "" {
				reason = payloadFromEvent(event, "message", "error", "errorCode")
			}
			if reason == "" {
				reason = string(status)
			}
			return task.Fail(reason, now)
		}
	case domain.TaskA2ARemoteStatusCanceled:
		if !isTaskTerminal(task.Status) {
			return task.MarkInterrupted(now)
		}
	}
	return nil
}

func updateA2ARoundDiagnostic(round *domain.TaskA2ARound, event *a2aext.ExecutionEvent) (string, string) {
	code := strings.TrimSpace(payloadString(event.Payload, "errorCode"))
	message := strings.TrimSpace(payloadString(event.Payload, "message"))
	if code != "" {
		round.ErrorCode = code
	}
	if message != "" {
		round.ErrorMessage = message
	}
	if retryable, ok := event.Payload["retryable"].(bool); ok {
		round.Retryable = retryable
	}
	return round.ErrorCode, round.ErrorMessage
}

func formatA2ADiagnostic(code, message string) string {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)
	switch {
	case code != "" && message != "":
		return code + ": " + message
	case code != "":
		return code
	default:
		return message
	}
}

func validateA2AEventScope(round *domain.TaskA2ARound, event *a2aext.ExecutionEvent) error {
	scope := event.Scope
	if scope.LocalTaskID != round.TaskID || scope.ExecutionID != round.ExecutionID || scope.Attempt != round.Attempt || scope.Turn != round.Turn || scope.WorkerID != round.WorkerID {
		return fmt.Errorf("%w: execution event scope does not match round", domain.ErrConflict)
	}
	return nil
}

func hashA2AEvent(event *a2aext.ExecutionEvent) (string, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func payloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			return value
		}
	}
	return ""
}

func payloadJSON(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func payloadFromEvent(event *a2aext.ExecutionEvent, keys ...string) string {
	if event == nil {
		return ""
	}
	return payloadString(event.Payload, keys...)
}

func runtimeSession(event *a2aext.ExecutionEvent) string {
	if event == nil || event.Runtime == nil {
		return ""
	}
	return event.Runtime.AgentSessionID
}

func statusFromTerminalPayload(event *a2aext.ExecutionEvent) domain.TaskA2ARemoteStatus {
	if event == nil || event.Event.Type != a2aext.EventExecutionTerminal {
		return domain.TaskA2ARemoteStatusUnspecified
	}
	switch strings.ToUpper(payloadString(event.Payload, "status", "state")) {
	case "COMPLETED", "TASK_STATE_COMPLETED":
		return domain.TaskA2ARemoteStatusCompleted
	case "FAILED", "TASK_STATE_FAILED":
		return domain.TaskA2ARemoteStatusFailed
	case "REJECTED", "TASK_STATE_REJECTED":
		return domain.TaskA2ARemoteStatusRejected
	case "CANCELED", "CANCELLED", "TASK_STATE_CANCELED":
		return domain.TaskA2ARemoteStatusCanceled
	default:
		return domain.TaskA2ARemoteStatusUnspecified
	}
}
