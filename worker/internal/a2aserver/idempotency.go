package a2aserver

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

type idempotentHandler struct {
	next     a2asrv.RequestHandler
	store    *a2astore.Store
	workerID string
}

// SendMessage 对同步消息执行 command 幂等校验后交给 SDK handler。
// 参数：ctx 控制请求生命周期，request 是待处理的 A2A 消息。
// 返回：重复命令返回既有 Task，首次命令返回下游处理结果。
// 错误：请求非法、幂等内容冲突、存储失败或下游处理失败时返回错误。
func (h *idempotentHandler) SendMessage(ctx context.Context, request *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	execution, replay, err := h.prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		return replay, nil
	}
	result, err := h.next.SendMessage(ctx, request)
	if err != nil {
		return nil, h.abandonUnbound(ctx, execution, err)
	}
	return result, nil
}

// SendStreamingMessage 对流式消息执行 command 幂等校验后交给 SDK handler。
// 参数：ctx 控制请求与订阅生命周期，request 是待处理的 A2A 消息。
// 返回：重复命令只产生既有 Task，首次命令转发下游事件流。
// 错误：请求、幂等、存储或下游流错误通过返回的迭代器产生。
func (h *idempotentHandler) SendStreamingMessage(ctx context.Context, request *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		execution, replay, err := h.prepare(ctx, request)
		if err != nil {
			yield(nil, err)
			return
		}
		if replay != nil {
			yield(replay, nil)
			return
		}
		for event, streamErr := range h.next.SendStreamingMessage(ctx, request) {
			if streamErr != nil {
				streamErr = h.abandonUnbound(ctx, execution, streamErr)
			}
			if !yield(event, streamErr) {
				return
			}
		}
	}
}

func (h *idempotentHandler) abandonUnbound(ctx context.Context, execution *a2aext.ExecutionRequest, cause error) error {
	if execution == nil || cause == nil {
		return cause
	}
	_, err := h.store.AbandonCommand(context.WithoutCancel(ctx), execution)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("释放失败的 command reservation: %w", err))
	}
	return cause
}

func (h *idempotentHandler) prepare(ctx context.Context, request *a2a.SendMessageRequest) (*a2aext.ExecutionRequest, *a2a.Task, error) {
	if request == nil {
		return nil, nil, a2a.ErrInvalidParams
	}
	execution, err := ParseExecutionRequest(request.Message, h.workerID)
	if err != nil {
		return nil, nil, err
	}
	record, created, err := h.store.ReserveCommand(ctx, execution)
	if errors.Is(err, a2astore.ErrProtocolConflict) {
		return nil, nil, fmt.Errorf("command payload 冲突: %w", a2a.ErrInvalidParams)
	}
	if err != nil {
		return nil, nil, err
	}
	if created {
		return execution, nil, nil
	}
	if record.TaskID == "" {
		record, err = h.waitForBoundCommand(ctx, execution.Command.ID)
		if err != nil {
			return nil, nil, err
		}
	}
	stored, err := h.waitForTask(ctx, record.TaskID)
	if err != nil {
		return nil, nil, err
	}
	return execution, stored.Task, nil
}

