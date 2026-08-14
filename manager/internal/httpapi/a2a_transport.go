package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/frp"
)

const (
	a2aControlTimeout = 15 * time.Second
	a2aIdleTimeout    = 60 * time.Second
	a2aPollFallback   = 2 * time.Second
)

var (
	errA2AIdleTimeout            = errors.New("a2a stream idle timeout")
	errA2ATerminalPairIncomplete = errors.New("a2a terminal artifact is awaiting its status")
)

type workerA2ATarget struct {
	worker       *domain.Worker
	tunnel       *workerProxyTunnel
	host         string
	port         int
	cardPath     string
	endpointPath string
}

// A2ATransport 返回绑定当前 Worker Gateway 的 FRP A2A 传输。
// 参数：无。
// 返回：可注入 app.Reconciler 的传输接口。
// 错误：本方法不返回错误。
func (s *Server) A2ATransport() app.A2ATransport {
	return s.gateway
}

// Dispatch 通过官方 SDK 下发一个持久化 intent，并持续消费其流式更新。
// 参数：ctx 绑定 Manager 生命周期，request 是还原后的命令，handler 原子投影每条更新。
// 返回：流正常结束时返回 nil。
// 错误：能力声明、Agent Card、15 秒首响应、FRP 网络、SDK 协议或投影失败时返回错误。
func (g *WorkerGateway) Dispatch(ctx context.Context, request *app.A2ADispatchRequest, handler app.A2AUpdateHandler) error {
	if request == nil || handler == nil {
		return errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("a2a dispatch request and handler are required"))
	}
	client, err := g.a2aClient(ctx, request.Round.WorkerID)
	if err != nil {
		return err
	}
	if request.Intent.Operation == domain.TaskA2AOperationCancel {
		controlCtx, cancel := context.WithTimeout(ctx, a2aControlTimeout)
		defer cancel()
		task, err := client.CancelTask(controlCtx, &a2a.CancelTaskRequest{ID: a2a.TaskID(request.Round.A2ATaskID)})
		if errors.Is(err, a2a.ErrTaskNotCancelable) {
			// 重复取消以远端 Task 快照为准，避免把已终止 Task 误判为协议失败。
			task, err = getA2ATask(controlCtx, client, request.Round.A2ATaskID)
		}
		if err != nil {
			return err
		}
		_, err = emitA2ATask(controlCtx, request.Round, task, handler)
		return err
	}
	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(request.Text), a2a.NewDataPart(request.Request))
	message.ID = request.Intent.CommandID
	message.Extensions = []string{a2aext.ExtensionURI}
	switch request.Intent.Operation {
	case domain.TaskA2AOperationContinue:
		message.ContextID = request.Round.ContextID
	case domain.TaskA2AOperationInteractionResponse:
		message.ContextID = request.Round.ContextID
		message.TaskID = a2a.TaskID(request.Round.A2ATaskID)
	}
	streamCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	deadlineErr := errors.New("a2a streaming first response timeout")
	timer := time.AfterFunc(a2aControlTimeout, func() { cancel(deadlineErr) })
	defer timer.Stop()
	seen := false
	trackedRound := request.Round
	trackedHandler := func(updateCtx context.Context, update app.A2ARemoteUpdate) error {
		if update.TaskID != "" {
			trackedRound.A2ATaskID = update.TaskID
		}
		if update.ContextID != "" {
			trackedRound.ContextID = update.ContextID
		}
		if err := handler(updateCtx, update); err != nil {
			return err
		}
		if update.Event != nil && update.Event.Event.Sequence > trackedRound.LastSequence {
			trackedRound.LastSequence = update.Event.Event.Sequence
		}
		return nil
	}
	projector := a2aStreamProjector{round: trackedRound, handler: trackedHandler}
	for event, streamErr := range client.SendStreamingMessage(streamCtx, &a2a.SendMessageRequest{
		Message: message,
		Config:  &a2a.SendMessageConfig{ReturnImmediately: false},
	}) {
		if streamErr != nil {
			if cause := context.Cause(streamCtx); cause != nil && cause != context.Canceled {
				streamErr = cause
			}
			if projector.terminal != nil && context.Cause(streamCtx) == nil {
				streamErr = errors.Join(errA2ATerminalPairIncomplete, streamErr)
			}
			return reconcileA2ADispatch(ctx, client, trackedRound, handler, streamErr)
		}
		if !seen {
			seen = true
			timer.Stop()
		}
		terminal, err := projector.emit(streamCtx, event)
		if err != nil {
			return reconcileA2ADispatch(ctx, client, trackedRound, handler, err)
		}
		if terminal {
			return nil
		}
	}
	if !seen {
		if cause := context.Cause(streamCtx); cause != nil {
			return cause
		}
		return errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("a2a streaming response ended without events"))
	}
	if projector.terminal != nil {
		return reconcileA2ADispatch(ctx, client, trackedRound, handler, errA2ATerminalPairIncomplete)
	}
	return nil
}

