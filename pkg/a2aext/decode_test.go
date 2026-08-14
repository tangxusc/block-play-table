package a2aext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStrictDecodersRejectUnknownFieldsAndTrailingJSON(t *testing.T) {
	requestRaw, err := json.Marshal(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeExecutionRequest(requestRaw); err != nil {
		t.Fatalf("合法 request 解码失败: %v", err)
	}
	var requestObject map[string]any
	if err := json.Unmarshal(requestRaw, &requestObject); err != nil {
		t.Fatal(err)
	}
	agent := requestObject["agent"].(map[string]any)
	config := agent["config"].(map[string]any)
	config["unknownConfig"] = true
	unknownRequest, _ := json.Marshal(requestObject)
	if _, err := DecodeExecutionRequest(unknownRequest); err == nil {
		t.Fatal("未知嵌套 request 字段应被拒绝")
	}
	if _, err := DecodeExecutionRequest(append(requestRaw, []byte(` {}`)...)); err == nil {
		t.Fatal("request 尾随 JSON 应被拒绝")
	}

	eventRaw, err := json.Marshal(validExecutionEvent(EventExecutionTerminal, map[string]any{"status": string(TerminalCompleted)}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeExecutionEvent(eventRaw); err != nil {
		t.Fatalf("合法 event 解码失败: %v", err)
	}
	var eventObject map[string]any
	if err := json.Unmarshal(eventRaw, &eventObject); err != nil {
		t.Fatal(err)
	}
	eventObject["scope"].(map[string]any)["unknownScope"] = "x"
	unknownEvent, _ := json.Marshal(eventObject)
	if _, err := DecodeExecutionEvent(unknownEvent); err == nil {
		t.Fatal("未知嵌套 event 字段应被拒绝")
	}

	metadata := ArtifactMetadata{
		Kind: ArtifactKind, Version: Version, Role: ArtifactResult, ExecutionID: "execution-1",
		Attempt: 1, Turn: 1, EventID: "018f0000-0000-7000-8000-000000000001",
		Sequence: 1, FinalChunk: true, CreatedAt: time.Now().UTC(),
	}
	metadataRaw, _ := json.Marshal(metadata)
	if _, err := DecodeArtifactMetadata(metadataRaw); err != nil {
		t.Fatalf("合法 metadata 解码失败: %v", err)
	}
	var metadataObject map[string]any
	_ = json.Unmarshal(metadataRaw, &metadataObject)
	metadataObject["unknown"] = true
	unknownMetadata, _ := json.Marshal(metadataObject)
	if _, err := DecodeArtifactMetadata(unknownMetadata); err == nil {
		t.Fatal("未知 metadata 字段应被拒绝")
	}
	if _, err := DecodeArtifactMetadata(append(metadataRaw, []byte(` null`)...)); err == nil {
		t.Fatal("metadata 尾随 JSON 应被拒绝")
	}
}

func TestDecodeExecutionEventEnforcesRawBoundary(t *testing.T) {
	eventRaw, err := json.Marshal(validExecutionEvent(EventExecutionTerminal, map[string]any{"status": string(TerminalCompleted)}))
	if err != nil {
		t.Fatal(err)
	}
	atBoundary := append(eventRaw, []byte(strings.Repeat(" ", MaxAgentEventBytes-len(eventRaw)))...)
	if _, err := DecodeExecutionEvent(atBoundary); err != nil {
		t.Fatalf("恰好 16 MiB 的 event 应进入严格解码: %v", err)
	}
	overBoundary := append(atBoundary, ' ')
	if _, err := DecodeExecutionEvent(overBoundary); err == nil || !strings.Contains(err.Error(), "JSON 大小不能超过") {
		t.Fatalf("超过 16 MiB 的 event 应在解码前被拒绝: %v", err)
	}
}

func TestDecodeArtifactMetadataRequiresExplicitSchemaFields(t *testing.T) {
	logMetadata := ArtifactMetadata{
		Kind: ArtifactKind, Version: Version, Role: ArtifactLog, ExecutionID: "execution-1",
		Attempt: 1, Turn: 1, EventID: "018f0000-0000-7000-8000-000000000001", Sequence: 1,
		Stream: LogStdout, ChunkIndex: 0, FinalChunk: false, CreatedAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(logMetadata)
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"kind", "version", "role", "executionId", "attempt", "turn", "eventId", "sequence", "createdAt",
		"stream", "chunkIndex", "finalChunk",
	} {
		t.Run(field, func(t *testing.T) {
			candidate := make(map[string]any, len(original))
			for key, value := range original {
				candidate[key] = value
			}
			delete(candidate, field)
			candidateRaw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeArtifactMetadata(candidateRaw); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("缺少 %s 应失败: %v", field, err)
			}
		})
	}
	resultMetadata := make(map[string]any, len(original))
	for key, value := range original {
		resultMetadata[key] = value
	}
	resultMetadata["role"] = string(ArtifactResult)
	delete(resultMetadata, "stream")
	delete(resultMetadata, "chunkIndex")
	delete(resultMetadata, "finalChunk")
	resultRaw, err := json.Marshal(resultMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeArtifactMetadata(resultRaw); err != nil {
		t.Fatalf("非日志 Artifact 不应要求日志专属字段: %v", err)
	}
}

func TestValidateEventRejectsSchemaDrift(t *testing.T) {
	cases := []struct {
		name  string
		event *ExecutionEvent
	}{
		{
			name:  "日志未知字段",
			event: validExecutionEvent(EventLogChunk, map[string]any{"stream": "stdout", "content": "ok", "extra": true}),
		},
		{
			name:  "日志索引非整数",
			event: validExecutionEvent(EventLogChunk, map[string]any{"stream": "stdout", "content": "ok", "chunkIndex": 1.5}),
		},
		{
			name:  "会话未知字段",
			event: validExecutionEvent(EventConversationMessage, map[string]any{"content": "ok", "extra": true}),
		},
		{
			name:  "交互未知字段",
			event: validExecutionEvent(EventInteractionRequested, map[string]any{"interactionId": "i", "kind": "USER_INPUT", "extra": true}),
		},
		{
			name:  "结果未知字段",
			event: validExecutionEvent(EventResultUpdated, map[string]any{"result": "ok", "extra": true}),
		},
		{
			name:  "诊断未知字段",
			event: validExecutionEvent(EventExecutionDiagnostic, map[string]any{"extra": true}),
		},
		{
			name:  "终态未知字段",
			event: validExecutionEvent(EventExecutionTerminal, map[string]any{"status": "FAILED", "extra": true}),
		},
		{
			name:  "终态错误码过长",
			event: validExecutionEvent(EventExecutionTerminal, map[string]any{"status": "FAILED", "errorCode": strings.Repeat("x", 257)}),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := ValidateEvent(testCase.event); err == nil {
				t.Fatal("畸形 event 应被拒绝")
			}
		})
	}
	runtimeHead := validExecutionEvent(EventExecutionAccepted, map[string]any{})
	runtimeHead.Runtime = &RuntimeInfo{Head: strings.Repeat("h", 257)}
	if err := ValidateEvent(runtimeHead); err == nil {
		t.Fatal("超过 256 字符的 runtime.head 应被拒绝")
	}
}

func TestValidateEventEnforcesEmptyDiagnosticAndTerminalPayloadSchemas(t *testing.T) {
	emptyPayloadEvents := []*ExecutionEvent{
		validExecutionEvent(EventExecutionAccepted, map[string]any{"extra": true}),
		validExecutionEvent(EventWorkspaceReady, map[string]any{"extra": true}),
		validExecutionEvent(EventAgentSessionStarted, map[string]any{"extra": true}),
		validExecutionEvent(EventAgentSessionUpdated, map[string]any{"extra": true}),
	}
	emptyPayloadEvents[1].Runtime = &RuntimeInfo{WorktreePath: "/tmp/worktree"}
	for _, event := range emptyPayloadEvents[2:] {
		event.Runtime = &RuntimeInfo{AgentSessionID: "session-1"}
	}
	for _, event := range emptyPayloadEvents {
		if err := ValidateEvent(event); err == nil || !strings.Contains(err.Error(), "payload.extra") {
			t.Fatalf("%s 的未知 payload 应被拒绝: %v", event.Event.Type, err)
		}
	}
	if err := ValidateEvent(validExecutionEvent(EventExecutionDiagnostic, map[string]any{})); err == nil {
		t.Fatal("diagnostic 缺少 errorCode/message 应失败")
	}
	for _, payload := range []map[string]any{{"errorCode": "CODE"}, {"message": "detail"}} {
		if err := ValidateEvent(validExecutionEvent(EventExecutionDiagnostic, payload)); err != nil {
			t.Fatalf("合法 diagnostic 被拒绝: %v", err)
		}
	}
	for _, status := range []TerminalStatus{TerminalCompleted, TerminalFailed, TerminalRejected, TerminalCanceled} {
		if err := ValidateEvent(validExecutionEvent(EventExecutionTerminal, map[string]any{"status": string(status)})); err != nil {
			t.Fatalf("仅包含 schema 必填 status 的 terminal %s 被拒绝: %v", status, err)
		}
	}
}
