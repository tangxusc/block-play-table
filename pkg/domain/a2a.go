package domain

import (
	"fmt"
	"strings"
	"time"
)

// TaskA2AOperation 表示 Manager 向 Worker 发起的一次 A2A 业务操作。
type TaskA2AOperation string

const (
	// TaskA2AOperationStart 表示首次启动任务。
	TaskA2AOperationStart TaskA2AOperation = "START"
	// TaskA2AOperationRetry 表示为失败或中断任务创建新 execution。
	TaskA2AOperationRetry TaskA2AOperation = "RETRY"
	// TaskA2AOperationContinue 表示在既有 execution 中创建新 turn。
	TaskA2AOperationContinue TaskA2AOperation = "CONTINUE"
	// TaskA2AOperationInteractionResponse 表示回复当前 A2A Task 的交互请求。
	TaskA2AOperationInteractionResponse TaskA2AOperation = "INTERACTION_RESPONSE"
	// TaskA2AOperationCancel 表示通过标准 CancelTask 取消远端任务。
	TaskA2AOperationCancel TaskA2AOperation = "CANCEL"
)

// Valid 判断操作是否属于协议允许的闭集。
// 参数：无。
// 返回：合法时返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (o TaskA2AOperation) Valid() bool {
	switch o {
	case TaskA2AOperationStart, TaskA2AOperationRetry, TaskA2AOperationContinue, TaskA2AOperationInteractionResponse, TaskA2AOperationCancel:
		return true
	default:
		return false
	}
}

// CreatesRound 判断操作是否会创建新的 A2A Task 执行轮次。
// 参数：无。
// 返回：START、RETRY、CONTINUE 返回 true，交互回复和取消返回 false。
// 错误：本方法不返回错误。
func (o TaskA2AOperation) CreatesRound() bool {
	return o == TaskA2AOperationStart || o == TaskA2AOperationRetry || o == TaskA2AOperationContinue
}

// TaskA2ARemoteStatus 表示官方 A2A Task 的远端状态快照。
type TaskA2ARemoteStatus string

const (
	// TaskA2ARemoteStatusUnspecified 表示远端没有提供可识别状态。
	TaskA2ARemoteStatusUnspecified TaskA2ARemoteStatus = "UNSPECIFIED"
	// TaskA2ARemoteStatusSubmitted 表示远端已经接受任务。
	TaskA2ARemoteStatusSubmitted TaskA2ARemoteStatus = "SUBMITTED"
	// TaskA2ARemoteStatusWorking 表示远端正在执行任务。
	TaskA2ARemoteStatusWorking TaskA2ARemoteStatus = "WORKING"
	// TaskA2ARemoteStatusInputRequired 表示远端等待用户输入。
	TaskA2ARemoteStatusInputRequired TaskA2ARemoteStatus = "INPUT_REQUIRED"
	// TaskA2ARemoteStatusAuthRequired 表示远端等待认证。
	TaskA2ARemoteStatusAuthRequired TaskA2ARemoteStatus = "AUTH_REQUIRED"
	// TaskA2ARemoteStatusCompleted 表示远端成功完成任务。
	TaskA2ARemoteStatusCompleted TaskA2ARemoteStatus = "COMPLETED"
	// TaskA2ARemoteStatusFailed 表示远端执行失败。
	TaskA2ARemoteStatusFailed TaskA2ARemoteStatus = "FAILED"
	// TaskA2ARemoteStatusRejected 表示远端拒绝执行请求。
	TaskA2ARemoteStatusRejected TaskA2ARemoteStatus = "REJECTED"
	// TaskA2ARemoteStatusCanceled 表示远端已确认取消。
	TaskA2ARemoteStatusCanceled TaskA2ARemoteStatus = "CANCELED"
	// TaskA2ARemoteStatusUnknown 表示远端返回未知枚举状态。
	TaskA2ARemoteStatusUnknown TaskA2ARemoteStatus = "UNKNOWN"
)