func reconcileA2ADispatch(ctx context.Context, client *a2aclient.Client, round domain.TaskA2ARound, handler app.A2AUpdateHandler, streamErr error) error {
	if round.A2ATaskID == "" || round.ContextID == "" ||
		!errors.Is(streamErr, errA2AIdleTimeout) && !errors.Is(streamErr, app.ErrA2ASequenceGap) && !errors.Is(streamErr, errA2ATerminalPairIncomplete) {
		return streamErr
	}
	return reconcileA2ATask(ctx, client, round, handler)
}

// Reconcile 先用 GetTask 对账，再订阅非终态 Task；订阅不可用时退化为定期 GetTask。
// 参数：ctx 绑定 Manager 生命周期，round 是已绑定远端 Task，handler 原子投影每条更新。
// 返回：远端 Task 终态或 context 结束时返回 nil 或对应 context 错误。
// 错误：能力声明、Agent Card、GetTask、FRP 网络、SDK 协议或投影失败时返回错误。
func (g *WorkerGateway) Reconcile(ctx context.Context, round domain.TaskA2ARound, handler app.A2AUpdateHandler) error {
	if handler == nil || round.A2ATaskID == "" {
		return errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("bound a2a round and handler are required"))
	}
	client, err := g.a2aClient(ctx, round.WorkerID)
	if err != nil {
		return err
	}
	return reconcileA2ATask(ctx, client, round, handler)
}

func reconcileA2ATask(ctx context.Context, client *a2aclient.Client, round domain.TaskA2ARound, handler app.A2AUpdateHandler) error {
	pollOnly := false
	subscribeTaskMissing := false
	for {
		task, err := getA2ATask(ctx, client, round.A2ATaskID)
		if err != nil {
			return err
		}
		terminal, err := emitA2ATask(ctx, round, task, handler)
		if err != nil {
			if errors.Is(err, app.ErrA2ASequenceGap) {
				return errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("a2a GetTask artifact snapshot is incomplete: %w", err))
			}
			return err
		}
		if terminal {
			return err
		}
		if task.Status.State == a2a.TaskStateInputRequired || task.Status.State == a2a.TaskStateAuthRequired {
			// 等待态已由快照投影；后续交互回复或认证后的取消/重试会创建独立控制流。
			return nil
		}
		if !pollOnly {
			err = subscribeA2ATask(ctx, client, round, handler)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !pollOnly && errors.Is(err, a2a.ErrTaskNotFound) && !subscribeTaskMissing {
			// SDK 只允许订阅活跃执行；执行可能在 GetTask 与订阅之间刚好结束，因此先立即重查终态快照。
			subscribeTaskMissing = true
			continue
		}
		if !pollOnly && (err == nil || errors.Is(err, app.ErrA2ASequenceGap) || errors.Is(err, errA2AIdleTimeout) || errors.Is(err, errA2ATerminalPairIncomplete)) {
			subscribeTaskMissing = false
			continue
		}
		if !pollOnly && (errors.Is(err, a2a.ErrUnsupportedOperation) || errors.Is(err, a2a.ErrMethodNotFound) || errors.Is(err, a2a.ErrTaskNotFound)) {
			pollOnly = true
		} else if !pollOnly && err != nil {
			return err
		}
		timer := time.NewTimer(a2aPollFallback)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (g *WorkerGateway) a2aClient(ctx context.Context, workerID string) (*a2aclient.Client, error) {
	target, err := g.resolveWorkerA2ATarget(ctx, workerID)
	if err != nil {
		return nil, err
	}
	roundTripper := &a2aTunnelRoundTripper{
		tunnel: target.tunnel, host: target.host, port: target.port, cardPath: target.cardPath,
		endpointPath: target.endpointPath, token: g.workerToken, idleTimeout: a2aIdleTimeout,
	}
	httpClient := &http.Client{Transport: roundTripper}
	baseURL := "http://" + net.JoinHostPort(target.host, strconv.Itoa(target.port))
	cardCtx, cancel := context.WithTimeout(ctx, a2aControlTimeout)
	defer cancel()
	card, err := agentcard.NewResolver(httpClient).Resolve(cardCtx, baseURL, agentcard.WithPath(target.cardPath))
	if err != nil {
		return nil, err
	}
	if err := validateWorkerAgentCard(card, target); err != nil {
		return nil, errors.Join(app.ErrA2AProtocolConflict, err)
	}
	client, err := a2aclient.NewFromCard(ctx, card,
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithJSONRPCTransport(httpClient),
	)
	if err != nil {
		return nil, errors.Join(app.ErrA2AProtocolConflict, err)
	}
	return client, nil
}

func (g *WorkerGateway) resolveWorkerA2ATarget(ctx context.Context, workerID string) (workerA2ATarget, error) {
	worker, err := g.service.Store().Worker(ctx, workerID)
	if err != nil {
		return workerA2ATarget{}, errors.Join(app.ErrA2AProjection, err)
	}
	capabilities := worker.Capabilities
	host := strings.TrimSpace(capabilities["a2a_host"])
	if host != "127.0.0.1" && host != "::1" {
		return workerA2ATarget{}, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("worker %s a2a_host must be 127.0.0.1 or ::1", worker.ID))
	}
	port, err := proxyPort(capabilities["a2a_port"])
	if err != nil {
		return workerA2ATarget{}, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("worker %s a2a_port is invalid: %w", worker.ID, err))
	}
	cardPath := strings.TrimSpace(capabilities["a2a_card_path"])
	endpointPath := strings.TrimSpace(capabilities["a2a_endpoint_path"])
	if capabilities["a2a_version"] != a2aext.Version || capabilities["a2a_transport"] != string(a2a.TransportProtocolJSONRPC) ||
		capabilities["a2a_extension"] != a2aext.ExtensionURI || cardPath != "/.well-known/agent-card.json" || endpointPath != "/a2a" {
		return workerA2ATarget{}, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("worker %s has incompatible a2a capabilities", worker.ID))
	}
	tunnel := g.proxyTunnelByWorkerID(worker.ID)
	if tunnel == nil || tunnel.session == nil || tunnel.session.IsClosed() {
		return workerA2ATarget{}, fmt.Errorf("worker %s a2a FRP tunnel is not connected", worker.ID)
	}
	return workerA2ATarget{worker: worker, tunnel: tunnel, host: host, port: port, cardPath: cardPath, endpointPath: endpointPath}, nil
}

