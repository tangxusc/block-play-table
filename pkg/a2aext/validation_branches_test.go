package a2aext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateRequestRejectsEveryContractBoundary(t *testing.T) {
	tooLong := strings.Repeat("x", 1025)
	tests := []struct {
		name   string
		mutate func(*ExecutionRequest)
	}{
		{"kind", func(req *ExecutionRequest) { req.Kind = "other" }},
		{"command id", func(req *ExecutionRequest) { req.Command.ID = "" }},
		{"issued at", func(req *ExecutionRequest) { req.Command.IssuedAt = time.Time{} }},
		{"operation", func(req *ExecutionRequest) { req.Command.Operation = "OTHER" }},
		{"local task", func(req *ExecutionRequest) { req.Scope.LocalTaskID = "" }},
		{"attempt", func(req *ExecutionRequest) { req.Scope.Attempt = 0 }},
		{"title", func(req *ExecutionRequest) { req.Task.Title = "" }},
		{"description", func(req *ExecutionRequest) { req.Task.Description = string([]byte{0xff}) }},
		{"base branch", func(req *ExecutionRequest) { req.Task.BaseBranch = "" }},
		{"agent type", func(req *ExecutionRequest) { req.Agent.Type = "other" }},
		{"work mode", func(req *ExecutionRequest) { req.Agent.WorkMode = "other" }},
		{"model", func(req *ExecutionRequest) { req.Agent.Config.Model = tooLong }},
		{"codex claude config", func(req *ExecutionRequest) { req.Agent.Config.Effort = "high" }},
		{"codex enum", func(req *ExecutionRequest) { req.Agent.Config.ReasoningEffort = "extreme" }},
		{"project git url", func(req *ExecutionRequest) { req.Project.GitURL = "" }},
		{"project url syntax", func(req *ExecutionRequest) { req.Project.GitURL = "%" }},
		{"project credentials", func(req *ExecutionRequest) { req.Project.GitURL = "https://user:secret@example.test/repo" }},
		{"project branch", func(req *ExecutionRequest) { req.Project.DefaultBranch = "" }},
		{"project prefix", func(req *ExecutionRequest) { req.Project.WorktreeNamePrefix = "" }},
		{"command count", func(req *ExecutionRequest) { req.Commands.Pre = make([]string, 1025) }},
		{"command size", func(req *ExecutionRequest) { req.Commands.Post = []string{strings.Repeat("x", 1024*1024+1)} }},
		{"environment count", func(req *ExecutionRequest) { req.Environment.Variables = make([]EnvironmentVariable, 4097) }},
		{"environment key", func(req *ExecutionRequest) { req.Environment.Variables = []EnvironmentVariable{{Key: "1BAD"}} }},
		{"environment duplicate", func(req *ExecutionRequest) {
			req.Environment.Variables = []EnvironmentVariable{{Key: "TOKEN"}, {Key: "TOKEN"}}
		}},
		{"environment value", func(req *ExecutionRequest) {
			req.Environment.Variables = []EnvironmentVariable{{Key: "TOKEN", Value: string([]byte{0xff})}}
		}},
		{"worktree mode", func(req *ExecutionRequest) { req.Worktree.Mode = WorktreeResume }},
		{"unexpected resume", func(req *ExecutionRequest) {
			req.Resume = &Resume{AgentSessionID: "session", WorktreePath: "/tmp/worktree"}
		}},
		{"unexpected interaction", func(req *ExecutionRequest) {
			req.Interaction = &Interaction{ID: "interaction", Decision: DecisionApprove}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := validRequest()
			test.mutate(req)
			if err := ValidateRequest(req, "worker-1"); err == nil {
				t.Fatal("非法请求未被拒绝")
			}
		})
	}
	if err := ValidateRequest(nil, "worker-1"); err == nil {
		t.Fatal("nil 请求未被拒绝")
	}
}

