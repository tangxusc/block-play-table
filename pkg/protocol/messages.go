package protocol

import (
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

type MessageType string

const (
	MessageTaskStart          MessageType = "TASK_START"
	MessageTaskInterrupt      MessageType = "TASK_INTERRUPT"
	MessageTaskCancel         MessageType = "TASK_CANCEL"
	MessageWorkerConfigUpdate MessageType = "WORKER_CONFIG_UPDATE"
	MessageWorkerRegister     MessageType = "WORKER_REGISTER"
	MessageWorkerHeartbeat    MessageType = "WORKER_HEARTBEAT"
	MessageTaskAccepted       MessageType = "TASK_ACCEPTED"
	MessageTaskStarted        MessageType = "TASK_STARTED"
	MessageTaskLog            MessageType = "TASK_LOG"
	MessageTaskConversation   MessageType = "TASK_CONVERSATION"
	MessageTaskWaitingInput   MessageType = "TASK_WAITING_INPUT"
	MessageTaskInterrupted    MessageType = "TASK_INTERRUPTED"
	MessageTaskCompleted      MessageType = "TASK_COMPLETED"
	MessageTaskFailed         MessageType = "TASK_FAILED"
	MessageTaskResult         MessageType = "TASK_RESULT"
	MessagePing               MessageType = "PING"
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

type TaskPayload struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	AgentType    domain.AgentType `json:"agentType"`
	BaseBranch   string           `json:"baseBranch"`
	TargetBranch string           `json:"targetBranch"`
	PreCommands  []string         `json:"preCommands"`
	PostCommands []string         `json:"postCommands"`
}

type ProjectPayload struct {
	ID                 string   `json:"id"`
	GitURL             string   `json:"gitUrl"`
	DefaultBranch      string   `json:"defaultBranch"`
	WorktreeNamePrefix string   `json:"worktreeNamePrefix"`
	SetupCommands      []string `json:"setupCommands"`
}

type RuntimeEnvVar struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}

type WorkerEvent struct {
	MessageID string            `json:"messageId"`
	Type      MessageType       `json:"type"`
	WorkerID  string            `json:"workerId,omitempty"`
	TaskID    string            `json:"taskId,omitempty"`
	Stream    string            `json:"stream,omitempty"`
	Content   string            `json:"content,omitempty"`
	Result    string            `json:"result,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}