func validateWorkerAgentCard(card *a2a.AgentCard, target workerA2ATarget) error {
	if card == nil || !card.Capabilities.Streaming {
		return fmt.Errorf("worker %s agent card must support streaming", target.worker.ID)
	}
	extensionCount := 0
	requiredExtensionCount := 0
	for _, extension := range card.Capabilities.Extensions {
		if extension.URI == a2aext.ExtensionURI {
			extensionCount++
			if extension.Required {
				requiredExtensionCount++
			}
		}
	}
	if extensionCount != 1 || requiredExtensionCount != 1 {
		return fmt.Errorf("worker %s agent card must declare one required execution extension", target.worker.ID)
	}
	skillCounts := map[string]int{}
	for _, skill := range card.Skills {
		skillCounts[skill.ID]++
	}
	if len(card.Skills) != 2 {
		return fmt.Errorf("worker %s agent card must declare exactly two execution skills", target.worker.ID)
	}
	for _, skillID := range []string{"block-play-table-codex", "block-play-table-claude"} {
		if skillCounts[skillID] != 1 {
			return fmt.Errorf("worker %s agent card must declare skill %s exactly once", target.worker.ID, skillID)
		}
	}
	if len(card.SupportedInterfaces) != 1 {
		return fmt.Errorf("worker %s agent card must declare exactly one JSON-RPC interface", target.worker.ID)
	}
	iface := card.SupportedInterfaces[0]
	if iface == nil || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC || iface.ProtocolVersion != a2a.Version {
		return fmt.Errorf("worker %s agent card JSON-RPC endpoint does not match capabilities", target.worker.ID)
	}
	parsed, err := url.Parse(iface.URL)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Hostname() != target.host || parsed.Path != target.endpointPath || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("worker %s agent card JSON-RPC endpoint does not match capabilities", target.worker.ID)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port != target.port {
		return fmt.Errorf("worker %s agent card JSON-RPC endpoint does not match capabilities", target.worker.ID)
	}
	return nil
}

type a2aTunnelRoundTripper struct {
	tunnel       *workerProxyTunnel
	host         string
	port         int
	cardPath     string
	endpointPath string
	token        string
	idleTimeout  time.Duration
}

// RoundTrip 校验 A2A 请求目标并通过目标 Worker 的 FRP 隧道执行一次 HTTP 往返。
// 参数：req 是官方 A2A SDK 构造的请求，目标必须与 Worker 能力声明中的地址一致。
// 返回：Worker 的 HTTP 响应；SSE 响应体会附加空闲超时检测。
// 错误：请求为空、目标越界、端口非法、FRP 代理失败或请求上下文取消时返回错误。
func (t *a2aTunnelRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("a2a http request is required"))
	}
	if req.URL.Scheme != "http" || req.URL.Hostname() != t.host || req.URL.Path != t.cardPath && req.URL.Path != t.endpointPath {
		return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("a2a request target is outside declared worker endpoint"))
	}
	port, err := strconv.Atoi(req.URL.Port())
	if err != nil || port != t.port {
		return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("a2a request port is outside declared worker endpoint"))
	}
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Set(a2a.SvcParamExtensions, a2aext.ExtensionURI)
	if t.token != "" {
		cloned.Header.Set("Authorization", "Bearer "+t.token)
	} else {
		cloned.Header.Del("Authorization")
	}
	targetPath := cloned.URL.Path
	if cloned.URL.RawQuery != "" {
		targetPath += "?" + cloned.URL.RawQuery
	}
	resp, err := frp.ProxyHTTP(cloned.Context(), t.tunnel.session, cloned, frp.ProxyTarget{Host: t.host, Port: t.port, Path: targetPath})
	if err != nil {
		return nil, err
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") && t.idleTimeout > 0 {
		resp.Body = newIdleReadCloser(resp.Body, t.idleTimeout)
	}
	return resp, nil
}

