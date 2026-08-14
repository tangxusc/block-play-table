package a2aext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeExecutionRequest 严格解码 execution v1 请求并拒绝未知字段或尾随 JSON。
// 参数：raw 是 DataPart 的完整 JSON 表示。
// 返回：保留敏感原值、尚未进行业务校验的请求。
// 错误：JSON 非法、包含未知字段或尾随非空值时返回错误。
func DecodeExecutionRequest(raw []byte) (*ExecutionRequest, error) {
	var request ExecutionRequest
	if err := decodeStrict(raw, &request); err != nil {
		return nil, fmt.Errorf("严格解码 execution request: %w", err)
	}
	return &request, nil
}

// DecodeExecutionEvent 严格解码 execution v1 事件并拒绝超限、未知结构字段或尾随 JSON。
// 参数：raw 是事件的完整 JSON 表示。
// 返回：尚未进行语义校验的事件；payload 的闭集键由 ValidateEvent 校验。
// 错误：原始数据超限、JSON 非法、结构字段未知或存在尾随非空值时返回错误。
func DecodeExecutionEvent(raw []byte) (*ExecutionEvent, error) {
	if len(raw) > MaxAgentEventBytes {
		return nil, fmt.Errorf("严格解码 execution event: JSON 大小不能超过 %d 字节", MaxAgentEventBytes)
	}
	var event ExecutionEvent
	if err := decodeStrict(raw, &event); err != nil {
		return nil, fmt.Errorf("严格解码 execution event: %w", err)
	}
	return &event, nil
}

// DecodeArtifactMetadata 严格解码 execution v1 Artifact metadata 并拒绝未知字段或尾随 JSON。
// 参数：raw 是 Artifact metadata 的完整 JSON 表示。
// 返回：尚未进行语义校验的 metadata。
// 错误：JSON 非法、包含未知字段或尾随非空值时返回错误。
func DecodeArtifactMetadata(raw []byte) (*ArtifactMetadata, error) {
	var metadata ArtifactMetadata
	if err := decodeStrict(raw, &metadata); err != nil {
		return nil, fmt.Errorf("严格解码 Artifact metadata: %w", err)
	}
	required := []string{"kind", "version", "role", "executionId", "attempt", "turn", "eventId", "sequence", "createdAt"}
	if metadata.Role == ArtifactLog {
		required = append(required, "stream", "chunkIndex", "finalChunk")
	}
	if err := requireJSONFields(raw, required...); err != nil {
		return nil, fmt.Errorf("严格解码 Artifact metadata: %w", err)
	}
	return &metadata, nil
}

func requireJSONFields(raw []byte, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("必须是 JSON 对象")
	}
	for _, field := range required {
		if _, exists := fields[field]; !exists {
			return fmt.Errorf("缺少必填字段 %q", field)
		}
	}
	return nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("存在尾随 JSON 值")
		}
		return err
	}
	return nil
}
