package a2aext

import (
	"encoding/json"
	"time"
)

const (
	// ExtensionURI 是 execution v1 扩展在 Agent Card 和消息中的稳定标识。
	ExtensionURI = "https://tangxusc.github.io/block-play-table/a2a/extensions/execution/v1"
	// Version 是 execution 扩展的契约版本。
	Version = "1.0"
	// RequestKind 是执行请求 DataPart 的固定类型。
	RequestKind = "bpt.execution.request"
	// EventKind 是执行事件 DataPart 的固定类型。
	EventKind = "bpt.execution.event"
	// ArtifactKind 是 Artifact metadata 的固定类型。
	ArtifactKind = "bpt.execution.artifact"
	// SensitivePlaceholder 是敏感环境变量持久化时使用的固定占位符。
	SensitivePlaceholder = "[REDACTED]"
	// MaxLogChunkBytes 是单个日志 Artifact 分块允许的最大字节数。
	MaxLogChunkBytes = 32 * 1024
	// MaxAgentEventBytes 是单条 CLI JSON 事件允许的最大字节数。
	MaxAgentEventBytes = 16 * 1024 * 1024
)

// Operation 表示 Manager 下发的执行操作。
type Operation string

const (
	// OperationStart 创建新的首次执行。
	OperationStart Operation = "START"
	// OperationRetry 创建新的重试执行。
	OperationRetry Operation = "RETRY"
	// OperationContinue 在既有会话上创建新的 turn。
	OperationContinue Operation = "CONTINUE"
	// OperationInteractionResponse 回复既有 A2A Task 的交互请求。
	OperationInteractionResponse Operation = "INTERACTION_RESPONSE"
)

// AgentType 表示 Worker 适配的 CLI 类型。
type AgentType string

const (
	// AgentCodex 表示 Codex CLI。
	AgentCodex AgentType = "codex"
	// AgentClaude 表示 Claude Code CLI。
	AgentClaude AgentType = "claude"
)

// WorkMode 表示 Agent 本轮工作的意图模式。
type WorkMode string

const (
	// WorkModePlan 表示只生成计划。
	WorkModePlan WorkMode = "plan"
	// WorkModeImplement 表示实现并验证改动。
	WorkModeImplement WorkMode = "implement"
	// WorkModeReview 表示审查并报告问题。
	WorkModeReview WorkMode = "review"
)

// WorktreeMode 表示本轮对 worktree 的处理方式。
type WorktreeMode string

const (
	// WorktreeCreate 表示创建新 worktree。
	WorktreeCreate WorktreeMode = "CREATE"
	// WorktreeRecreate 表示为重试重建 worktree。
	WorktreeRecreate WorktreeMode = "RECREATE"
	// WorktreeResume 表示复用既有 worktree。
	WorktreeResume WorktreeMode = "RESUME"
)

// InteractionDecision 表示用户对 Agent 交互请求的决定。
type InteractionDecision string

const (
	// DecisionApprove 表示仅批准当前请求。
	DecisionApprove InteractionDecision = "APPROVE"
	// DecisionApproveForSession 表示在当前会话持续批准。
	DecisionApproveForSession InteractionDecision = "APPROVE_FOR_SESSION"
	// DecisionDeny 表示拒绝请求。
	DecisionDeny InteractionDecision = "DENY"
	// DecisionCancel 表示取消交互及任务。
	DecisionCancel InteractionDecision = "CANCEL"
	// DecisionRespond 表示提供文本或结构化回复。
	DecisionRespond InteractionDecision = "RESPOND"
)

// InteractionKind 表示 Agent 请求用户处理的交互类型。
type InteractionKind string

const (
	// InteractionUserInput 表示 Agent 请求普通用户输入。
	InteractionUserInput InteractionKind = "USER_INPUT"
	// InteractionCommandApproval 表示 Agent 请求批准命令执行。
	InteractionCommandApproval InteractionKind = "COMMAND_APPROVAL"
	// InteractionFileApproval 表示 Agent 请求批准文件修改。
	InteractionFileApproval InteractionKind = "FILE_APPROVAL"
	// InteractionPermissionApproval 表示 Agent 请求批准其他工具权限。
	InteractionPermissionApproval InteractionKind = "PERMISSION_APPROVAL"
)

// TerminalStatus 表示 execution.terminal payload 的稳定终态。
type TerminalStatus string

const (
	// TerminalCompleted 表示执行成功完成。
	TerminalCompleted TerminalStatus = "COMPLETED"
	// TerminalFailed 表示执行失败。
	TerminalFailed TerminalStatus = "FAILED"
	// TerminalRejected 表示请求被 Worker 拒绝。
	TerminalRejected TerminalStatus = "REJECTED"
	// TerminalCanceled 表示执行已取消。
	TerminalCanceled TerminalStatus = "CANCELED"
)

// EventType 表示 Worker 可投影事件的闭集类型。
type EventType string