type idleReadCloser struct {
	body     io.ReadCloser
	activity chan struct{}
	done     chan struct{}
	once     sync.Once
	timedOut atomic.Bool
}

func newIdleReadCloser(body io.ReadCloser, timeout time.Duration) *idleReadCloser {
	reader := &idleReadCloser{body: body, activity: make(chan struct{}, 1), done: make(chan struct{})}
	go reader.watch(timeout)
	return reader
}

// Read 从底层响应体读取数据并刷新 SSE 空闲计时器。
// 参数：buffer 是接收本次数据的目标字节切片。
// 返回：实际读取的字节数以及底层读取结果。
// 错误：底层读取失败时返回对应错误；空闲超时关闭响应体后返回包含 errA2AIdleTimeout 的错误。
func (r *idleReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.body.Read(buffer)
	if count > 0 {
		select {
		case r.activity <- struct{}{}:
		default:
		}
	}
	if err != nil && r.timedOut.Load() {
		return count, fmt.Errorf("%w: no network bytes received", errA2AIdleTimeout)
	}
	return count, err
}

// Close 幂等关闭空闲监视器和底层响应体。
// 参数：无。
// 返回：首次关闭底层响应体产生的错误；重复关闭返回 nil。
// 错误：底层响应体关闭失败时返回对应错误。
func (r *idleReadCloser) Close() error {
	var err error
	r.once.Do(func() {
		close(r.done)
		err = r.body.Close()
	})
	return err
}

func (r *idleReadCloser) watch(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-r.activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)
		case <-timer.C:
			r.timedOut.Store(true)
			_ = r.Close()
			return
		}
	}
}

func getA2ATask(ctx context.Context, client *a2aclient.Client, taskID string) (*a2a.Task, error) {
	controlCtx, cancel := context.WithTimeout(ctx, a2aControlTimeout)
	defer cancel()
	return client.GetTask(controlCtx, &a2a.GetTaskRequest{ID: a2a.TaskID(taskID)})
}

func subscribeA2ATask(ctx context.Context, client *a2aclient.Client, round domain.TaskA2ARound, handler app.A2AUpdateHandler) error {
	streamCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	deadlineErr := errors.New("a2a subscribe first response timeout")
	timer := time.AfterFunc(a2aControlTimeout, func() { cancel(deadlineErr) })
	defer timer.Stop()
	seen := false
	projector := a2aStreamProjector{round: round, handler: handler}
	for event, streamErr := range client.SubscribeToTask(streamCtx, &a2a.SubscribeToTaskRequest{ID: a2a.TaskID(round.A2ATaskID)}) {
		if streamErr != nil {
			if cause := context.Cause(streamCtx); cause != nil && cause != context.Canceled {
				return cause
			}
			if projector.terminal != nil {
				return errors.Join(errA2ATerminalPairIncomplete, streamErr)
			}
			return streamErr
		}
		if !seen {
			seen = true
			timer.Stop()
		}
		terminal, err := projector.emit(streamCtx, event)
		if err != nil || terminal {
			return err
		}
	}
	if !seen {
		if cause := context.Cause(streamCtx); cause != nil {
			return cause
		}
		return fmt.Errorf("a2a subscription ended without events")
	}
	if projector.terminal != nil {
		return errA2ATerminalPairIncomplete
	}
	return nil
}

type a2aPendingTerminal struct {
	update app.A2ARemoteUpdate
	hash   [sha256.Size]byte
}

type a2aStreamProjector struct {
	round             domain.TaskA2ARound
	handler           app.A2AUpdateHandler
	terminal          *a2aPendingTerminal
	executionObserved bool
}

func (p *a2aStreamProjector) emit(ctx context.Context, event a2a.Event) (bool, error) {
	if p.terminal != nil {
		status, ok := event.(*a2a.TaskStatusUpdateEvent)
		if !ok {
			return false, a2aTransportProtocolError("terminal artifact must be followed by its status update")
		}
		return p.emitTerminalStatus(ctx, status)
	}
	switch value := event.(type) {
	case *a2a.TaskArtifactUpdateEvent:
		events, err := executionEventsFromArtifact(p.round, value.Artifact)
		if err != nil {
			return false, err
		}
		if len(events) == 1 && events[0].Event.Type == a2aext.EventExecutionTerminal {
			if err := validateA2ARemoteIdentity(p.round, string(value.TaskID), value.ContextID); err != nil {
				return false, err
			}
			hash, err := hashA2ATransportEvent(events[0])
			if err != nil {
				return false, err
			}
			p.terminal = &a2aPendingTerminal{update: app.A2ARemoteUpdate{
				TaskID: string(value.TaskID), ContextID: value.ContextID,
				Sequence: events[0].Event.Sequence, Event: events[0],
			}, hash: hash}
			return false, nil
		}
	case *a2a.TaskStatusUpdateEvent:
		if mapA2ATaskState(value.Status.State).Terminal() {
			return p.emitTerminalStatus(ctx, value)
		}
	}
	return emitA2AEvent(ctx, p.round, event, p.handle)
}

