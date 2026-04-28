package protocol

import (
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

type MessageType string

const (
	MessageTaskStart               MessageType = "TASK_START"
	MessageTaskContinue            MessageType = "TASK_CONTINUE"
	MessageTaskInterrupt           MessageType = "TASK_INTERRUPT"
	MessageTaskCancel              MessageType = "TASK_CANCEL"
	MessageWorkerConfigUpdate      MessageType = "WORKER_CONFIG_UPDATE"
	MessageWorkerRegister          MessageType = "WORKER_REGISTER"
	MessageWorkerHeartbeat         MessageType = "WORKER_HEARTBEAT"
	MessageTaskAccepted            MessageType = "TASK_ACCEPTED"
	MessageTaskStarted             MessageType = "TASK_STARTED"
	MessageTaskLog                 MessageType = "TASK_LOG"
	MessageTaskConversation        MessageType = "TASK_CONVERSATION"
	MessageTaskWaitingInput        MessageType = "TASK_WAITING_INPUT"
	MessageTaskInteractionRequest  MessageType = "TASK_INTERACTION_REQUEST"
	MessageTaskInteractionResponse MessageType = "TASK_INTERACTION_RESPONSE"
	MessageTaskInteractionResolved MessageType = "TASK_INTERACTION_RESOLVED"
	MessageTaskInterrupted         MessageType = "TASK_INTERRUPTED"
	MessageTaskCompleted           MessageType = "TASK_COMPLETED"
	MessageTaskFailed              MessageType = "TASK_FAILED"
	MessageTaskResult              MessageType = "TASK_RESULT"
	MessagePing                    MessageType = "PING"
)

type Envelope struct {
	MessageID string      `json:"messageId"`
	Type      MessageType `json:"type"`
	WorkerID  string      `json:"workerId,omitempty"`
	TaskID    string      `json:"taskId,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   any         `json:"payload,omitempty"`
}

type TaskStartPayload struct {
	Task            TaskPayload       `json:"task"`
	Project         ProjectPayload    `json:"project"`
	AgentRuntimeEnv []RuntimeEnvVar   `json:"agentRuntimeEnv"`
	Settings        map[string]string `json:"settings,omitempty"`
}

type TaskContinuePayload struct {
	Task            TaskPayload       `json:"task"`
	Project         ProjectPayload    `json:"project"`
	Message         string            `json:"message"`
	AgentSessionID  string            `json:"agentSessionId"`
	WorktreePath    string            `json:"worktreePath"`
	AgentRuntimeEnv []RuntimeEnvVar   `json:"agentRuntimeEnv"`
	Settings        map[string]string `json:"settings,omitempty"`
}

type TaskPayload struct {
	ID           string                      `json:"id"`
	Title        string                      `json:"title"`
	Description  string                      `json:"description"`
	AgentType    domain.AgentType            `json:"agentType"`
	AgentConfig  domain.AgentExecutionConfig `json:"agentConfig,omitempty"`
	BaseBranch   string                      `json:"baseBranch"`
	PreCommands  []string                    `json:"preCommands"`
	PostCommands []string                    `json:"postCommands"`
}

type ProjectPayload struct {
	ID                 string `json:"id"`
	GitURL             string `json:"gitUrl"`
	DefaultBranch      string `json:"defaultBranch"`
	WorktreeNamePrefix string `json:"worktreeNamePrefix"`
}

type RuntimeEnvVar struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}

type TaskInteractionRequestPayload struct {
	InteractionID  string                     `json:"interactionId"`
	TaskID         string                     `json:"taskId,omitempty"`
	Kind           domain.TaskInteractionKind `json:"kind"`
	Title          string                     `json:"title"`
	Body           string                     `json:"body"`
	RawPayload     string                     `json:"rawPayload,omitempty"`
	AgentSessionID string                     `json:"agentSessionId,omitempty"`
}

type TaskInteractionResponsePayload struct {
	InteractionID string                         `json:"interactionId"`
	TaskID        string                         `json:"taskId,omitempty"`
	Decision      domain.TaskInteractionDecision `json:"decision,omitempty"`
	Message       string                         `json:"message,omitempty"`
	Payload       string                         `json:"payload,omitempty"`
}

type TaskInteractionResolvedPayload struct {
	InteractionID string                         `json:"interactionId"`
	TaskID        string                         `json:"taskId,omitempty"`
	Responded     bool                           `json:"responded,omitempty"`
	Decision      domain.TaskInteractionDecision `json:"decision,omitempty"`
	Message       string                         `json:"message,omitempty"`
	Payload       string                         `json:"payload,omitempty"`
}

type WorkerEvent struct {
	MessageID      string                         `json:"messageId"`
	Type           MessageType                    `json:"type"`
	WorkerID       string                         `json:"workerId,omitempty"`
	TaskID         string                         `json:"taskId,omitempty"`
	InteractionID  string                         `json:"interactionId,omitempty"`
	Kind           domain.TaskInteractionKind     `json:"kind,omitempty"`
	Title          string                         `json:"title,omitempty"`
	Body           string                         `json:"body,omitempty"`
	RawPayload     string                         `json:"rawPayload,omitempty"`
	Responded      bool                           `json:"responded,omitempty"`
	Decision       domain.TaskInteractionDecision `json:"decision,omitempty"`
	Message        string                         `json:"message,omitempty"`
	Payload        string                         `json:"payload,omitempty"`
	Stream         string                         `json:"stream,omitempty"`
	Content        string                         `json:"content,omitempty"`
	Result         string                         `json:"result,omitempty"`
	AgentSessionID string                         `json:"agentSessionId,omitempty"`
	Metadata       map[string]string              `json:"metadata,omitempty"`
	CreatedAt      time.Time                      `json:"createdAt"`
}