const (
	// EventExecutionAccepted 表示执行已被 Worker 接受。
	EventExecutionAccepted EventType = "execution.accepted"
	// EventWorkspaceReady 表示 worktree 已准备完成。
	EventWorkspaceReady EventType = "workspace.ready"
	// EventAgentSessionStarted 表示 CLI 会话已创建。
	EventAgentSessionStarted EventType = "agent.session.started"
	// EventAgentSessionUpdated 表示 CLI 会话绑定已更新。
	EventAgentSessionUpdated EventType = "agent.session.updated"
	// EventLogChunk 表示新增日志分块。
	EventLogChunk EventType = "log.chunk"
	// EventConversationMessage 表示新增规范化会话消息。
	EventConversationMessage EventType = "conversation.message"
	// EventInteractionRequested 表示 Agent 等待用户输入。
	EventInteractionRequested EventType = "interaction.requested"
	// EventInteractionResolved 表示交互已被回复。
	EventInteractionResolved EventType = "interaction.resolved"
	// EventResultUpdated 表示本轮结果已更新。
	EventResultUpdated EventType = "result.updated"
	// EventExecutionDiagnostic 表示执行诊断信息。
	EventExecutionDiagnostic EventType = "execution.diagnostic"
	// EventExecutionTerminal 表示 execution 进入终态。
	EventExecutionTerminal EventType = "execution.terminal"
)

// ArtifactRole 表示 Artifact 在 execution 中的稳定用途。
type ArtifactRole string

const (
	// ArtifactManifest 表示 execution 的最新恢复快照。
	ArtifactManifest ArtifactRole = "manifest"
	// ArtifactLog 表示可重放日志分块。
	ArtifactLog ArtifactRole = "log"
	// ArtifactConversation 表示规范化对话投影。
	ArtifactConversation ArtifactRole = "conversation"
	// ArtifactInteraction 表示交互请求或回复记录。
	ArtifactInteraction ArtifactRole = "interaction"
	// ArtifactResult 表示 turn 最终结果。
	ArtifactResult ArtifactRole = "result"
	// ArtifactOutput 表示文件、补丁或报告等输出。
	ArtifactOutput ArtifactRole = "output"
	// ArtifactDiagnostic 表示脱敏诊断摘要。
	ArtifactDiagnostic ArtifactRole = "diagnostic"
)

// LogStream 表示日志分块的来源。
type LogStream string

const (
	// LogStdout 表示标准输出。
	LogStdout LogStream = "stdout"
	// LogStderr 表示标准错误。
	LogStderr LogStream = "stderr"
	// LogSystem 表示 Worker 生成的系统日志。
	LogSystem LogStream = "system"
)

// ErrorCode 表示跨 Manager 与 Worker 传递的稳定错误码。
type ErrorCode string

const (
	// ErrorWorkerRestarted 表示 Worker 重启导致内存运行时丢失。
	ErrorWorkerRestarted ErrorCode = "WORKER_RESTARTED"
	// ErrorWorkerUnreachableTimeout 表示 Worker 失联超过恢复窗口。
	ErrorWorkerUnreachableTimeout ErrorCode = "WORKER_UNREACHABLE_TIMEOUT"
	// ErrorA2ATaskNotFound 表示远端 A2A Task 已不存在。
	ErrorA2ATaskNotFound ErrorCode = "A2A_TASK_NOT_FOUND"
	// ErrorA2AProtocolConflict 表示幂等键或事件内容发生协议冲突。
	ErrorA2AProtocolConflict ErrorCode = "A2A_PROTOCOL_CONFLICT"
	// ErrorA2AMigrationRetryRequired 表示旧任务必须由用户重新执行。
	ErrorA2AMigrationRetryRequired ErrorCode = "A2A_MIGRATION_RETRY_REQUIRED"
	// ErrorAgentEventTooLarge 表示 CLI 单条事件超过协议上限。
	ErrorAgentEventTooLarge ErrorCode = "AGENT_EVENT_TOO_LARGE"
	// ErrorAgentUnavailable 表示请求的 Agent 不可执行。
	ErrorAgentUnavailable ErrorCode = "AGENT_UNAVAILABLE"
	// ErrorAuthRequired 表示 Agent CLI 缺少有效认证。
	ErrorAuthRequired ErrorCode = "AUTH_REQUIRED"
)

// ExecutionRequest 是 execution v1 请求 DataPart 的强类型表示。
type ExecutionRequest struct {
	Kind        string       `json:"kind"`
	Version     string       `json:"version"`
	Command     Command      `json:"command"`
	Scope       RequestScope `json:"scope"`
	Task        TaskSpec     `json:"task"`
	Agent       AgentSpec    `json:"agent"`
	Project     ProjectSpec  `json:"project"`
	Worktree    WorktreeSpec `json:"worktree"`
	Commands    Commands     `json:"commands"`
	Environment Environment  `json:"environment"`
	Resume      *Resume      `json:"resume,omitempty"`
	Interaction *Interaction `json:"interaction,omitempty"`
}

// Command 标识一次具有语义幂等性的 Manager 命令。
type Command struct {
	ID        string    `json:"id"`
	Operation Operation `json:"operation"`
	IssuedAt  time.Time `json:"issuedAt"`
}