// Terminal 判断远端状态是否已经不可逆地结束。
// 参数：无。
// 返回：完成、失败、拒绝或取消时返回 true。
// 错误：本方法不返回错误。
func (s TaskA2ARemoteStatus) Terminal() bool {
	switch s {
	case TaskA2ARemoteStatusCompleted, TaskA2ARemoteStatusFailed, TaskA2ARemoteStatusRejected, TaskA2ARemoteStatusCanceled:
		return true
	default:
		return false
	}
}

// A2ADispatchStatus 表示持久化下发意图的处理状态。
type A2ADispatchStatus string

const (
	// A2ADispatchPending 表示意图等待领取。
	A2ADispatchPending A2ADispatchStatus = "PENDING"
	// A2ADispatchSending 表示意图已领取且正在发送。
	A2ADispatchSending A2ADispatchStatus = "SENDING"
	// A2ADispatchSent 表示远端已确认接收命令。
	A2ADispatchSent A2ADispatchStatus = "SENT"
	// A2ADispatchCompleted 表示命令和首个远端更新已在本地完成处理。
	A2ADispatchCompleted A2ADispatchStatus = "COMPLETED"
	// A2ADispatchFailed 表示意图以不可重试错误结束。
	A2ADispatchFailed A2ADispatchStatus = "FAILED"
)

// A2AProjectionStatus 表示事件 inbox 是否已与业务投影在同一事务完成。
type A2AProjectionStatus string

const (
	// A2AProjectionProjected 表示事件和业务投影已在同一事务提交。
	A2AProjectionProjected A2AProjectionStatus = "PROJECTED"
)

// TaskA2ARound 保存一次 GraphQL A2A 操作及其远端任务绑定。
type TaskA2ARound struct {
	ID               string              `json:"id"`
	TaskID           string              `json:"taskId"`
	ExecutionID      string              `json:"executionId"`
	Attempt          int                 `json:"attempt"`
	Turn             int                 `json:"turn"`
	Operation        TaskA2AOperation    `json:"operation"`
	WorkerID         string              `json:"workerId"`
	CommandID        string              `json:"commandId"`
	ParentRoundID    string              `json:"parentRoundId,omitempty"`
	A2ATaskID        string              `json:"a2aTaskId,omitempty"`
	ContextID        string              `json:"contextId,omitempty"`
	RemoteStatus     TaskA2ARemoteStatus `json:"remoteStatus"`
	LastSequence     int64               `json:"lastSequence"`
	LastSyncedAt     *time.Time          `json:"lastSyncedAt,omitempty"`
	LastNetworkAt    *time.Time          `json:"lastNetworkAt,omitempty"`
	UnreachableSince *time.Time          `json:"unreachableSince,omitempty"`
	UnknownSince     *time.Time          `json:"unknownSince,omitempty"`
	ErrorCode        string              `json:"errorCode,omitempty"`
	ErrorMessage     string              `json:"errorMessage,omitempty"`
	Retryable        bool                `json:"retryable"`
	Version          int                 `json:"version"`
	CreatedAt        time.Time           `json:"createdAt"`
	UpdatedAt        time.Time           `json:"updatedAt"`
	CompletedAt      *time.Time          `json:"completedAt,omitempty"`
}

// NewTaskA2ARoundInput 包含创建 A2A round 所需的稳定身份和时间。
type NewTaskA2ARoundInput struct {
	ID            string
	TaskID        string
	ExecutionID   string
	Attempt       int
	Turn          int
	Operation     TaskA2AOperation
	WorkerID      string
	CommandID     string
	ParentRoundID string
	ContextID     string
	LastSequence  int64
	Now           time.Time
}