func (p *a2aStreamProjector) handle(ctx context.Context, update app.A2ARemoteUpdate) error {
	if err := p.handler(ctx, update); err != nil {
		return err
	}
	if update.Event != nil {
		// execution 事件证明 Runtime 已建立，后续不能降级为早期 REJECTED。
		p.executionObserved = true
	}
	return nil
}

func (p *a2aStreamProjector) emitTerminalStatus(ctx context.Context, status *a2a.TaskStatusUpdateEvent) (bool, error) {
	if status == nil {
		return false, a2aTransportProtocolError("terminal status update is required")
	}
	standardStatus := mapA2ATaskState(status.Status.State)
	if !standardStatus.Terminal() {
		return false, a2aTransportProtocolError("terminal artifact was followed by a non-terminal status")
	}
	if err := validateA2ARemoteIdentity(p.round, string(status.TaskID), status.ContextID); err != nil {
		return false, err
	}
	if status.Status.Message != nil && (string(status.Status.Message.TaskID) != string(status.TaskID) || status.Status.Message.ContextID != status.ContextID) {
		return false, a2aTransportProtocolError("terminal status message identity does not match its task")
	}
	events, err := executionEventsFromMessage(p.round, status.Status.Message)
	if err != nil {
		return false, err
	}
	if len(events) == 0 {
		if p.terminal != nil {
			return false, a2aTransportProtocolError("terminal artifact status message lacks its terminal event")
		}
		if standardStatus != domain.TaskA2ARemoteStatusRejected || p.executionObserved || p.round.LastSequence > 0 {
			return false, a2aTransportProtocolError("only an early rejected task may omit its terminal event")
		}
		if err := p.handler(ctx, app.A2ARemoteUpdate{TaskID: string(status.TaskID), ContextID: status.ContextID, Status: standardStatus}); err != nil {
			return false, err
		}
		return true, nil
	}
	if len(events) != 1 || events[0].Event.Type != a2aext.EventExecutionTerminal {
		return false, a2aTransportProtocolError("terminal status message must contain exactly one terminal event")
	}
	if p.terminal == nil {
		// 订阅可能从配对中间恢复；交给 GetTask 获取含 Artifact 的完整快照。
		return false, errA2ATerminalPairIncomplete
	}
	if eventStatus := mapA2ATerminalEventStatus(events[0]); eventStatus != standardStatus {
		return false, a2aTransportProtocolError(fmt.Sprintf("terminal event status %s does not match standard status %s", eventStatus, standardStatus))
	}
	hash, err := hashA2ATransportEvent(events[0])
	if err != nil {
		return false, err
	}
	update := app.A2ARemoteUpdate{
		TaskID: string(status.TaskID), ContextID: status.ContextID, Status: standardStatus,
		Sequence: events[0].Event.Sequence, Event: events[0],
	}
	if p.terminal.update.TaskID != update.TaskID || p.terminal.update.ContextID != update.ContextID ||
		p.terminal.update.Event.Event.ID != update.Event.Event.ID || p.terminal.hash != hash {
		return false, a2aTransportProtocolError("terminal artifact does not match its status message")
	}
	update.Event = p.terminal.update.Event
	update.Sequence = p.terminal.update.Sequence
	if err := p.handler(ctx, update); err != nil {
		return false, err
	}
	p.terminal = nil
	return true, nil
}

func emitA2AEvent(ctx context.Context, round domain.TaskA2ARound, event a2a.Event, handler app.A2AUpdateHandler) (bool, error) {
	switch value := event.(type) {
	case *a2a.Task:
		return emitA2ATask(ctx, round, value, handler)
	case *a2a.TaskStatusUpdateEvent:
		updates, err := executionEventsFromMessage(round, value.Status.Message)
		if err != nil {
			return false, err
		}
		if err := emitExecutionEvents(ctx, string(value.TaskID), value.ContextID, updates, handler); err != nil {
			return false, err
		}
		status := mapA2ATaskState(value.Status.State)
		if err := handler(ctx, app.A2ARemoteUpdate{TaskID: string(value.TaskID), ContextID: value.ContextID, Status: status}); err != nil {
			return false, err
		}
		return status.Terminal(), nil
	case *a2a.TaskArtifactUpdateEvent:
		updates, err := executionEventsFromArtifact(round, value.Artifact)
		if err != nil {
			return false, err
		}
		return false, emitExecutionEvents(ctx, string(value.TaskID), value.ContextID, updates, handler)
	case *a2a.Message:
		updates, err := executionEventsFromMessage(round, value)
		if err != nil {
			return false, err
		}
		return false, emitExecutionEvents(ctx, string(value.TaskID), value.ContextID, updates, handler)
	default:
		return false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("unsupported a2a SDK event %T", event))
	}
}