func TestValidateRequestOperationAndAgentVariants(t *testing.T) {
	continueRequest := func() *ExecutionRequest {
		req := validRequest()
		req.Command.Operation = OperationContinue
		req.Worktree.Mode = WorktreeResume
		req.Resume = &Resume{AgentSessionID: "session-1", WorktreePath: "/tmp/worktree"}
		return req
	}
	interactionRequest := func() *ExecutionRequest {
		req := continueRequest()
		req.Command.Operation = OperationInteractionResponse
		req.Interaction = &Interaction{ID: "interaction-1", Decision: DecisionApprove}
		return req
	}
	valid := []*ExecutionRequest{continueRequest(), interactionRequest()}
	retry := validRequest()
	retry.Command.Operation = OperationRetry
	retry.Worktree.Mode = WorktreeRecreate
	valid = append(valid, retry)
	claude := validRequest()
	claude.Agent = AgentSpec{Type: AgentClaude, WorkMode: WorkModePlan, Config: AgentConfig{Effort: "max", PermissionMode: "plan"}}
	valid = append(valid, claude)
	for _, req := range valid {
		if err := ValidateRequest(req, "worker-1"); err != nil {
			t.Fatalf("合法变体被拒绝: %+v: %v", req, err)
		}
	}

	tests := []func(*ExecutionRequest){
		func(req *ExecutionRequest) { req.Resume = nil },
		func(req *ExecutionRequest) { req.Resume.AgentSessionID = "" },
		func(req *ExecutionRequest) { req.Resume.WorktreePath = "" },
		func(req *ExecutionRequest) { req.Interaction = nil },
		func(req *ExecutionRequest) { req.Interaction.ID = "" },
		func(req *ExecutionRequest) { req.Interaction.Decision = "other" },
		func(req *ExecutionRequest) { req.Interaction.Message = string([]byte{0xff}) },
		func(req *ExecutionRequest) { req.Interaction.Payload = string([]byte{0xff}) },
	}
	for index, mutate := range tests {
		req := interactionRequest()
		mutate(req)
		if err := ValidateRequest(req, "worker-1"); err == nil {
			t.Fatalf("非法 continue/interaction 变体 %d 未被拒绝", index)
		}
	}
	for _, mutate := range []func(*ExecutionRequest){
		func(req *ExecutionRequest) { req.Agent.Config.ReasoningEffort = "high" },
		func(req *ExecutionRequest) { req.Agent.Config.Effort = "extreme" },
	} {
		req := validRequest()
		req.Agent = AgentSpec{Type: AgentClaude, WorkMode: WorkModeReview}
		mutate(req)
		if err := ValidateRequest(req, "worker-1"); err == nil {
			t.Fatal("Claude 非法配置未被拒绝")
		}
	}
}

func TestValidateArtifactCoversRoleSpecificBoundaries(t *testing.T) {
	valid := func() *ArtifactMetadata {
		return &ArtifactMetadata{
			Kind: ArtifactKind, Version: Version, Role: ArtifactResult,
			ExecutionID: "execution-1", Attempt: 1, Turn: 1,
			EventID: "018f0000-0000-7000-8000-000000000001", Sequence: 1, CreatedAt: time.Unix(1, 0).UTC(),
		}
	}
	if err := ValidateArtifact(nil); err == nil {
		t.Fatal("nil artifact 未被拒绝")
	}
	tests := []func(*ArtifactMetadata){
		func(metadata *ArtifactMetadata) { metadata.Kind = "other" },
		func(metadata *ArtifactMetadata) { metadata.ExecutionID = "" },
		func(metadata *ArtifactMetadata) { metadata.EventID = "not-uuid" },
		func(metadata *ArtifactMetadata) { metadata.Attempt = 0 },
		func(metadata *ArtifactMetadata) { metadata.Stream = LogStdout },
		func(metadata *ArtifactMetadata) { metadata.MIMEType = string([]byte{0xff}) },
	}
	for index, mutate := range tests {
		metadata := valid()
		mutate(metadata)
		if err := ValidateArtifact(metadata); err == nil {
			t.Fatalf("非法 artifact %d 未被拒绝", index)
		}
	}
	logMetadata := valid()
	logMetadata.Role = ArtifactLog
	logMetadata.Stream = LogStdout
	if err := ValidateArtifact(logMetadata); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ArtifactMetadata){
		func(metadata *ArtifactMetadata) { metadata.Stream = "other" },
		func(metadata *ArtifactMetadata) { metadata.ChunkIndex = -1 },
	} {
		metadata := *logMetadata
		mutate(&metadata)
		if err := ValidateArtifact(&metadata); err == nil {
			t.Fatal("非法日志 artifact 未被拒绝")
		}
	}
}