// RequestScope 保存 Manager 侧关联标识和目标 Worker。
type RequestScope struct {
	LocalTaskID      string `json:"localTaskId"`
	ExecutionID      string `json:"executionId"`
	Attempt          int    `json:"attempt"`
	Turn             int    `json:"turn"`
	ExpectedWorkerID string `json:"expectedWorkerId"`
}

// TaskSpec 描述 Agent 需要完成的业务任务。
type TaskSpec struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	BaseBranch  string `json:"baseBranch"`
}

// AgentSpec 描述目标 CLI、工作模式和强类型配置。
type AgentSpec struct {
	Type     AgentType   `json:"type"`
	WorkMode WorkMode    `json:"workMode"`
	Config   AgentConfig `json:"config"`
}

// AgentConfig 合并 Codex 与 Claude 的互斥配置字段，校验时按 AgentType 限定。
type AgentConfig struct {
	Model                     string `json:"model,omitempty"`
	ReasoningEffort           string `json:"reasoningEffort,omitempty"`
	SandboxMode               string `json:"sandboxMode,omitempty"`
	ApprovalPolicy            string `json:"approvalPolicy,omitempty"`
	FullAuto                  bool   `json:"fullAuto,omitempty"`
	BypassApprovalsAndSandbox bool   `json:"bypassApprovalsAndSandbox,omitempty"`
	Effort                    string `json:"effort,omitempty"`
	PermissionMode            string `json:"permissionMode,omitempty"`
}

// ProjectSpec 描述仓库来源和 worktree 命名信息。
type ProjectSpec struct {
	ID                 string `json:"id"`
	GitURL             string `json:"gitUrl"`
	DefaultBranch      string `json:"defaultBranch"`
	WorktreeNamePrefix string `json:"worktreeNamePrefix"`
}

// WorktreeSpec 描述本轮 worktree 操作。
type WorktreeSpec struct {
	Mode WorktreeMode `json:"mode"`
}

// Commands 保存 worktree 内执行的前置与后置命令。
type Commands struct {
	Pre  []string `json:"pre"`
	Post []string `json:"post"`
}

// Environment 保存本轮注入 CLI 的环境变量。
type Environment struct {
	Variables []EnvironmentVariable `json:"variables"`
}

// EnvironmentVariable 描述普通或敏感环境变量。
type EnvironmentVariable struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}

// Resume 保存 continue 或交互回复时的运行时一致性期望。
type Resume struct {
	AgentSessionID string `json:"agentSessionId"`
	WorktreePath   string `json:"worktreePath"`
}

// Interaction 保存对既有交互请求的回复。
type Interaction struct {
	ID       string              `json:"id"`
	Decision InteractionDecision `json:"decision"`
	Message  string              `json:"message"`
	Payload  string              `json:"payload"`
}

// ExecutionEvent 是 execution v1 事件 DataPart 的强类型表示。
type ExecutionEvent struct {
	Kind    string         `json:"kind"`
	Version string         `json:"version"`
	Event   EventHeader    `json:"event"`
	Scope   EventScope     `json:"scope"`
	Runtime *RuntimeInfo   `json:"runtime,omitempty"`
	Payload map[string]any `json:"payload"`
}

// EventHeader 保存事件的稳定身份、序号和时间。
type EventHeader struct {
	ID         string    `json:"id"`
	Sequence   int64     `json:"sequence"`
	Type       EventType `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
}

// EventScope 保存事件投影所需的 Manager 和 Worker 关联标识。
type EventScope struct {
	LocalTaskID string `json:"localTaskId"`
	ExecutionID string `json:"executionId"`
	Attempt     int    `json:"attempt"`
	Turn        int    `json:"turn"`
	WorkerID    string `json:"workerId"`
}

// RuntimeInfo 保存 Worker 权威的 CLI 会话和 worktree 绑定。
type RuntimeInfo struct {
	AgentSessionID string `json:"agentSessionId,omitempty"`
	WorktreePath   string `json:"worktreePath,omitempty"`
	Branch         string `json:"branch,omitempty"`
	Head           string `json:"head,omitempty"`
}

// ArtifactMetadata 是 execution v1 Artifact metadata 的强类型表示。
type ArtifactMetadata struct {
	Kind        string       `json:"kind"`
	Version     string       `json:"version"`
	Role        ArtifactRole `json:"role"`
	ExecutionID string       `json:"executionId"`
	Attempt     int          `json:"attempt"`
	Turn        int          `json:"turn"`
	EventID     string       `json:"eventId"`
	Sequence    int64        `json:"sequence"`
	Stream      LogStream    `json:"stream,omitempty"`
	ChunkIndex  int64        `json:"chunkIndex"`
	FinalChunk  bool         `json:"finalChunk"`
	MIMEType    string       `json:"mimeType,omitempty"`
	Compacted   bool         `json:"compacted,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
}

// RawPayload 将结构化 payload 编码为延迟解析的 JSON。
type RawPayload = json.RawMessage