// NewTaskA2ARound 创建并校验一个待绑定远端 Task 的 round。
// 参数：input 提供本地任务、execution、attempt、turn、操作、Worker 和命令身份。
// 返回：合法时返回初始版本为 1 的 round。
// 错误：必填身份为空、序号小于 1 或操作不受支持时返回校验错误。
func NewTaskA2ARound(input NewTaskA2ARoundInput) (*TaskA2ARound, error) {
	for name, value := range map[string]string{
		"round id": input.ID, "task id": input.TaskID, "execution id": input.ExecutionID,
		"worker id": input.WorkerID, "command id": input.CommandID,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	if input.Attempt < 1 || input.Turn < 1 {
		return nil, fmt.Errorf("attempt and turn must be positive")
	}
	if input.LastSequence < 0 {
		return nil, fmt.Errorf("last sequence cannot be negative")
	}
	if !input.Operation.CreatesRound() {
		return nil, fmt.Errorf("unsupported a2a operation %q", input.Operation)
	}
	return &TaskA2ARound{
		ID: input.ID, TaskID: input.TaskID, ExecutionID: input.ExecutionID,
		Attempt: input.Attempt, Turn: input.Turn, Operation: input.Operation,
		WorkerID: input.WorkerID, CommandID: input.CommandID, ParentRoundID: input.ParentRoundID,
		ContextID: input.ContextID, LastSequence: input.LastSequence, RemoteStatus: TaskA2ARemoteStatusUnspecified, Version: 1,
		CreatedAt: input.Now, UpdatedAt: input.Now,
	}, nil
}

// BindRemote 记录 Worker 分配的 A2A task/context，并推进 OCC 版本。
// 参数：taskID 和 contextID 是官方 A2A 标准身份，status 是首次远端状态，now 是同步时间。
// 返回：无。
// 错误：远端 task/context 为空，或已有绑定与新值冲突时返回错误。
func (r *TaskA2ARound) BindRemote(taskID, contextID string, status TaskA2ARemoteStatus, now time.Time) error {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(contextID) == "" {
		return fmt.Errorf("a2a task id and context id are required")
	}
	if r.A2ATaskID != "" && r.A2ATaskID != taskID {
		return fmt.Errorf("%w: a2a task binding changed", ErrConflict)
	}
	if r.ContextID != "" && r.ContextID != contextID {
		return fmt.Errorf("%w: a2a context binding changed", ErrConflict)
	}
	r.A2ATaskID = taskID
	r.ContextID = contextID
	if status == TaskA2ARemoteStatusUnknown || status == TaskA2ARemoteStatusUnspecified {
		r.UnknownSince = timePointer(now)
	} else if status != "" {
		r.RemoteStatus = status
		r.UnknownSince = nil
	}
	r.LastSyncedAt = timePointer(now)
	r.LastNetworkAt = timePointer(now)
	r.UnreachableSince = nil
	r.Version++
	r.UpdatedAt = now
	if status.Terminal() {
		r.CompletedAt = timePointer(now)
	}
	return nil
}

// ApplyRemoteSnapshot 更新远端状态、sequence 和网络活跃时间，并阻止终态回退。
// 参数：status 是远端状态，sequence 是 execution 全局序号，now 是同步时间。
// 返回：成功接收快照时返回 true，以确保网络活跃时间通过 OCC 持久化。
// 错误：sequence 回退、已有终态被不同状态覆盖时返回协议冲突。
func (r *TaskA2ARound) ApplyRemoteSnapshot(status TaskA2ARemoteStatus, sequence int64, now time.Time) (bool, error) {
	return r.applyRemoteObservation(status, sequence, true, now)
}

// ApplyRemoteEvent 推进 execution 事件游标，但不把缺失状态误判为未知状态。
// 参数：sequence 是 execution 全局序号，now 是接收事件的网络时间。
// 返回：事件游标和网络活跃时间成功持久化时返回 true。
// 错误：sequence 回退时返回协议冲突。
func (r *TaskA2ARound) ApplyRemoteEvent(sequence int64, now time.Time) (bool, error) {
	return r.applyRemoteObservation("", sequence, false, now)
}

func (r *TaskA2ARound) applyRemoteObservation(status TaskA2ARemoteStatus, sequence int64, statusObserved bool, now time.Time) (bool, error) {
	if sequence < r.LastSequence {
		return false, fmt.Errorf("%w: a2a sequence regressed from %d to %d", ErrConflict, r.LastSequence, sequence)
	}
	if statusObserved && r.RemoteStatus.Terminal() && status != "" && status != TaskA2ARemoteStatusUnknown && status != TaskA2ARemoteStatusUnspecified && status != r.RemoteStatus {
		return false, fmt.Errorf("%w: terminal a2a status %s cannot become %s", ErrConflict, r.RemoteStatus, status)
	}
	knownStatus := statusObserved && status != "" && status != TaskA2ARemoteStatusUnknown && status != TaskA2ARemoteStatusUnspecified
	unknownStatus := statusObserved && (status == TaskA2ARemoteStatusUnknown || status == TaskA2ARemoteStatusUnspecified || status == "")
	changed := sequence > r.LastSequence || knownStatus && status != r.RemoteStatus
	if unknownStatus && r.UnknownSince == nil {
		r.UnknownSince = timePointer(now)
		changed = true
	}
	if knownStatus && r.UnknownSince != nil {
		r.UnknownSince = nil
		changed = true
	}
	if !changed {
		r.LastNetworkAt = timePointer(now)
		r.UnreachableSince = nil
		r.Version++
		r.UpdatedAt = now
		return true, nil
	}
	if knownStatus {
		r.RemoteStatus = status
	}
	r.LastSequence = sequence
	r.LastSyncedAt = timePointer(now)
	r.LastNetworkAt = timePointer(now)
	r.UnreachableSince = nil
	r.Version++
	r.UpdatedAt = now
	if r.RemoteStatus.Terminal() {
		r.CompletedAt = timePointer(now)
	}
	return true, nil
}

// MarkUnreachable 记录首次不可达时间，供 60 秒宽限策略使用。
// 参数：now 是本次探测失败时间。
// 返回：首次进入不可达时返回 true，重复失败返回 false。
// 错误：本方法不返回错误。
func (r *TaskA2ARound) MarkUnreachable(now time.Time) bool {
	first := r.UnreachableSince == nil
	if first {
		r.UnreachableSince = timePointer(now)
	}
	// 每次失败观测都推进版本，防止重复写者以旧版本绕过 OCC。
	r.Version++
	r.UpdatedAt = now
	return first
}

// SetError 保存已脱敏的协议错误并结束 round。
// 参数：code 和 message 是稳定错误码及安全描述，now 是完成时间。
// 返回：无。
// 错误：本方法不返回错误。
func (r *TaskA2ARound) SetError(code, message string, now time.Time) {
	r.ErrorCode = code
	r.ErrorMessage = message
	r.CompletedAt = timePointer(now)
	r.Version++
	r.UpdatedAt = now
}

// TaskA2ADispatchIntent 保存网络发送前必须持久化的非敏感命令意图。
type TaskA2ADispatchIntent struct {
	ID            string            `json:"id"`
	RoundID       string            `json:"roundId"`
	TaskID        string            `json:"taskId"`
	ExecutionID   string            `json:"executionId"`
	WorkerID      string            `json:"workerId"`
	CommandID     string            `json:"commandId"`
	Operation     TaskA2AOperation  `json:"operation"`
	Status        A2ADispatchStatus `json:"status"`
	Payload       []byte            `json:"payload,omitempty"`
	AttemptCount  int               `json:"attemptCount"`
	AvailableAt   time.Time         `json:"availableAt"`
	LastAttemptAt *time.Time        `json:"lastAttemptAt,omitempty"`
	SentAt        *time.Time        `json:"sentAt,omitempty"`
	CompletedAt   *time.Time        `json:"completedAt,omitempty"`
	ErrorCode     string            `json:"errorCode,omitempty"`
	ErrorMessage  string            `json:"errorMessage,omitempty"`
	Version       int               `json:"version"`
	CreatedAt     time.Time         `json:"createdAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
}

// NewTaskA2ADispatchIntent 创建一个可由 reconciler 领取的持久化意图。
// 参数：round 提供已校验的执行关联；operation 和 commandID 标识本次操作；payload 只能包含非敏感参数；now 是创建时间。
// 返回：合法时返回 PENDING 状态、版本为 1 的意图。
// 错误：round 为空、操作不合法或 commandID 为空时返回错误。
func NewTaskA2ADispatchIntent(round *TaskA2ARound, operation TaskA2AOperation, commandID string, payload []byte, now time.Time) (*TaskA2ADispatchIntent, error) {
	if round == nil {
		return nil, fmt.Errorf("a2a round is required")
	}
	if !operation.Valid() {
		return nil, fmt.Errorf("unsupported a2a operation %q", operation)
	}
	if strings.TrimSpace(commandID) == "" {
		return nil, fmt.Errorf("command id is required")
	}
	return &TaskA2ADispatchIntent{
		ID: "dispatch_" + commandID, RoundID: round.ID, TaskID: round.TaskID,
		ExecutionID: round.ExecutionID, WorkerID: round.WorkerID, CommandID: commandID,
		Operation: operation, Status: A2ADispatchPending, Payload: append([]byte(nil), payload...),
		AvailableAt: now, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// StartAttempt 使用 OCC 语义把待处理或超时发送中的意图标记为 SENDING。
// 参数：now 是领取时间。
// 返回：无。
// 错误：终态意图不可重新领取时返回状态冲突。
func (i *TaskA2ADispatchIntent) StartAttempt(now time.Time) error {
	if i.Status != A2ADispatchPending && i.Status != A2ADispatchSending {
		return fmt.Errorf("%w: dispatch %s is %s", ErrConflict, i.ID, i.Status)
	}
	i.Status = A2ADispatchSending
	i.AttemptCount++
	i.LastAttemptAt = timePointer(now)
	i.Version++
	i.UpdatedAt = now
	return nil
}

// MarkSent 标记控制请求已获得远端响应或已建立流。
// 参数：now 是发送确认时间。
// 返回：无。
// 错误：仅 SENDING 意图可标记为 SENT，否则返回状态冲突。
func (i *TaskA2ADispatchIntent) MarkSent(now time.Time) error {
	if i.Status != A2ADispatchSending {
		return fmt.Errorf("%w: dispatch %s is %s", ErrConflict, i.ID, i.Status)
	}
	i.Status = A2ADispatchSent
	i.SentAt = timePointer(now)
	i.Version++
	i.UpdatedAt = now
	return nil
}

// ScheduleRetry 保存已脱敏错误并把意图重新置为待处理。
// 参数：code/message 是安全错误信息，availableAt 是下次可领取时间，now 是更新时间。
// 返回：无。
// 错误：已完成意图不可重试时返回状态冲突。
func (i *TaskA2ADispatchIntent) ScheduleRetry(code, message string, availableAt, now time.Time) error {
	if i.Status == A2ADispatchCompleted {
		return fmt.Errorf("%w: dispatch %s already completed", ErrConflict, i.ID)
	}
	i.Status = A2ADispatchPending
	i.ErrorCode = code
	i.ErrorMessage = message
	i.AvailableAt = availableAt
	i.Version++
	i.UpdatedAt = now
	return nil
}

// Complete 标记命令已经被远端幂等接收并完成本地处理。
// 参数：now 是完成时间。
// 返回：无。
// 错误：本方法不返回错误。
func (i *TaskA2ADispatchIntent) Complete(now time.Time) {
	i.Status = A2ADispatchCompleted
	i.CompletedAt = timePointer(now)
	i.ErrorCode = ""
	i.ErrorMessage = ""
	i.Version++
	i.UpdatedAt = now
}

// Fail 以不可重试错误结束意图。
// 参数：code/message 是已脱敏错误，now 是完成时间。
// 返回：无。
// 错误：本方法不返回错误。
func (i *TaskA2ADispatchIntent) Fail(code, message string, now time.Time) {
	i.Status = A2ADispatchFailed
	i.ErrorCode = code
	i.ErrorMessage = message
	i.CompletedAt = timePointer(now)
	i.Version++
	i.UpdatedAt = now
}

// A2AEventInbox 保存远端事件的幂等证明和原子投影结果。
type A2AEventInbox struct {
	RoundID          string              `json:"roundId"`
	ExecutionID      string              `json:"executionId"`
	EventID          string              `json:"eventId"`
	PayloadHash      string              `json:"payloadHash"`
	Sequence         int64               `json:"sequence"`
	EventType        string              `json:"eventType"`
	ProjectionStatus A2AProjectionStatus `json:"projectionStatus"`
	ProjectedAt      time.Time           `json:"projectedAt"`
	CreatedAt        time.Time           `json:"createdAt"`
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}
