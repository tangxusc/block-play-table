package a2aserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aruntime"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

// AgentExecutor 将 A2A SDK 调用桥接到 Worker TaskRuntime。
type AgentExecutor struct {
	workerID string
	store    *a2astore.Store
	runtime  taskRuntime
	now      func() time.Time

	mu          sync.Mutex
	drains      map[string]chan struct{}
	cancelLocks map[string]*sync.Mutex
}

type taskRuntime interface {
	Begin(context.Context, *a2aext.ExecutionRequest, string, string, string) (*a2aruntime.Turn, error)
	Start(string) error
	ResumeInteraction(context.Context, *a2aext.ExecutionRequest) (<-chan a2aruntime.Update, error)
	Cancel(context.Context, string) error
	Abort(string, error)
	LastSequence(string) (int64, error)
}

var _ a2asrv.AgentExecutor = (*AgentExecutor)(nil)
var _ a2asrv.AgentExecutionCleaner = (*AgentExecutor)(nil)

// NewAgentExecutor 创建 Worker A2A AgentExecutor。
// 参数：workerID 是目标 Worker，store 保存幂等与 Task 数据，runtime 管理 CLI 生命周期。
// 返回：可传入 a2asrv.NewHandler 的 AgentExecutor。
// 错误：本函数不返回错误；空依赖会在 Execute 时返回明确错误。
func NewAgentExecutor(workerID string, store *a2astore.Store, runtime *a2aruntime.Runtime) *AgentExecutor {
	return &AgentExecutor{
		workerID: workerID, store: store, runtime: runtime, now: time.Now,
		drains: map[string]chan struct{}{}, cancelLocks: map[string]*sync.Mutex{},
	}
}

// Execute 绑定 command，并把 Runtime 更新转换为 A2A Task、Artifact 和状态事件。
// 参数：ctx 由 SDK 脱离 HTTP/SSE 取消信号后提供，execCtx 保存 Task/Context 和请求 Message。
// 返回：按 SDK 要求先 Task、后 Artifact/Status 的事件序列。
// 错误：请求解析、幂等绑定或 Runtime 初始化在首 Task 前失败时返回错误。
func (e *AgentExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if e == nil || e.store == nil || e.runtime == nil || execCtx == nil || execCtx.Message == nil {
			yield(nil, errors.New("A2A AgentExecutor 依赖或请求为空"))
			return
		}
		drain := e.beginDrain(string(execCtx.TaskID))
		defer e.endDrain(string(execCtx.TaskID), drain)
		request, err := ParseExecutionRequest(execCtx.Message, e.workerID)
		if err != nil {
			e.abandon(ctx, request)
			yield(nil, err)
			return
		}
		if request.Command.Operation == a2aext.OperationInteractionResponse {
			if err := e.validateInteractionBinding(ctx, execCtx, request); err != nil {
				e.abandon(ctx, request)
				yield(nil, err)
				return
			}
		}
		if _, err := e.store.BindCommand(ctx, request, string(execCtx.TaskID), execCtx.ContextID); err != nil {
			e.abandon(ctx, request)
			yield(nil, fmt.Errorf("绑定 command 到 A2A Task: %w", err))
			return
		}
		if request.Command.Operation == a2aext.OperationInteractionResponse {
			updates, err := e.runtime.ResumeInteraction(ctx, request)
			if err != nil {
				if _, cleanupErr := e.store.AbandonBoundCommand(context.WithoutCancel(ctx), request, string(execCtx.TaskID)); cleanupErr != nil {
					err = errors.Join(err, cleanupErr)
				}
				yield(nil, err)
				return
			}
			e.yieldUpdates(ctx, execCtx, updates, yield)
			return
		}
		turn, err := e.runtime.Begin(ctx, request, messageText(execCtx.Message), string(execCtx.TaskID), execCtx.ContextID)
		if err != nil {
			task, taskErr := rejectedTask(execCtx, execCtx.Message, err, e.now().UTC())
			yield(task, taskErr)
			return
		}
		task, err := submittedTask(execCtx, execCtx.Message, turn.Accepted)
		if err != nil {
			yield(nil, err)
			return
		}
		if !yield(task, nil) {
			return
		}
		if err := e.runtime.Start(turn.ExecutionID); err != nil {
			yield(nil, err)
			return
		}
		e.yieldUpdates(ctx, execCtx, turn.Updates, yield)
	}
}

