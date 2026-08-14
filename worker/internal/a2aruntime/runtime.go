package a2aruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aadapter"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

const runtimeUpdateBuffer = 32

// Config 描述 TaskRuntime 的 Worker 生命周期、持久化和 Adapter 依赖。
type Config struct {
	Context  context.Context
	WorkerID string
	WorkDir  string
	Store    *a2astore.Store
	Adapters map[a2aext.AgentType]a2aadapter.Adapter
	Review   ReviewRecorder
	Now      func() time.Time
}

// ReviewRecorder 记录每个 Agent turn 的 worktree 改动范围。
type ReviewRecorder interface {
	// BeginTurn 在 Agent 运行前建立 review 基线。
	// 参数：ctx 控制采集，taskID/worktreePath 标识任务目录，baseBranch/defaultBranch 提供比较基线。
	// 返回：传给 EndTurn 的不透明 token；空值表示无需结束采集。
	// 错误：本方法不返回错误，失败由实现记录并返回空 token。
	BeginTurn(ctx context.Context, taskID, worktreePath, baseBranch, defaultBranch string) string
	// EndTurn 在 Agent 结束后持久化 review 结果。
	// 参数：ctx 不受请求取消影响，taskID 和 token 必须与 BeginTurn 对应。
	// 返回：无。
	// 错误：本方法不返回错误，失败由实现记录。
	EndTurn(ctx context.Context, taskID, token string)
}

// Update 是 Runtime 交给 A2A AgentExecutor 的原子业务更新。
type Update struct {
	Event *a2aext.ExecutionEvent
	State a2a.TaskState
	Role  a2aext.ArtifactRole
}

// Turn 保存新 A2A Task 的首个事件和后续运行流。
type Turn struct {
	ExecutionID string
	Accepted    *a2aext.ExecutionEvent
	Updates     <-chan Update
}

// Runtime 管理 execution 到 CLI 进程、交互和事件流的内存绑定。
type Runtime struct {
	rootCtx  context.Context
	workerID string
	workDir  string
	store    *a2astore.Store
	adapters map[a2aext.AgentType]a2aadapter.Adapter
	review   ReviewRecorder
	now      func() time.Time

	mu              sync.Mutex
	executions      map[string]*execution
	pendingBegins   map[string]struct{}
	repositoryLocks sync.Map
}

type execution struct {
	ctx        context.Context
	cancel     context.CancelCauseFunc
	request    *a2aext.ExecutionRequest
	message    string
	taskID     string
	contextID  string
	adapter    a2aadapter.Adapter
	updates    chan Update
	done       chan struct{}
	startOnce  sync.Once
	finishOnce sync.Once
	emitMu     sync.Mutex

	mu                  sync.Mutex
	sequence            int64
	worktreePath        string
	agentSessionID      string
	branch              string
	result              string
	cancelRequested     bool
	interactionCanceled bool
	startRequested      bool
	eventErr            error
	pending             *pendingInteraction
	logChunkIndex       map[a2aext.LogStream]int64
	logRedactors        map[a2aext.LogStream]*a2aext.Redactor
}

type pendingInteraction struct {
	request  a2aadapter.InteractionRequest
	response chan a2aadapter.InteractionResponse
}

