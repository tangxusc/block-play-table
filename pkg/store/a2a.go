package store

import (
	"context"
	"fmt"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

// A2ACommandCommit 描述用户命令产生的 Task、Worker、round、意图和领域事件原子写入。
type A2ACommandCommit struct {
	Task                      *domain.Task
	ExpectedTaskVersion       int
	Worker                    *domain.Worker
	ExpectedWorkerVersion     int
	Round                     *domain.TaskA2ARound
	CreateRound               bool
	Intent                    *domain.TaskA2ADispatchIntent
	Interaction               *domain.TaskInteraction
	Conversations             []domain.ConversationMessage
	Events                    []domain.DomainEvent
	CancelPendingInteractions bool
}

// A2AProjectionCommit 描述一个远端事件 inbox 与所有业务投影的原子写入。
type A2AProjectionCommit struct {
	Inbox                     *domain.A2AEventInbox
	Task                      *domain.Task
	ExpectedTaskVersion       int
	Worker                    *domain.Worker
	ExpectedWorkerVersion     int
	Round                     *domain.TaskA2ARound
	ExpectedRoundVersion      int
	Intent                    *domain.TaskA2ADispatchIntent
	ExpectedIntentVersion     int
	Interaction               *domain.TaskInteraction
	Logs                      []domain.TaskLog
	Conversations             []domain.ConversationMessage
	Events                    []domain.DomainEvent
	CancelPendingInteractions bool
}

// A2AMigrationCommit 描述旧活跃任务切换到 A2A 控制面时的原子失败与 Worker 释放。
type A2AMigrationCommit struct {
	Task                      *domain.Task
	ExpectedTaskVersion       int
	Worker                    *domain.Worker
	ExpectedWorkerVersion     int
	Events                    []domain.DomainEvent
	CancelPendingInteractions bool
}

// A2AEventDisposition 表示 event inbox 对本次事件的处理结论。
type A2AEventDisposition string

const (
	// A2AEventApplied 表示事件首次写入 inbox 并完成业务投影。
	A2AEventApplied A2AEventDisposition = "APPLIED"
	// A2AEventDuplicate 表示相同内容的事件已完成投影，本次无需重复写入。
	A2AEventDuplicate A2AEventDisposition = "DUPLICATE"
)

// ErrA2AConcurrentModification 表示 round 或 intent 的 OCC 版本已经被其他处理器推进。
var ErrA2AConcurrentModification = fmt.Errorf("a2a concurrent modification")

// A2AStore 定义 Manager A2A 控制面所需的持久化和事务能力。
// 主要用法：由 Service 和 Reconciler 通过同一实现原子推进 command、round、intent 与事件投影。
type A2AStore interface {
	// CommitA2ACommand 原子提交用户命令产生的聚合、round、intent 和领域事件。
	// 参数：context 控制事务生命周期，A2ACommandCommit 描述写集合和 OCC 版本。
	// 返回：提交成功时返回 nil。
	// 错误：关联校验、OCC 或持久化失败时返回错误。
	CommitA2ACommand(context.Context, A2ACommandCommit) error
	// CommitA2AProjection 原子提交远端事件 inbox 及对应业务投影。
	// 参数：context 控制事务生命周期，A2AProjectionCommit 描述事件和完整投影写集合。
	// 返回：首次投影返回 APPLIED，同内容重放返回 DUPLICATE。
	// 错误：事件冲突、OCC 或持久化失败时返回错误。
	CommitA2AProjection(context.Context, A2AProjectionCommit) (A2AEventDisposition, error)
	// CommitA2AMigrationFailure 原子结束无法恢复的旧任务并释放 Worker。
	// 参数：context 控制事务生命周期，A2AMigrationCommit 描述迁移写集合。
	// 返回：确实迁移任务时返回 true，已由其他处理器推进时返回 false。
	// 错误：OCC 或持久化失败时返回错误。
	CommitA2AMigrationFailure(context.Context, A2AMigrationCommit) (bool, error)
	// A2AEventInbox 按 round 和事件 ID 查询幂等记录。
	// 参数：context 控制查询，两个字符串依次为 round ID 和事件 ID。
	// 返回：匹配的 inbox 记录。
	// 错误：记录不存在或查询失败时返回错误。
	A2AEventInbox(context.Context, string, string) (*domain.A2AEventInbox, error)
	// TaskA2ARounds 按任务查询全部 A2A 轮次。
	// 参数：context 控制查询，字符串为 Manager Task ID。
	// 返回：按存储实现定义的稳定顺序返回轮次列表。
	// 错误：查询失败时返回错误。
	TaskA2ARounds(context.Context, string) ([]domain.TaskA2ARound, error)
	// TaskA2ARound 按 round ID 查询单个 A2A 轮次。
	// 参数：context 控制查询，字符串为 round ID。
	// 返回：匹配的轮次快照。
	// 错误：轮次不存在或查询失败时返回错误。
	TaskA2ARound(context.Context, string) (*domain.TaskA2ARound, error)
	// LatestTaskA2ARound 查询任务最近创建的 A2A 轮次。
	// 参数：context 控制查询，字符串为 Manager Task ID。
	// 返回：最近轮次快照。
	// 错误：轮次不存在或查询失败时返回错误。
	LatestTaskA2ARound(context.Context, string) (*domain.TaskA2ARound, error)
	// A2ARoundsForReconcile 查询 Manager 启动后需要恢复追踪的轮次。
	// 参数：context 控制查询。
	// 返回：所有已绑定且未终止的轮次。
	// 错误：查询失败时返回错误。
	A2ARoundsForReconcile(context.Context) ([]domain.TaskA2ARound, error)
	// A2ADispatchIntentsDue 查询到期且可领取的下发意图。
	// 参数：context 控制查询，time.Time 是截止时间，int 是最大返回数量。
	// 返回：符合条件的意图列表。
	// 错误：参数或查询失败时返回错误。
	A2ADispatchIntentsDue(context.Context, time.Time, int) ([]domain.TaskA2ADispatchIntent, error)
	// A2ADispatchIntent 按 intent ID 查询单个下发意图。
	// 参数：context 控制查询，字符串为 intent ID。
	// 返回：匹配的意图快照。
	// 错误：意图不存在或查询失败时返回错误。
	A2ADispatchIntent(context.Context, string) (*domain.TaskA2ADispatchIntent, error)
	// UpdateA2ARound 使用期望版本更新轮次。
	// 参数：context 控制写入，TaskA2ARound 是新快照，int 是期望 OCC 版本。
	// 返回：更新成功时返回 nil。
	// 错误：OCC 冲突、参数或持久化失败时返回错误。
	UpdateA2ARound(context.Context, *domain.TaskA2ARound, int) error
	// UpdateA2ADispatchIntent 使用期望版本更新下发意图。
	// 参数：context 控制写入，TaskA2ADispatchIntent 是新快照，int 是期望 OCC 版本。
	// 返回：更新成功时返回 nil。
	// 错误：OCC 冲突、参数或持久化失败时返回错误。
	UpdateA2ADispatchIntent(context.Context, *domain.TaskA2ADispatchIntent, int) error
	// CommitA2ADispatchResult 原子保存 round 与 intent 的一次发送结果。
	// 参数：context 控制事务，两个对象是新快照，两个 int 分别是对应期望 OCC 版本。
	// 返回：提交成功时返回 nil。
	// 错误：关联校验、OCC 或持久化失败时返回错误。
	CommitA2ADispatchResult(context.Context, *domain.TaskA2ARound, int, *domain.TaskA2ADispatchIntent, int) error
}