func (e *AgentExecutor) validateInteractionBinding(ctx context.Context, execCtx *a2asrv.ExecutorContext, request *a2aext.ExecutionRequest) error {
	binding, err := e.store.GetBinding(ctx, request.Scope.ExecutionID)
	if err != nil {
		return err
	}
	if request.Resume == nil || binding.TaskID != string(execCtx.TaskID) || binding.ContextID != execCtx.ContextID ||
		binding.LocalTaskID != request.Scope.LocalTaskID || binding.WorkerID != request.Scope.ExpectedWorkerID ||
		binding.Attempt != request.Scope.Attempt || binding.Turn != request.Scope.Turn || binding.AgentType != request.Agent.Type ||
		binding.AgentSessionID != request.Resume.AgentSessionID || binding.WorktreePath != request.Resume.WorktreePath {
		return fmt.Errorf("交互回复与 RuntimeBinding 不一致: %w", a2astore.ErrProtocolConflict)
	}
	return nil
}

// Cancel 停止 Runtime 进程组，并以标准 CANCELED 终态结束 Task。
// 参数：ctx 控制取消等待，execCtx 保存待取消 Task 的身份。
// 返回：execution.terminal Artifact 和 CANCELED 状态事件。
// 错误：binding 不存在、进程未能退出或事件构造失败时返回错误。
func (e *AgentExecutor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if e == nil || e.store == nil || e.runtime == nil || execCtx == nil {
			yield(nil, errors.New("A2A Cancel 依赖或请求为空"))
			return
		}
		cancelLock := e.cancelLock(string(execCtx.TaskID))
		cancelLock.Lock()
		defer cancelLock.Unlock()
		binding, err := e.store.GetBindingByTask(ctx, string(execCtx.TaskID))
		if err != nil {
			yield(nil, err)
			return
		}
		if binding.State == "CANCELED" {
			e.replayCanceled(ctx, execCtx, binding, yield)
			return
		}
		if err := e.runtime.Cancel(ctx, binding.ExecutionID); err != nil {
			yield(nil, err)
			return
		}
		if err := e.waitDrain(ctx, string(execCtx.TaskID)); err != nil {
			yield(nil, err)
			return
		}
		runtimeSequence, err := e.runtime.LastSequence(binding.ExecutionID)
		if err != nil {
			yield(nil, err)
			return
		}
		if err := e.waitBindingSequence(ctx, binding.ExecutionID, runtimeSequence); err != nil {
			yield(nil, err)
			return
		}
		binding, err = e.store.GetBinding(ctx, binding.ExecutionID)
		if err != nil {
			yield(nil, err)
			return
		}
		switch binding.State {
		case "CANCELED":
			e.replayCanceled(ctx, execCtx, binding, yield)
			return
		case "COMPLETED", "FAILED":
			yield(nil, a2a.ErrTaskNotCancelable)
			return
		}
		eventID, err := uuid.NewV7()
		if err != nil {
			yield(nil, err)
			return
		}
		now := e.now().UTC()
		event := &a2aext.ExecutionEvent{
			Kind: a2aext.EventKind, Version: a2aext.Version,
			Event: a2aext.EventHeader{
				ID: eventID.String(), Sequence: binding.LastSequence + 1,
				Type: a2aext.EventExecutionTerminal, OccurredAt: now,
			},
			Scope: a2aext.EventScope{
				LocalTaskID: binding.LocalTaskID, ExecutionID: binding.ExecutionID, Attempt: binding.Attempt,
				Turn: binding.Turn, WorkerID: binding.WorkerID,
			},
			Runtime: &a2aext.RuntimeInfo{AgentSessionID: binding.AgentSessionID, WorktreePath: binding.WorktreePath},
			Payload: map[string]any{"status": string(a2aext.TerminalCanceled), "message": "任务已取消"},
		}
		if err := a2aext.ValidateEvent(event); err != nil {
			yield(nil, err)
			return
		}
		if _, err := e.store.AppendEvent(ctx, event); err != nil {
			yield(nil, err)
			return
		}
		binding.State = "CANCELED"
		if err := e.store.SaveBinding(ctx, *binding); err != nil {
			yield(nil, err)
			return
		}
		artifact, err := artifactEvent(execCtx, event, a2aext.ArtifactManifest)
		if err != nil || !yield(artifact, err) {
			return
		}
		status, err := statusEvent(execCtx, event, a2a.TaskStateCanceled)
		yield(status, err)
	}
}

func (e *AgentExecutor) beginDrain(taskID string) chan struct{} {
	drain := make(chan struct{})
	e.mu.Lock()
	e.drains[taskID] = drain
	e.mu.Unlock()
	return drain
}