// New 创建绑定 Worker 生命周期的 TaskRuntime。
// 参数：config 提供根 context、Worker 身份、工作目录、Store 和 Agent Adapter。
// 返回：可由 A2A AgentExecutor 调用的 Runtime。
// 错误：必填配置为空、Agent 类型非法或 Adapter 为空时返回错误。
func New(config Config) (*Runtime, error) {
	if config.Context == nil || strings.TrimSpace(config.WorkerID) == "" || strings.TrimSpace(config.WorkDir) == "" || config.Store == nil {
		return nil, errors.New("TaskRuntime 缺少 context、WorkerID、WorkDir 或 Store")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	adapters := make(map[a2aext.AgentType]a2aadapter.Adapter, len(config.Adapters))
	for agentType, adapter := range config.Adapters {
		if !agentType.Valid() || adapter == nil {
			return nil, fmt.Errorf("TaskRuntime Adapter 配置非法: %s", agentType)
		}
		adapters[agentType] = adapter
	}
	return &Runtime{
		rootCtx: config.Context, workerID: config.WorkerID, workDir: config.WorkDir,
		store: config.Store, adapters: adapters, review: config.Review, now: config.Now,
		executions: map[string]*execution{}, pendingBegins: map[string]struct{}{},
	}, nil
}

// Begin 创建 START、RETRY 或 CONTINUE turn，但在首个 Task 快照提交前不启动 CLI。
// 参数：ctx 提供认证值，request 是完整 execution 请求，message 是用户文本，taskID/contextID 是 SDK 标识。
// 返回：包含 execution.accepted 和后续更新流的 Turn。
// 错误：请求、Adapter、续接绑定或并发状态非法，或 binding 保存失败时返回错误。
func (r *Runtime) Begin(ctx context.Context, request *a2aext.ExecutionRequest, message, taskID, contextID string) (*Turn, error) {
	if r == nil || ctx == nil || request == nil || taskID == "" || contextID == "" {
		return nil, errors.New("TaskRuntime Begin 参数不完整")
	}
	if err := a2aext.ValidateRequest(request, r.workerID); err != nil {
		return nil, err
	}
	if request.Command.Operation == a2aext.OperationInteractionResponse {
		return nil, errors.New("交互回复必须使用 ResumeInteraction")
	}
	adapter := r.adapters[request.Agent.Type]
	if adapter == nil {
		return nil, fmt.Errorf("%s: %s", a2aext.ErrorAgentUnavailable, request.Agent.Type)
	}
	executionID := request.Scope.ExecutionID
	if !r.reserveBegin(executionID) {
		return nil, errors.New("execution 已存在活动 Runtime")
	}
	reserved := true
	defer func() {
		if reserved {
			r.releaseBegin(executionID)
		}
	}()
	binding := a2astore.RuntimeBinding{
		ExecutionID: request.Scope.ExecutionID, LocalTaskID: request.Scope.LocalTaskID, WorkerID: r.workerID,
		TaskID: taskID, ContextID: contextID, Attempt: request.Scope.Attempt, Turn: request.Scope.Turn,
		AgentType: request.Agent.Type, State: "SUBMITTED",
	}
	if request.Command.Operation == a2aext.OperationContinue {
		existing, err := r.store.GetBinding(ctx, request.Scope.ExecutionID)
		if err != nil {
			return nil, err
		}
		if err := validateResume(r.workDir, request, existing, contextID); err != nil {
			return nil, err
		}
		binding.AgentSessionID = existing.AgentSessionID
		binding.WorktreePath = existing.WorktreePath
		binding.LastSequence = existing.LastSequence
	}
	if err := r.store.SaveBinding(ctx, binding); err != nil {
		return nil, err
	}
	storedBinding, err := r.store.GetBinding(ctx, request.Scope.ExecutionID)
	if err != nil {
		return nil, err
	}
	execCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	item := &execution{
		ctx: execCtx, cancel: cancel, request: request, message: message, taskID: taskID, contextID: contextID,
		adapter: adapter, updates: make(chan Update, runtimeUpdateBuffer), done: make(chan struct{}),
		sequence: storedBinding.LastSequence, worktreePath: storedBinding.WorktreePath,
		agentSessionID: storedBinding.AgentSessionID, logChunkIndex: map[a2aext.LogStream]int64{},
		logRedactors: map[a2aext.LogStream]*a2aext.Redactor{},
	}
	secrets := sensitiveValues(request)
	for _, stream := range []a2aext.LogStream{a2aext.LogStdout, a2aext.LogStderr, a2aext.LogSystem} {
		item.logRedactors[stream] = a2aext.NewRedactor(secrets)
	}
	accepted, err := r.newEvent(item, a2aext.EventExecutionAccepted, map[string]any{}, nil)
	if err != nil {
		cancel(err)
		return nil, err
	}
	r.mu.Lock()
	delete(r.pendingBegins, executionID)
	r.executions[executionID] = item
	reserved = false
	r.mu.Unlock()
	go func() {
		select {
		case <-r.rootCtx.Done():
			item.cancel(context.Cause(r.rootCtx))
		case <-item.done:
		}
	}()
	return &Turn{ExecutionID: request.Scope.ExecutionID, Accepted: accepted, Updates: item.updates}, nil
}

func (r *Runtime) reserveBegin(executionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.executions[executionID]; current != nil && !channelClosed(current.done) {
		return false
	}
	if _, exists := r.pendingBegins[executionID]; exists {
		return false
	}
	if r.pendingBegins == nil {
		r.pendingBegins = map[string]struct{}{}
	}
	// 持久化前预留 execution，避免并发 Begin 覆盖权威 binding。
	r.pendingBegins[executionID] = struct{}{}
	return true
}

func (r *Runtime) releaseBegin(executionID string) {
	r.mu.Lock()
	delete(r.pendingBegins, executionID)
	r.mu.Unlock()
}

// Start 在 accepted Task 已持久化后启动 worktree、命令与 Agent。
// 参数：executionID 选择 Begin 创建的 turn。
// 返回：成功触发后台运行时返回 nil，重复调用幂等成功。
// 错误：execution 不存在时返回错误。
func (r *Runtime) Start(executionID string) error {
	item := r.lookup(executionID)
	if item == nil {
		return fmt.Errorf("execution %s 不存在", executionID)
	}
	item.mu.Lock()
	item.startRequested = true
	item.mu.Unlock()
	item.startOnce.Do(func() { go r.startWhenStored(item) })
	return nil
}

// Abort 终止尚未成功创建首个 A2A Task 的 Runtime，且不产生业务终态事件。
// 参数：executionID 选择 Begin 已创建的运行实例，cause 记录初始化失败原因。
// 返回：实例不存在或已结束时幂等返回。
// 错误：本方法不返回错误。
func (r *Runtime) Abort(executionID string, cause error) {
	item := r.lookup(executionID)
	if item == nil {
		return
	}
	if cause == nil {
		cause = errors.New("A2A Task 初始化失败")
	}
	item.cancel(cause)
	item.mu.Lock()
	started := item.startRequested
	item.mu.Unlock()
	if !started {
		item.finish()
	}
}

// ResumeInteraction 向仍在内存中的 CLI 进程投递交互回复并返回后续更新流。
// 参数：ctx 控制回复投递，request 是 INTERACTION_RESPONSE 请求。
// 返回：原 execution 的后续更新通道。
// 错误：runtime、binding、interaction ID 或回复状态不一致时返回错误。
func (r *Runtime) ResumeInteraction(ctx context.Context, request *a2aext.ExecutionRequest) (<-chan Update, error) {
	if r == nil || ctx == nil || request == nil || request.Interaction == nil {
		return nil, errors.New("TaskRuntime ResumeInteraction 参数不完整")
	}
	item := r.lookup(request.Scope.ExecutionID)
	if item == nil {
		return nil, errors.New("交互对应的内存 Runtime 不存在")
	}
	item.mu.Lock()
	pending := item.pending
	item.mu.Unlock()
	if pending == nil || pending.request.ID != request.Interaction.ID {
		return nil, errors.New("交互 ID 与当前等待项不匹配")
	}
	response := a2aadapter.InteractionResponse{
		ID: request.Interaction.ID, Decision: request.Interaction.Decision,
		Message: request.Interaction.Message, Payload: request.Interaction.Payload,
	}
	select {
	case pending.response <- response:
		return item.updates, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-item.done:
		return nil, errors.New("交互 Runtime 已结束")
	}
}

// Cancel 取消 execution 的全部子进程并等待退出。
// 参数：ctx 控制等待时限，executionID 选择运行实例。
// 返回：进程已退出或此前已结束时返回 nil。
// 错误：execution 不存在或等待 context 结束时返回错误。
func (r *Runtime) Cancel(ctx context.Context, executionID string) error {
	item := r.lookup(executionID)
	if item == nil {
		return fmt.Errorf("execution %s 不存在", executionID)
	}
	item.mu.Lock()
	item.cancelRequested = true
	started := item.startRequested
	item.mu.Unlock()
	item.cancel(context.Canceled)
	if !started {
		item.finish()
	}
	select {
	case <-item.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) lookup(executionID string) *execution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.executions[executionID]
}

func (r *Runtime) run(item *execution) {
	defer item.finish()
	worktree, branch, err := r.prepareWorkspace(item.ctx, item.request)
	if err != nil {
		r.finishWithError(item, err)
		return
	}
	item.mu.Lock()
	item.worktreePath, item.branch = worktree, branch
	item.mu.Unlock()
	if err := r.saveBinding(item, "WORKING"); err != nil {
		r.finishWithError(item, err)
		return
	}
	if err := r.emitEvent(item, a2aext.EventWorkspaceReady, a2aext.ArtifactManifest, a2a.TaskStateWorking, map[string]any{}, item.runtimeInfo()); err != nil {
		r.finishWithError(item, err)
		return
	}
	environment := environmentMap(item.request)
	for _, command := range item.request.Commands.Pre {
		if err := r.runShell(item, environment, command); err != nil {
			r.finishWithError(item, err)
			return
		}
	}
	reviewToken := ""
	if r.review != nil {
		reviewToken = r.review.BeginTurn(item.ctx, item.request.Scope.LocalTaskID, worktree, item.request.Task.BaseBranch, item.request.Project.DefaultBranch)
	}
	agentCtx, cancelAgent := context.WithCancel(item.ctx)
	err = item.adapter.Run(agentCtx, a2aadapter.Input{
		Request: item.request, Message: item.message, WorktreePath: worktree,
		AgentSessionID: item.agentSessionID, Environment: environment,
		RequestInteraction: func(ctx context.Context, request a2aadapter.InteractionRequest) (a2aadapter.InteractionResponse, error) {
			return r.requestInteraction(ctx, item, request)
		},
	}, func(event a2aadapter.Event) {
		if eventErr := r.handleAdapterEvent(item, event); eventErr != nil {
			item.setEventError(adapterEventEmissionError(eventErr))
			cancelAgent()
		}
	})
	cancelAgent()
	if r.review != nil && reviewToken != "" {
		r.review.EndTurn(context.WithoutCancel(item.ctx), item.request.Scope.LocalTaskID, reviewToken)
	}
	item.mu.Lock()
	interactionCanceled := item.interactionCanceled
	eventErr := item.eventErr
	item.mu.Unlock()
	if eventErr != nil {
		r.finishWithError(item, eventErr)
		return
	}
	if interactionCanceled || errors.Is(err, a2aadapter.ErrInteractionCanceled) {
		r.finishCanceled(item, "用户取消了 Agent 交互")
		return
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(context.Cause(item.ctx), context.Canceled) {
			_ = r.flushLogs(item)
			return
		}
		r.finishWithError(item, err)
		return
	}
	for _, command := range item.request.Commands.Post {
		if err := r.runShell(item, environment, command); err != nil {
			r.finishWithError(item, err)
			return
		}
	}
	if err := r.flushLogs(item); err != nil {
		r.finishWithError(item, err)
		return
	}
	item.mu.Lock()
	result := item.result
	item.mu.Unlock()
	if strings.TrimSpace(result) == "" {
		result = "completed"
	}
	if err := r.emitEvent(item, a2aext.EventResultUpdated, a2aext.ArtifactResult, "", map[string]any{"result": result}, item.runtimeInfo()); err != nil {
		r.finishWithError(item, adapterEventEmissionError(err))
		return
	}
	_ = r.saveBinding(item, "COMPLETED")
	if err := r.emitEvent(item, a2aext.EventExecutionTerminal, a2aext.ArtifactManifest, a2a.TaskStateCompleted, map[string]any{
		"status": string(a2aext.TerminalCompleted), "result": result,
	}, item.runtimeInfo()); err != nil {
		r.finishWithError(item, adapterEventEmissionError(err))
	}
}

func (r *Runtime) startWhenStored(item *execution) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := r.store.Get(item.ctx, a2a.TaskID(item.taskID)); err == nil {
			r.run(item)
			return
		} else if !errors.Is(err, a2a.ErrTaskNotFound) {
			item.cancel(err)
			item.finish()
			return
		}
		select {
		case <-item.ctx.Done():
			item.finish()
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) handleAdapterEvent(item *execution, event a2aadapter.Event) error {
	if event.AgentSessionID != "" {
		item.mu.Lock()
		changed := item.agentSessionID != event.AgentSessionID
		item.agentSessionID = event.AgentSessionID
		item.mu.Unlock()
		if changed {
			_ = r.saveBinding(item, "WORKING")
			if err := r.emitEvent(item, a2aext.EventAgentSessionStarted, a2aext.ArtifactManifest, "", map[string]any{}, item.runtimeInfo()); err != nil {
				return err
			}
		}
	}
	switch event.Type {
	case a2aadapter.EventStdout:
		return r.emitLog(item, a2aext.LogStdout, event.Content, false)
	case a2aadapter.EventStderr:
		return r.emitLog(item, a2aext.LogStderr, event.Content, false)
	case a2aadapter.EventConversation:
		content := a2aext.NewRedactor(sensitiveValues(item.request)).Redact(event.Content)
		if content != "" {
			return r.emitEvent(item, a2aext.EventConversationMessage, a2aext.ArtifactConversation, "", map[string]any{
				"role": "assistant", "content": content,
			}, item.runtimeInfo())
		}
	case a2aadapter.EventCompleted:
		item.mu.Lock()
		item.result = a2aext.NewRedactor(sensitiveValues(item.request)).Redact(event.Content)
		item.mu.Unlock()
	case a2aadapter.EventFailed:
		item.mu.Lock()
		item.result = a2aext.NewRedactor(sensitiveValues(item.request)).Redact(event.Content)
		item.mu.Unlock()
	}
	return nil
}

func (r *Runtime) requestInteraction(ctx context.Context, item *execution, request a2aadapter.InteractionRequest) (a2aadapter.InteractionResponse, error) {
	if request.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return a2aadapter.InteractionResponse{}, err
		}
		request.ID = "interaction_" + id.String()
	}
	if request.AgentSessionID != "" {
		item.mu.Lock()
		changed := item.agentSessionID != request.AgentSessionID
		item.agentSessionID = request.AgentSessionID
		item.mu.Unlock()
		if changed {
			if err := r.saveBinding(item, "WORKING"); err != nil {
				return a2aadapter.InteractionResponse{}, err
			}
			if err := r.emitEvent(item, a2aext.EventAgentSessionStarted, a2aext.ArtifactManifest, "", map[string]any{}, item.runtimeInfo()); err != nil {
				return a2aadapter.InteractionResponse{}, adapterEventEmissionError(err)
			}
		}
	}
	pending := &pendingInteraction{request: request, response: make(chan a2aadapter.InteractionResponse, 1)}
	item.mu.Lock()
	if item.pending != nil {
		item.mu.Unlock()
		return a2aadapter.InteractionResponse{}, errors.New("execution 已有待处理交互")
	}
	item.pending = pending
	item.mu.Unlock()
	redactor := a2aext.NewRedactor(sensitiveValues(item.request))
	if err := r.emitEvent(item, a2aext.EventInteractionRequested, a2aext.ArtifactInteraction, a2a.TaskStateInputRequired, map[string]any{
		"interactionId": request.ID, "kind": string(request.Kind), "title": redactor.Redact(request.Title),
		"body": redactor.Redact(request.Body), "rawPayload": redactor.Redact(request.RawPayload),
	}, item.runtimeInfo()); err != nil {
		r.clearPendingInteraction(item, pending)
		return a2aadapter.InteractionResponse{}, adapterEventEmissionError(err)
	}
	select {
	case response := <-pending.response:
		item.mu.Lock()
		if item.pending == pending {
			item.pending = nil
		}
		if response.Decision == a2aext.DecisionCancel {
			item.interactionCanceled = true
		}
		item.mu.Unlock()
		if err := r.emitEvent(item, a2aext.EventInteractionResolved, a2aext.ArtifactInteraction, a2a.TaskStateWorking, map[string]any{
			"interactionId": request.ID, "kind": string(request.Kind), "decision": string(response.Decision),
			"message": redactor.Redact(response.Message), "payload": redactor.Redact(response.Payload),
		}, item.runtimeInfo()); err != nil {
			return a2aadapter.InteractionResponse{}, adapterEventEmissionError(err)
		}
		return response, nil
	case <-ctx.Done():
		r.clearPendingInteraction(item, pending)
		return a2aadapter.InteractionResponse{}, context.Cause(ctx)
	case <-item.ctx.Done():
		r.clearPendingInteraction(item, pending)
		return a2aadapter.InteractionResponse{}, context.Cause(item.ctx)
	}
}

