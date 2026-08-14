// Package protocol 定义 Worker 注册通道使用的最小 WebSocket 信封。
package protocol

import "time"

// MessageType 表示 Worker 控制通道允许的消息类型。
type MessageType string

const (
	// MessageWorkerRegister 表示 Worker 首次注册及能力上报。
	MessageWorkerRegister MessageType = "WORKER_REGISTER"
	// MessageWorkerHeartbeat 表示 Worker 存活心跳。
	MessageWorkerHeartbeat MessageType = "WORKER_HEARTBEAT"
	// MessagePing 表示 Manager 主动探测 Worker 连接。
	MessagePing MessageType = "PING"
)

// Envelope 是 Worker 注册、心跳和 PING 的通用 WebSocket 信封。
type Envelope struct {
	MessageID string      `json:"messageId"`
	Type      MessageType `json:"type"`
	WorkerID  string      `json:"workerId,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   any         `json:"payload,omitempty"`
}