func emitA2ATask(ctx context.Context, round domain.TaskA2ARound, task *a2a.Task, handler app.A2AUpdateHandler) (bool, error) {
	if task == nil {
		return false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("a2a task is required"))
	}
	if err := validateA2ARemoteIdentity(round, string(task.ID), task.ContextID); err != nil {
		return false, err
	}
	statusEvents, err := executionEventsFromMessage(round, task.Status.Message)
	if err != nil {
		return false, err
	}
	if task.Status.Message != nil && (string(task.Status.Message.TaskID) != string(task.ID) || task.Status.Message.ContextID != task.ContextID) {
		return false, a2aTransportProtocolError("task status message identity does not match its task")
	}
	historyEvents := make([]*a2aext.ExecutionEvent, 0)
	for _, message := range task.History {
		updates, err := executionEventsFromMessage(round, message)
		if err != nil {
			return false, err
		}
		historyEvents = append(historyEvents, updates...)
	}
	artifactEvents := make([]*a2aext.ExecutionEvent, 0)
	for _, artifact := range task.Artifacts {
		updates, err := executionEventsFromArtifact(round, artifact)
		if err != nil {
			return false, err
		}
		artifactEvents = append(artifactEvents, updates...)
	}
	status := mapA2ATaskState(task.Status.State)
	if status.Terminal() {
		return emitA2ATerminalTask(ctx, round, task, status, statusEvents, historyEvents, artifactEvents, handler)
	}
	events := append(append(statusEvents, historyEvents...), artifactEvents...)
	for _, event := range events {
		if event.Event.Type == a2aext.EventExecutionTerminal {
			return false, a2aTransportProtocolError("non-terminal task snapshot contains a terminal event")
		}
	}
	// 所有重放都进入 inbox 哈希校验，避免同 eventId 的篡改内容被静默去重。
	if err := emitExecutionEvents(ctx, string(task.ID), task.ContextID, events, handler); err != nil {
		return false, err
	}
	if err := handler(ctx, app.A2ARemoteUpdate{TaskID: string(task.ID), ContextID: task.ContextID, Status: status}); err != nil {
		return false, err
	}
	return false, nil
}

func emitA2ATerminalTask(
	ctx context.Context,
	round domain.TaskA2ARound,
	task *a2a.Task,
	status domain.TaskA2ARemoteStatus,
	statusEvents, historyEvents, artifactEvents []*a2aext.ExecutionEvent,
	handler app.A2AUpdateHandler,
) (bool, error) {
	allEvents := append(append(statusEvents, historyEvents...), artifactEvents...)
	terminalCount := 0
	for _, event := range allEvents {
		if event.Event.Type == a2aext.EventExecutionTerminal {
			terminalCount++
		}
	}
	if terminalCount == 0 {
		if status != domain.TaskA2ARemoteStatusRejected || round.LastSequence > 0 || len(allEvents) != 0 || len(task.Artifacts) != 0 {
			return false, a2aTransportProtocolError("only an early rejected task without execution artifacts may omit its terminal event")
		}
		if err := handler(ctx, app.A2ARemoteUpdate{TaskID: string(task.ID), ContextID: task.ContextID, Status: status}); err != nil {
			return false, err
		}
		return true, nil
	}
	if len(statusEvents) != 1 || statusEvents[0].Event.Type != a2aext.EventExecutionTerminal {
		return false, a2aTransportProtocolError("terminal task status message must contain exactly one terminal event")
	}
	terminal := statusEvents[0]
	if eventStatus := mapA2ATerminalEventStatus(terminal); eventStatus != status {
		return false, a2aTransportProtocolError(fmt.Sprintf("terminal event status %s does not match standard status %s", eventStatus, status))
	}
	terminalHash, err := hashA2ATransportEvent(terminal)
	if err != nil {
		return false, err
	}
	artifactMatched := false
	nonTerminal := make([]*a2aext.ExecutionEvent, 0, len(historyEvents)+len(artifactEvents))
	for _, candidate := range append(historyEvents, artifactEvents...) {
		if candidate.Event.Type != a2aext.EventExecutionTerminal {
			if candidate.Event.Sequence >= terminal.Event.Sequence {
				return false, a2aTransportProtocolError("terminal event must be the final execution event")
			}
			nonTerminal = append(nonTerminal, candidate)
			continue
		}
		candidateHash, hashErr := hashA2ATransportEvent(candidate)
		if hashErr != nil {
			return false, hashErr
		}
		if candidate.Event.ID != terminal.Event.ID || candidateHash != terminalHash {
			return false, a2aTransportProtocolError("terminal task snapshot contains conflicting terminal events")
		}
	}
	for _, candidate := range artifactEvents {
		if candidate.Event.Type == a2aext.EventExecutionTerminal {
			artifactMatched = true
			break
		}
	}
	if !artifactMatched {
		return false, a2aTransportProtocolError("terminal task snapshot lacks its terminal artifact")
	}
	if err := emitExecutionEvents(ctx, string(task.ID), task.ContextID, nonTerminal, handler); err != nil {
		return false, err
	}
	if err := handler(ctx, app.A2ARemoteUpdate{
		TaskID: string(task.ID), ContextID: task.ContextID, Status: status,
		Sequence: terminal.Event.Sequence, Event: terminal,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func validateA2ARemoteIdentity(round domain.TaskA2ARound, taskID, contextID string) error {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(contextID) == "" {
		return a2aTransportProtocolError("a2a task and context identity are required")
	}
	if round.A2ATaskID != "" && round.A2ATaskID != taskID {
		return a2aTransportProtocolError("a2a task identity changed")
	}
	if round.ContextID != "" && round.ContextID != contextID {
		return a2aTransportProtocolError("a2a context identity changed")
	}
	return nil
}

func hashA2ATransportEvent(event *a2aext.ExecutionEvent) ([sha256.Size]byte, error) {
	if event == nil {
		return [sha256.Size]byte{}, a2aTransportProtocolError("execution event is required")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return [sha256.Size]byte{}, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("encode execution event hash: %w", err))
	}
	return sha256.Sum256(raw), nil
}

func mapA2ATerminalEventStatus(event *a2aext.ExecutionEvent) domain.TaskA2ARemoteStatus {
	if event == nil || event.Event.Type != a2aext.EventExecutionTerminal {
		return domain.TaskA2ARemoteStatusUnspecified
	}
	switch strings.ToUpper(a2aPayloadString(event.Payload, "status")) {
	case string(a2aext.TerminalCompleted):
		return domain.TaskA2ARemoteStatusCompleted
	case string(a2aext.TerminalFailed):
		return domain.TaskA2ARemoteStatusFailed
	case string(a2aext.TerminalRejected):
		return domain.TaskA2ARemoteStatusRejected
	case string(a2aext.TerminalCanceled):
		return domain.TaskA2ARemoteStatusCanceled
	default:
		return domain.TaskA2ARemoteStatusUnspecified
	}
}

func a2aTransportProtocolError(message string) error {
	return errors.Join(app.ErrA2AProtocolConflict, errors.New(message))
}

func emitExecutionEvents(ctx context.Context, taskID, contextID string, events []*a2aext.ExecutionEvent, handler app.A2AUpdateHandler) error {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Event.Sequence < events[j].Event.Sequence })
	for _, event := range events {
		if err := handler(ctx, app.A2ARemoteUpdate{TaskID: taskID, ContextID: contextID, Sequence: event.Event.Sequence, Event: event}); err != nil {
			return err
		}
	}
	return nil
}