func (h *idempotentHandler) waitForTask(ctx context.Context, taskID string) (*taskstore.StoredTask, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		stored, err := h.store.Get(ctx, a2a.TaskID(taskID))
		if err == nil {
			return stored, nil
		}
		if !errors.Is(err, a2a.ErrTaskNotFound) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *idempotentHandler) waitForBoundCommand(ctx context.Context, commandID string) (*a2astore.CommandRecord, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := h.store.LookupCommand(ctx, commandID)
		if err != nil {
			return nil, err
		}
		if record.TaskID != "" {
			return record, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// GetTask 将标准 Task 查询委托给 SDK handler。
// 参数：ctx 控制查询生命周期，request 指定 Task 及历史长度。
// 返回：当前 Task 快照。
// 错误：请求非法、Task 不存在或存储查询失败时返回错误。
func (h *idempotentHandler) GetTask(ctx context.Context, request *a2a.GetTaskRequest) (*a2a.Task, error) {
	return h.next.GetTask(ctx, request)
}

// ListTasks 将标准 Task 列表查询委托给 SDK handler。
// 参数：ctx 控制查询生命周期，request 指定过滤和分页条件。
// 返回：符合条件的 Task 列表及分页信息。
// 错误：请求非法或存储查询失败时返回错误。
func (h *idempotentHandler) ListTasks(ctx context.Context, request *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return h.next.ListTasks(ctx, request)
}

// CancelTask 将标准 Task 取消请求委托给 SDK handler。
// 参数：ctx 控制取消生命周期，request 指定待取消 Task。
// 返回：取消后的 Task 快照。
// 错误：请求非法、Task 不存在或 Runtime 取消失败时返回错误。
func (h *idempotentHandler) CancelTask(ctx context.Context, request *a2a.CancelTaskRequest) (*a2a.Task, error) {
	return h.next.CancelTask(ctx, request)
}

// SubscribeToTask 将标准 Task 订阅请求委托给 SDK handler。
// 参数：ctx 仅控制订阅生命周期，request 指定待订阅 Task。
// 返回：Task 后续事件流。
// 错误：请求、Task 查询或订阅错误通过返回的迭代器产生。
func (h *idempotentHandler) SubscribeToTask(ctx context.Context, request *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	return h.next.SubscribeToTask(ctx, request)
}

// GetTaskPushConfig 将推送配置查询委托给 SDK handler。
// 参数：ctx 控制查询生命周期，request 指定 Task 和配置标识。
// 返回：匹配的推送配置。
// 错误：请求非法、配置不存在或下游不支持时返回错误。
func (h *idempotentHandler) GetTaskPushConfig(ctx context.Context, request *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return h.next.GetTaskPushConfig(ctx, request)
}

// ListTaskPushConfigs 将推送配置列表查询委托给 SDK handler。
// 参数：ctx 控制查询生命周期，request 指定 Task 和分页条件。
// 返回：Task 的推送配置列表。
// 错误：请求非法、查询失败或下游不支持时返回错误。
func (h *idempotentHandler) ListTaskPushConfigs(ctx context.Context, request *a2a.ListTaskPushConfigRequest) (*a2a.ListTaskPushConfigResponse, error) {
	return h.next.ListTaskPushConfigs(ctx, request)
}

// CreateTaskPushConfig 将推送配置创建请求委托给 SDK handler。
// 参数：ctx 控制创建生命周期，request 包含 Task 及推送配置。
// 返回：持久化后的推送配置。
// 错误：请求非法、持久化失败或下游不支持时返回错误。
func (h *idempotentHandler) CreateTaskPushConfig(ctx context.Context, request *a2a.PushConfig) (*a2a.PushConfig, error) {
	return h.next.CreateTaskPushConfig(ctx, request)
}

// DeleteTaskPushConfig 将推送配置删除请求委托给 SDK handler。
// 参数：ctx 控制删除生命周期，request 指定 Task 和配置标识。
// 返回：删除成功时返回 nil。
// 错误：请求非法、配置不存在、持久化失败或下游不支持时返回错误。
func (h *idempotentHandler) DeleteTaskPushConfig(ctx context.Context, request *a2a.DeleteTaskPushConfigRequest) error {
	return h.next.DeleteTaskPushConfig(ctx, request)
}

// GetExtendedAgentCard 将扩展 Agent Card 请求委托给 SDK handler。
// 参数：ctx 控制查询生命周期，request 携带扩展卡片请求上下文。
// 返回：当前 Worker 的扩展 Agent Card。
// 错误：请求非法或下游不支持扩展卡片时返回错误。
func (h *idempotentHandler) GetExtendedAgentCard(ctx context.Context, request *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return h.next.GetExtendedAgentCard(ctx, request)
}