func (r *Runtime) clearPendingInteraction(item *execution, pending *pendingInteraction) {
	item.mu.Lock()
	if item.pending == pending {
		item.pending = nil
	}
	item.mu.Unlock()
}

func (r *Runtime) finishWithError(item *execution, err error) {
	item.mu.Lock()
	canceled := item.cancelRequested
	item.mu.Unlock()
	if canceled || errors.Is(err, context.Canceled) {
		return
	}
	_ = r.flushLogs(item)
	code := a2aext.ErrorCode("")
	if errors.Is(err, a2aadapter.ErrEventTooLarge) {
		code = a2aext.ErrorAgentEventTooLarge
	}
	message := boundedEventMessage(a2aext.NewRedactor(sensitiveValues(item.request)).Redact(err.Error()))
	if errors.Is(err, a2aadapter.ErrAuthRequired) {
		code = a2aext.ErrorAuthRequired
		_ = r.saveBinding(item, "AUTH_REQUIRED")
		_ = r.emitEvent(item, a2aext.EventExecutionDiagnostic, a2aext.ArtifactDiagnostic, a2a.TaskStateAuthRequired, map[string]any{
			"errorCode": string(code), "message": message, "retryable": true,
		}, item.runtimeInfo())
		// 认证可由运维在进程外恢复；保持流存活，标准 CancelTask 负责唯一终态。
		<-item.ctx.Done()
		return
	}
	_ = r.emitEvent(item, a2aext.EventExecutionDiagnostic, a2aext.ArtifactDiagnostic, "", map[string]any{
		"errorCode": string(code), "message": message, "retryable": false,
	}, item.runtimeInfo())
	_ = r.saveBinding(item, "FAILED")
	_ = r.emitEvent(item, a2aext.EventExecutionTerminal, a2aext.ArtifactManifest, a2a.TaskStateFailed, map[string]any{
		"status": string(a2aext.TerminalFailed), "errorCode": string(code), "message": message,
	}, item.runtimeInfo())
}