func executionEventsFromMessage(round domain.TaskA2ARound, message *a2a.Message) ([]*a2aext.ExecutionEvent, error) {
	if message == nil {
		return nil, nil
	}
	events, err := executionEventsFromParts(message.Parts)
	if err != nil {
		return nil, err
	}
	if len(events) > 0 && countA2AExtension(message.Extensions) != 1 {
		return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("execution message must declare the extension exactly once"))
	}
	return events, nil
}

func executionEventsFromArtifact(round domain.TaskA2ARound, artifact *a2a.Artifact) ([]*a2aext.ExecutionEvent, error) {
	if artifact == nil {
		return nil, nil
	}
	_, metadataDeclared := artifact.Metadata[a2aext.ExtensionURI]
	extensionCount := countA2AExtension(artifact.Extensions)
	if extensionCount == 0 && !metadataDeclared {
		events, err := executionEventsFromParts(artifact.Parts)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("execution artifact does not declare the extension"))
		}
		return nil, nil
	}
	if extensionCount != 1 || !metadataDeclared {
		return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("execution artifact must declare the extension and metadata exactly once"))
	}
	metadata, ok, err := decodeA2AArtifactMetadata(artifact.Metadata)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("execution artifact metadata is required"))
	}
	events, err := executionEventsFromParts(artifact.Parts)
	if err != nil {
		return nil, err
	}
	if len(events) != 1 {
		return nil, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("execution artifact must contain exactly one execution event"))
	}
	if err := validateA2AArtifactEvent(round, artifact, metadata, events[0]); err != nil {
		return nil, errors.Join(app.ErrA2AProtocolConflict, err)
	}
	return events, nil
}

func executionEventsFromParts(parts a2a.ContentParts) ([]*a2aext.ExecutionEvent, error) {
	out := make([]*a2aext.ExecutionEvent, 0)
	for _, part := range parts {
		if part == nil {
			continue
		}
		event, ok, err := decodeA2AExecutionEvent(part.Data())
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, event)
		}
	}
	return out, nil
}

func countA2AExtension(extensions []string) int {
	count := 0
	for _, extension := range extensions {
		if extension == a2aext.ExtensionURI {
			count++
		}
	}
	return count
}