func TestValidateEventCoversPayloadTypeBoundaries(t *testing.T) {
	if err := ValidateEvent(nil); err == nil {
		t.Fatal("nil event 未被拒绝")
	}
	baseMutations := []func(*ExecutionEvent){
		func(event *ExecutionEvent) { event.Kind = "other" },
		func(event *ExecutionEvent) { event.Event.ID = "not-uuid" },
		func(event *ExecutionEvent) { event.Scope.LocalTaskID = "" },
		func(event *ExecutionEvent) { event.Event.Sequence = 0 },
		func(event *ExecutionEvent) { event.Event.Type = "other" },
		func(event *ExecutionEvent) { event.Event.OccurredAt = time.Time{} },
		func(event *ExecutionEvent) { event.Payload = nil },
		func(event *ExecutionEvent) { event.Runtime = &RuntimeInfo{AgentSessionID: string([]byte{0xff})} },
		func(event *ExecutionEvent) { event.Runtime = &RuntimeInfo{WorktreePath: string([]byte{0xff})} },
		func(event *ExecutionEvent) { event.Runtime = &RuntimeInfo{Branch: string([]byte{0xff})} },
		func(event *ExecutionEvent) { event.Runtime = &RuntimeInfo{Head: string([]byte{0xff})} },
	}
	for index, mutate := range baseMutations {
		event := validExecutionEvent(EventExecutionAccepted, map[string]any{})
		mutate(event)
		if err := ValidateEvent(event); err == nil {
			t.Fatalf("非法基础事件 %d 未被拒绝", index)
		}
	}

	tests := []struct {
		typeID  EventType
		payload map[string]any
		runtime *RuntimeInfo
		valid   bool
	}{
		{EventExecutionAccepted, map[string]any{"extra": true}, nil, false},
		{EventWorkspaceReady, map[string]any{}, nil, false},
		{EventWorkspaceReady, map[string]any{}, &RuntimeInfo{WorktreePath: "/tmp/worktree"}, true},
		{EventAgentSessionUpdated, map[string]any{}, &RuntimeInfo{AgentSessionID: "session"}, true},
		{EventLogChunk, map[string]any{"stream": "other", "content": "x"}, nil, false},
		{EventLogChunk, map[string]any{"stream": "stdout"}, nil, false},
		{EventLogChunk, map[string]any{"stream": "stdout", "content": "x", "chunkIndex": -1}, nil, false},
		{EventLogChunk, map[string]any{"stream": "stdout", "content": "x", "chunkIndex": int8(1), "finalChunk": "yes"}, nil, false},
		{EventConversationMessage, map[string]any{"content": "hello", "role": 1}, nil, false},
		{EventConversationMessage, map[string]any{"content": "hello", "role": "user"}, nil, true},
		{EventInteractionRequested, map[string]any{"interactionId": "", "kind": string(InteractionUserInput)}, nil, false},
		{EventInteractionRequested, map[string]any{"interactionId": "interaction", "kind": "other"}, nil, false},
		{EventInteractionRequested, map[string]any{"interactionId": "interaction", "kind": string(InteractionUserInput), "title": 1}, nil, false},
		{EventInteractionResolved, map[string]any{"interactionId": "interaction", "kind": string(InteractionUserInput), "decision": "other"}, nil, false},
		{EventInteractionResolved, map[string]any{"interactionId": "interaction", "kind": string(InteractionUserInput), "decision": string(DecisionRespond)}, nil, true},
		{EventResultUpdated, map[string]any{}, nil, false},
		{EventExecutionDiagnostic, map[string]any{}, nil, false},
		{EventExecutionDiagnostic, map[string]any{"message": "warning", "retryable": "yes"}, nil, false},
		{EventExecutionDiagnostic, map[string]any{"errorCode": "CODE", "retryable": true}, nil, true},
		{EventExecutionTerminal, map[string]any{"status": "other"}, nil, false},
		{EventExecutionTerminal, map[string]any{"status": string(TerminalFailed), "errorCode": 1}, nil, false},
	}
	for index, test := range tests {
		event := validExecutionEvent(test.typeID, test.payload)
		event.Runtime = test.runtime
		err := ValidateEvent(event)
		if (err == nil) != test.valid {
			t.Fatalf("事件 payload %d 校验结果错误: %v", index, err)
		}
	}
}