func (r *Runtime) finishCanceled(item *execution, message string) {
	_ = r.flushLogs(item)
	_ = r.saveBinding(item, "CANCELED")
	_ = r.emitEvent(item, a2aext.EventExecutionTerminal, a2aext.ArtifactManifest, a2a.TaskStateCanceled, map[string]any{
		"status": string(a2aext.TerminalCanceled), "message": message,
	}, item.runtimeInfo())
}

// LastSequence 返回内存 Runtime 已成功发布的最后事件序号。
// 参数：executionID 标识待查询 execution。
// 返回：包含 accepted 及全部运行时更新的最后序号。
// 错误：execution 不存在时返回错误。
func (r *Runtime) LastSequence(executionID string) (int64, error) {
	item := r.lookup(executionID)
	if item == nil {
		return 0, fmt.Errorf("execution %s 不存在", executionID)
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	return item.sequence, nil
}

func (r *Runtime) emitLog(item *execution, stream a2aext.LogStream, content string, final bool) error {
	item.mu.Lock()
	redactor := item.logRedactors[stream]
	item.mu.Unlock()
	safe := redactor.RedactChunk(content, final)
	for _, chunk := range splitUTF8(safe, a2aext.MaxLogChunkBytes) {
		item.mu.Lock()
		index := item.logChunkIndex[stream]
		item.logChunkIndex[stream] = index + 1
		item.mu.Unlock()
		if err := r.emitEvent(item, a2aext.EventLogChunk, a2aext.ArtifactLog, "", map[string]any{
			"stream": string(stream), "content": chunk, "chunkIndex": index,
		}, item.runtimeInfo()); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) flushLogs(item *execution) error {
	for _, stream := range []a2aext.LogStream{a2aext.LogStdout, a2aext.LogStderr, a2aext.LogSystem} {
		if err := r.emitLog(item, stream, "", true); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) emitEvent(item *execution, eventType a2aext.EventType, role a2aext.ArtifactRole, state a2a.TaskState, payload map[string]any, runtime *a2aext.RuntimeInfo) error {
	// 序号分配与 channel 发布必须串行，取消时才能安全回滚未发布的尾部序号。
	item.emitMu.Lock()
	defer item.emitMu.Unlock()
	select {
	case <-item.ctx.Done():
		return context.Cause(item.ctx)
	default:
	}
	event, err := r.newEvent(item, eventType, payload, runtime)
	if err != nil {
		return err
	}
	select {
	case item.updates <- Update{Event: event, State: state, Role: role}:
		return nil
	case <-item.ctx.Done():
		item.rollbackSequence(event.Event.Sequence)
		return context.Cause(item.ctx)
	}
}

func (r *Runtime) newEvent(item *execution, eventType a2aext.EventType, payload map[string]any, runtime *a2aext.RuntimeInfo) (*a2aext.ExecutionEvent, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	item.mu.Lock()
	item.sequence++
	sequence := item.sequence
	item.mu.Unlock()
	event := &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event: a2aext.EventHeader{ID: id.String(), Sequence: sequence, Type: eventType, OccurredAt: r.now().UTC()},
		Scope: a2aext.EventScope{
			LocalTaskID: item.request.Scope.LocalTaskID, ExecutionID: item.request.Scope.ExecutionID,
			Attempt: item.request.Scope.Attempt, Turn: item.request.Scope.Turn, WorkerID: r.workerID,
		},
		Runtime: runtime, Payload: payload,
	}
	if err := a2aext.ValidateEvent(event); err != nil {
		item.rollbackSequence(sequence)
		return nil, err
	}
	return event, nil
}

func (item *execution) rollbackSequence(sequence int64) {
	item.mu.Lock()
	if item.sequence == sequence {
		item.sequence--
	}
	item.mu.Unlock()
}

func (item *execution) setEventError(err error) {
	if err == nil {
		return
	}
	item.mu.Lock()
	if item.eventErr == nil {
		item.eventErr = err
	}
	item.mu.Unlock()
}

func adapterEventEmissionError(err error) error {
	if err == nil || errors.Is(err, a2aadapter.ErrEventTooLarge) {
		return err
	}
	if strings.Contains(err.Error(), "JSON 大小不能超过") {
		return fmt.Errorf("%w: %v", a2aadapter.ErrEventTooLarge, err)
	}
	return err
}

func boundedEventMessage(message string) string {
	const limit = 4096
	if len(message) <= limit {
		return strings.ToValidUTF8(message, "?")
	}
	end := limit
	for end > 0 && !utf8.RuneStart(message[end]) {
		end--
	}
	return strings.ToValidUTF8(message[:end], "?") + "..."
}

func (r *Runtime) saveBinding(item *execution, state string) error {
	item.mu.Lock()
	binding := a2astore.RuntimeBinding{
		ExecutionID: item.request.Scope.ExecutionID, LocalTaskID: item.request.Scope.LocalTaskID,
		WorkerID: r.workerID, TaskID: item.taskID, ContextID: item.contextID,
		Attempt: item.request.Scope.Attempt, Turn: item.request.Scope.Turn, AgentType: item.request.Agent.Type,
		AgentSessionID: item.agentSessionID, WorktreePath: item.worktreePath, State: state,
	}
	item.mu.Unlock()
	return r.store.SaveBinding(item.ctx, binding)
}

func (item *execution) runtimeInfo() *a2aext.RuntimeInfo {
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.agentSessionID == "" && item.worktreePath == "" && item.branch == "" {
		return nil
	}
	return &a2aext.RuntimeInfo{AgentSessionID: item.agentSessionID, WorktreePath: item.worktreePath, Branch: item.branch}
}

func (item *execution) finish() {
	item.finishOnce.Do(func() {
		close(item.done)
		close(item.updates)
	})
}

func validateResume(workDir string, request *a2aext.ExecutionRequest, binding *a2astore.RuntimeBinding, contextID string) error {
	if request.Resume == nil || binding == nil {
		return errors.New("CONTINUE 缺少 resume 或 runtime binding")
	}
	path, err := a2aext.ValidateWorktreePath(workDir, request.Resume.WorktreePath)
	if err != nil {
		return err
	}
	boundPath, err := a2aext.ValidateWorktreePath(workDir, binding.WorktreePath)
	if err != nil {
		return err
	}
	if path != boundPath || request.Resume.AgentSessionID != binding.AgentSessionID || contextID != binding.ContextID ||
		request.Scope.Turn != binding.Turn+1 || request.Scope.Attempt != binding.Attempt {
		return a2astore.ErrProtocolConflict
	}
	return nil
}

func sensitiveValues(request *a2aext.ExecutionRequest) []string {
	var values []string
	for _, variable := range request.Environment.Variables {
		if variable.Sensitive && variable.Value != "" {
			values = append(values, variable.Value)
		}
	}
	return values
}

func environmentMap(request *a2aext.ExecutionRequest) map[string]string {
	result := make(map[string]string, len(request.Environment.Variables))
	for _, variable := range request.Environment.Variables {
		result[variable.Key] = variable.Value
	}
	return result
}

func splitUTF8(value string, maxBytes int) []string {
	if value == "" || maxBytes < 1 {
		return nil
	}
	result := make([]string, 0, len(value)/maxBytes+1)
	for len(value) > maxBytes {
		end := maxBytes
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		if end == 0 {
			end = maxBytes
		}
		result = append(result, value[:end])
		value = value[end:]
	}
	if value != "" {
		result = append(result, value)
	}
	return result
}

func channelClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