func validateA2AArtifactEvent(round domain.TaskA2ARound, artifact *a2a.Artifact, metadata a2aext.ArtifactMetadata, event *a2aext.ExecutionEvent) error {
	if event == nil {
		return fmt.Errorf("execution artifact event is required")
	}
	if string(artifact.ID) != metadata.EventID || artifact.Name != string(metadata.Role) {
		return fmt.Errorf("execution artifact identity or role does not match metadata")
	}
	if metadata.EventID != event.Event.ID || metadata.Sequence != event.Event.Sequence || metadata.ExecutionID != event.Scope.ExecutionID ||
		metadata.Attempt != event.Scope.Attempt || metadata.Turn != event.Scope.Turn || !metadata.CreatedAt.Equal(event.Event.OccurredAt) {
		return fmt.Errorf("execution artifact metadata does not match event")
	}
	if metadata.ExecutionID != round.ExecutionID || metadata.Attempt != round.Attempt || metadata.Turn != round.Turn {
		return fmt.Errorf("execution artifact scope does not match round")
	}
	if !a2aArtifactRoleMatchesEvent(metadata.Role, event.Event.Type) {
		return fmt.Errorf("execution artifact role does not match event type")
	}
	if metadata.Role == a2aext.ArtifactLog && string(metadata.Stream) != a2aPayloadString(event.Payload, "stream") {
		return fmt.Errorf("execution log artifact stream does not match event")
	}
	return nil
}

func a2aPayloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func a2aArtifactRoleMatchesEvent(role a2aext.ArtifactRole, eventType a2aext.EventType) bool {
	switch role {
	case a2aext.ArtifactManifest:
		return eventType == a2aext.EventExecutionAccepted || eventType == a2aext.EventWorkspaceReady ||
			eventType == a2aext.EventAgentSessionStarted || eventType == a2aext.EventAgentSessionUpdated || eventType == a2aext.EventExecutionTerminal
	case a2aext.ArtifactLog:
		return eventType == a2aext.EventLogChunk
	case a2aext.ArtifactConversation:
		return eventType == a2aext.EventConversationMessage
	case a2aext.ArtifactInteraction:
		return eventType == a2aext.EventInteractionRequested || eventType == a2aext.EventInteractionResolved
	case a2aext.ArtifactResult:
		return eventType == a2aext.EventResultUpdated
	case a2aext.ArtifactDiagnostic:
		return eventType == a2aext.EventExecutionDiagnostic
	default:
		return false
	}
}

func decodeA2AExecutionEvent(value any) (*a2aext.ExecutionEvent, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("encode a2a data part: %w", err))
	}
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("decode a2a data part kind: %w", err))
	}
	if header.Kind != a2aext.EventKind {
		return nil, false, nil
	}
	event, err := a2aext.DecodeExecutionEvent(raw)
	if err != nil {
		return nil, false, errors.Join(app.ErrA2AProtocolConflict, err)
	}
	if err := a2aext.ValidateEvent(event); err != nil {
		return nil, false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("validate a2a execution event: %w", err))
	}
	return event, true, nil
}

func decodeA2AArtifactMetadata(value map[string]any) (a2aext.ArtifactMetadata, bool, error) {
	if value == nil {
		return a2aext.ArtifactMetadata{}, false, nil
	}
	candidate := any(nil)
	if nested, ok := value[a2aext.ExtensionURI]; ok {
		candidate = nested
	} else {
		raw, err := json.Marshal(value)
		if err != nil {
			return a2aext.ArtifactMetadata{}, false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("encode a2a artifact metadata: %w", err))
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return a2aext.ArtifactMetadata{}, false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("decode a2a artifact metadata kind: %w", err))
		}
		if header.Kind != a2aext.ArtifactKind {
			return a2aext.ArtifactMetadata{}, false, nil
		}
		candidate = value
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return a2aext.ArtifactMetadata{}, false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("encode a2a artifact metadata: %w", err))
	}
	metadata, err := a2aext.DecodeArtifactMetadata(raw)
	if err != nil {
		return a2aext.ArtifactMetadata{}, false, errors.Join(app.ErrA2AProtocolConflict, err)
	}
	if err := a2aext.ValidateArtifact(metadata); err != nil {
		return a2aext.ArtifactMetadata{}, false, errors.Join(app.ErrA2AProtocolConflict, fmt.Errorf("validate a2a artifact metadata: %w", err))
	}
	return *metadata, true, nil
}

func mapA2ATaskState(state a2a.TaskState) domain.TaskA2ARemoteStatus {
	switch state {
	case a2a.TaskStateSubmitted:
		return domain.TaskA2ARemoteStatusSubmitted
	case a2a.TaskStateWorking:
		return domain.TaskA2ARemoteStatusWorking
	case a2a.TaskStateInputRequired:
		return domain.TaskA2ARemoteStatusInputRequired
	case a2a.TaskStateAuthRequired:
		return domain.TaskA2ARemoteStatusAuthRequired
	case a2a.TaskStateCompleted:
		return domain.TaskA2ARemoteStatusCompleted
	case a2a.TaskStateFailed:
		return domain.TaskA2ARemoteStatusFailed
	case a2a.TaskStateRejected:
		return domain.TaskA2ARemoteStatusRejected
	case a2a.TaskStateCanceled:
		return domain.TaskA2ARemoteStatusCanceled
	default:
		return domain.TaskA2ARemoteStatusUnspecified
	}
}