func TestPayloadIntegerVariantsAndSensitiveHelpers(t *testing.T) {
	for _, value := range []any{int(0), int8(0), int16(0), int32(0), int64(0), uint(0), uint8(0), uint16(0), uint32(0), uint64(0), float64(0), json.Number("0")} {
		if err := payloadOptionalNonNegativeInteger(map[string]any{"index": value}, "index"); err != nil {
			t.Fatalf("合法整数 %T 被拒绝: %v", value, err)
		}
	}
	for _, value := range []any{-1, 1.5, json.Number("bad"), "1"} {
		if err := payloadOptionalNonNegativeInteger(map[string]any{"index": value}, "index"); err == nil {
			t.Fatalf("非法整数 %#v 未被拒绝", value)
		}
	}

	if NormalizeRequestDefaults(nil) != nil || SensitiveValues(nil) != nil {
		t.Fatal("nil helper 应返回 nil")
	}
	req := validRequest()
	req.Environment.Variables = []EnvironmentVariable{
		{Key: "EMPTY", Sensitive: true},
		{Key: "PLACEHOLDER", Value: SensitivePlaceholder, Sensitive: true},
		{Key: "PUBLIC", Value: "visible"},
		{Key: "A", Value: "same", Sensitive: true},
		{Key: "B", Value: "same", Sensitive: true},
		{Key: "LONG", Value: "longer", Sensitive: true},
	}
	values := SensitiveValues(req)
	if len(values) != 2 || values[0] != "longer" || values[1] != "same" {
		t.Fatalf("敏感值提取结果 = %#v", values)
	}
	if got := NewRedactor([]string{"", SensitivePlaceholder, "secret", "secret"}).Redact("secret"); got != SensitivePlaceholder {
		t.Fatalf("脱敏结果 = %q", got)
	}
	var nilRedactor *Redactor
	if nilRedactor.Redact("plain") != "plain" || nilRedactor.RedactChunk("plain", true) != "plain" {
		t.Fatal("nil redactor 应原样返回")
	}
	if NewRedactor(nil).Redact("plain") != "plain" {
		t.Fatal("空 redactor 应原样返回")
	}
	secrets := []string{"bb", "aa", "long"}
	sortSecrets(secrets)
	if strings.Join(secrets, ",") != "long,aa,bb" {
		t.Fatalf("同长度 secret 排序错误: %#v", secrets)
	}
}

func TestStrictDecodeObjectAndTrailingErrorBranches(t *testing.T) {
	if err := requireJSONFields([]byte("null"), "kind"); err == nil {
		t.Fatal("null 不应被接受为对象")
	}
	if err := requireJSONFields([]byte("{"), "kind"); err == nil {
		t.Fatal("畸形 JSON 未被拒绝")
	}
	var value map[string]any
	if err := decodeStrict([]byte(`{} ???`), &value); err == nil {
		t.Fatal("非法尾随内容未被拒绝")
	}
}
