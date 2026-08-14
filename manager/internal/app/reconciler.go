package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

// Reconciler 协调 Worker 心跳与 Manager A2A intent/round 的后台恢复。
// 主要用法：通过 NewReconciler 注入 Service 和 A2ATransport，再调用 Run 直到进程 context 结束。
type Reconciler struct {
	service            *Service
	timeout            time.Duration
	interval           time.Duration
	logger             *slog.Logger
	a2a                A2ATransport
	a2aMu              sync.Mutex
	a2aActive          map[string]struct{}
	a2aProjectionMu    sync.Mutex
	a2aProjectionLocks map[string]*a2aTaskProjectionLock
}

type a2aTaskProjectionLock struct {
	mu   sync.Mutex
	refs int
}

// A2ARemoteUpdate 表示 SDK 流中的标准 Task 状态和可选 execution 扩展事件。
// TaskID/ContextID 是远端身份，Status 是显式状态观测，Sequence/Event 描述可选扩展事件。
type A2ARemoteUpdate struct {
	TaskID    string
	ContextID string
	Status    domain.TaskA2ARemoteStatus
	Sequence  int64
	Event     *a2aext.ExecutionEvent
}

// A2AUpdateHandler 把一次已校验的远端更新交给 Manager 原子投影。
// 参数：context 控制单次投影，A2ARemoteUpdate 是待持久化更新。
// 返回：投影成功时返回 nil。
// 错误：协议、OCC 或 Store 失败时返回可由 Reconciler 分类的错误。
type A2AUpdateHandler func(context.Context, A2ARemoteUpdate) error

// A2ATransport 抽象 FRP 内的官方 A2A client，供 Reconciler 驱动发送和恢复。
// 实现必须把所有远端更新交给 handler，并保留官方 SDK 错误的 errors.Is 语义。
type A2ATransport interface {
	// Dispatch 发送一个持久化意图并持续投影当前响应流。
	// 参数：context 控制网络生命周期，A2ADispatchRequest 是待发送命令，A2AUpdateHandler 接收已校验更新。
	// 返回：流正常结束或到达终态时返回 nil。
	// 错误：Agent Card、网络、SDK 协议或投影失败时返回错误。
	Dispatch(context.Context, *A2ADispatchRequest, A2AUpdateHandler) error
	// Reconcile 查询既有远端 Task，并恢复非终态 Task 的订阅或轮询。
	// 参数：context 控制恢复生命周期，TaskA2ARound 提供绑定身份，A2AUpdateHandler 接收已校验更新。
	// 返回：远端到达终态或等待输入时返回 nil。
	// 错误：远端查询、网络、SDK 协议或投影失败时返回错误。
	Reconcile(context.Context, domain.TaskA2ARound, A2AUpdateHandler) error
}

// ReconcilerOption 配置 Reconciler 的可选依赖。
type ReconcilerOption func(*Reconciler)

// WithReconcilerLogger 注入后台协调日志记录器。
// 参数：logger 为目标记录器，nil 时保留默认记录器。
// 返回：可传给 NewReconciler 的配置选项。
// 错误：本函数不返回错误。
func WithReconcilerLogger(logger *slog.Logger) ReconcilerOption {
	return func(r *Reconciler) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// WithA2ATransport 注入通过 FRP 访问 Worker 的 A2A 传输实现。
// 参数：transport 提供发送、GetTask 对账和订阅能力。
// 返回：用于构造 Reconciler 的选项。
// 错误：本函数不返回错误；nil 表示仅运行旧的 Worker 心跳协调。
func WithA2ATransport(transport A2ATransport) ReconcilerOption {
	return func(r *Reconciler) {
		r.a2a = transport
	}
}

// NewReconciler 创建 Worker 心跳与 A2A 控制面协调器。
// 参数：service 提供业务与 Store，timeout/interval 配置心跳失联判定，options 注入传输和日志。
// 返回：可直接调用 Run 的协调器。
// 错误：本函数不返回错误；调用方必须提供非 nil service。
func NewReconciler(service *Service, timeout, interval time.Duration, options ...ReconcilerOption) *Reconciler {
	r := &Reconciler{
		service:            service,
		timeout:            timeout,
		interval:           interval,
		logger:             slog.Default(),
		a2aActive:          map[string]struct{}{},
		a2aProjectionLocks: map[string]*a2aTaskProjectionLock{},
	}
	if r.interval <= 0 {
		r.interval = timeout / 3
	}
	if r.interval <= 0 {
		r.interval = time.Second
	}
	for _, option := range options {
		option(r)
	}
	return r
}

// Run 持续执行 Worker 心跳检查、A2A intent 领取和 round 对账。
// 参数：ctx 绑定后台循环生命周期。
// 返回：无；context 结束后函数返回。
// 错误：单轮错误会记录并由后续轮次恢复，不向调用方返回。
func (r *Reconciler) Run(ctx context.Context) {
	if r.timeout <= 0 && r.a2a == nil {
		return
	}
	var heartbeatTicker *time.Ticker
	var heartbeat <-chan time.Time
	if r.timeout > 0 {
		heartbeatTicker = time.NewTicker(r.interval)
		heartbeat = heartbeatTicker.C
		defer heartbeatTicker.Stop()
	}
	var a2aTicker *time.Ticker
	var a2aTick <-chan time.Time
	if r.a2a != nil {
		a2aTicker = time.NewTicker(a2aPollInterval)
		a2aTick = a2aTicker.C
		defer a2aTicker.Stop()
		if err := r.ReconcileA2A(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Warn("initial a2a reconcile failed", "error", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat:
			if err := r.ReconcileWorkerLiveness(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Warn("reconcile worker liveness failed", "error", err)
			}
		case <-a2aTick:
			if err := r.ReconcileA2A(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Warn("reconcile a2a failed", "error", err)
			}
		}
	}
}

// ReconcileWorkerLiveness 把超过心跳窗口的 Worker 标记为离线。
// 参数：ctx 用于取消 Store 操作。
// 返回：禁用心跳检查或处理成功时返回 nil。
// 错误：读取或保存 Worker 状态失败时返回错误。
func (r *Reconciler) ReconcileWorkerLiveness(ctx context.Context) error {
	if r.timeout <= 0 {
		return nil
	}
	return r.service.MarkStaleWorkersOffline(ctx, r.timeout)
}