func (e *AgentExecutor) endDrain(taskID string, drain chan struct{}) {
	e.mu.Lock()
	if e.drains[taskID] == drain {
		delete(e.drains, taskID)
	}
	close(drain)
	e.mu.Unlock()
}

func (e *AgentExecutor) waitDrain(ctx context.Context, taskID string) error {
	e.mu.Lock()
	drain := e.drains[taskID]
	e.mu.Unlock()
	if drain == nil {
		return nil
	}
	select {
	case <-drain:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *AgentExecutor) cancelLock(taskID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	lock := e.cancelLocks[taskID]
	if lock == nil {
		lock = &sync.Mutex{}
		e.cancelLocks[taskID] = lock
	}
	return lock
}

func (e *AgentExecutor) waitBindingSequence(ctx context.Context, executionID string, sequence int64) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		binding, err := e.store.GetBinding(ctx, executionID)
		if err != nil {
			return err
		}
		if binding.LastSequence >= sequence {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *AgentExecutor) replayCanceled(ctx context.Context, execCtx *a2asrv.ExecutorContext, binding *a2astore.RuntimeBinding, yield func(a2a.Event, error) bool) {
	events, err := e.store.ListEvents(ctx, binding.ExecutionID, binding.LastSequence-1, 1)
	if err != nil || len(events) != 1 || events[0].Event.Type != a2aext.EventExecutionTerminal {
		if err == nil {
			err = errors.New("CANCELED binding 缺少唯一终态 journal")
		}
		yield(nil, err)
		return
	}
	event := events[0]
	artifact, err := artifactEvent(execCtx, event, a2aext.ArtifactManifest)
	if err != nil || !yield(artifact, err) {
		return
	}
	status, err := statusEvent(execCtx, event, a2a.TaskStateCanceled)
	yield(status, err)
}

// Cleanup 只释放尚未完成首次 Bind 的失败 reservation；已绑定 command 保持可重放。
// 参数：ctx 是 SDK detached execution context，execCtx 保存原 Message，result/executeErr 是执行结果。
// 返回：无。
// 错误：清理错误被忽略，reservation 仍可由后续相同 command 等待或恢复。
func (e *AgentExecutor) Cleanup(ctx context.Context, execCtx *a2asrv.ExecutorContext, _ a2a.SendMessageResult, executeErr error) {
	if executeErr == nil || e == nil || e.store == nil || execCtx == nil || execCtx.Message == nil {
		return
	}
	request, err := ParseExecutionRequest(execCtx.Message, e.workerID)
	if err == nil {
		if request.Command.Operation == a2aext.OperationInteractionResponse {
			return
		}
		e.runtime.Abort(request.Scope.ExecutionID, executeErr)
		_, _ = e.store.AbandonFailedBoundCommand(context.WithoutCancel(ctx), request, string(execCtx.TaskID))
	}
}

func (e *AgentExecutor) yieldUpdates(ctx context.Context, execCtx *a2asrv.ExecutorContext, updates <-chan a2aruntime.Update, yield func(a2a.Event, error) bool) {
	for {
		var update a2aruntime.Update
		var open bool
		select {
		case <-ctx.Done():
			return
		case update, open = <-updates:
			if !open {
				return
			}
		}
		artifact, err := artifactEvent(execCtx, update.Event, update.Role)
		if err != nil || !yield(artifact, err) {
			return
		}
		if update.State != "" {
			status, err := statusEvent(execCtx, update.Event, update.State)
			if err != nil || !yield(status, err) {
				return
			}
		}
		if update.State == a2a.TaskStateInputRequired || update.State.Terminal() {
			return
		}
	}
}

func (e *AgentExecutor) abandon(ctx context.Context, request *a2aext.ExecutionRequest) {
	if request != nil {
		_, _ = e.store.AbandonCommand(context.WithoutCancel(ctx), request)
	}
}

func submittedTask(execCtx *a2asrv.ExecutorContext, requestMessage *a2a.Message, accepted *a2aext.ExecutionEvent) (*a2a.Task, error) {
	if accepted == nil {
		return nil, errors.New("execution.accepted 不能为空")
	}
	historyMessage, err := jsonMessageCopy(requestMessage)
	if err != nil {
		return nil, err
	}
	task := a2a.NewSubmittedTask(execCtx, historyMessage)
	message, err := eventMessage(execCtx, accepted)
	if err != nil {
		return nil, err
	}
	task.Status.Message = message
	task.Status.Timestamp = &accepted.Event.OccurredAt
	artifact, err := eventArtifact(accepted, a2aext.ArtifactManifest)
	if err != nil {
		return nil, err
	}
	task.Artifacts = []*a2a.Artifact{artifact}
	return task, nil
}

func rejectedTask(execCtx *a2asrv.ExecutorContext, requestMessage *a2a.Message, cause error, now time.Time) (*a2a.Task, error) {
	historyMessage, err := jsonMessageCopy(requestMessage)
	if err != nil {
		return nil, err
	}
	task := a2a.NewSubmittedTask(execCtx, historyMessage)
	message := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewTextPart("Worker 拒绝执行: "+cause.Error()))
	message.Extensions = []string{a2aext.ExtensionURI}
	task.Status = a2a.TaskStatus{State: a2a.TaskStateRejected, Message: message, Timestamp: &now}
	return task, nil
}

func artifactEvent(execCtx *a2asrv.ExecutorContext, event *a2aext.ExecutionEvent, role a2aext.ArtifactRole) (*a2a.TaskArtifactUpdateEvent, error) {
	artifact, err := eventArtifact(event, role)
	if err != nil {
		return nil, err
	}
	return &a2a.TaskArtifactUpdateEvent{TaskID: execCtx.TaskID, ContextID: execCtx.ContextID, Artifact: artifact, LastChunk: true}, nil
}

func eventArtifact(event *a2aext.ExecutionEvent, role a2aext.ArtifactRole) (*a2a.Artifact, error) {
	eventValue, err := eventJSONValue(event)
	if err != nil {
		return nil, err
	}
	metadata := a2aext.ArtifactMetadata{
		Kind: a2aext.ArtifactKind, Version: a2aext.Version, Role: role,
		ExecutionID: event.Scope.ExecutionID, Attempt: event.Scope.Attempt, Turn: event.Scope.Turn,
		EventID: event.Event.ID, Sequence: event.Event.Sequence, FinalChunk: true,
		MIMEType: "application/json", CreatedAt: event.Event.OccurredAt,
	}
	if role == a2aext.ArtifactLog {
		metadata.Stream = a2aext.LogStream(payloadString(event.Payload, "stream"))
		metadata.ChunkIndex = payloadInt64(event.Payload, "chunkIndex")
	}
	if err := a2aext.ValidateArtifact(&metadata); err != nil {
		return nil, err
	}
	metadataValue, err := jsonObject(metadata, "Artifact metadata")
	if err != nil {
		return nil, err
	}
	return &a2a.Artifact{
		ID: a2a.ArtifactID(event.Event.ID), Name: string(role), Extensions: []string{a2aext.ExtensionURI},
		Metadata: map[string]any{a2aext.ExtensionURI: metadataValue}, Parts: a2a.ContentParts{a2a.NewDataPart(eventValue)},
	}, nil
}

func statusEvent(execCtx *a2asrv.ExecutorContext, event *a2aext.ExecutionEvent, state a2a.TaskState) (*a2a.TaskStatusUpdateEvent, error) {
	message, err := eventMessage(execCtx, event)
	if err != nil {
		return nil, err
	}
	result := a2a.NewStatusUpdateEvent(execCtx, state, message)
	result.Status.Timestamp = &event.Event.OccurredAt
	return result, nil
}

func eventMessage(execCtx *a2asrv.ExecutorContext, event *a2aext.ExecutionEvent) (*a2a.Message, error) {
	value, err := eventJSONValue(event)
	if err != nil {
		return nil, err
	}
	message := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewDataPart(value))
	message.Extensions = []string{a2aext.ExtensionURI}
	return message, nil
}

func eventJSONValue(event *a2aext.ExecutionEvent) (map[string]any, error) {
	if err := a2aext.ValidateEvent(event); err != nil {
		return nil, err
	}
	return jsonObject(event, "execution event")
}

func jsonObject(value any, name string) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("编码 %s: %w", name, err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解码 %s: %w", name, err)
	}
	return result, nil
}

func jsonMessageCopy(message *a2a.Message) (*a2a.Message, error) {
	if message == nil {
		return nil, errors.New("A2A Message 不能为空")
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("编码 A2A Message: %w", err)
	}
	var result a2a.Message
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解码 A2A Message: %w", err)
	}
	return &result, nil
}

func messageText(message *a2a.Message) string {
	if message == nil {
		return ""
	}
	for _, part := range message.Parts {
		if part != nil && strings.TrimSpace(part.Text()) != "" {
			return part.Text()
		}
	}
	return ""
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func payloadInt64(payload map[string]any, key string) int64 {
	switch value := payload[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}
