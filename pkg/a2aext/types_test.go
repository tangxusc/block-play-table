package a2aext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestArtifactMetadataLogRequiredZeroValuesAreSerialized(t *testing.T) {
	metadata := ArtifactMetadata{
		Kind: ArtifactKind, Version: Version, Role: ArtifactLog,
		ExecutionID: "execution-1", Attempt: 1, Turn: 1,
		EventID: "018f0000-0000-7000-8000-000000000001", Sequence: 1, Stream: LogStdout,
		ChunkIndex: 0, FinalChunk: false, CreatedAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, field := range []string{`"chunkIndex":0`, `"finalChunk":false`} {
		if !strings.Contains(text, field) {
			t.Fatalf("日志 metadata 缺少必填字段 %s: %s", field, text)
		}
	}
}

func TestValidateEventAndArtifactRequireUUIDv7(t *testing.T) {
	event := &ExecutionEvent{
		Kind: EventKind, Version: Version,
		Event: EventHeader{
			ID: "018f0000-0000-7000-8000-000000000001", Sequence: 1,
			Type: EventExecutionAccepted, OccurredAt: time.Now().UTC(),
		},
		Scope:   EventScope{LocalTaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker"},
		Payload: map[string]any{},
	}
	if err := ValidateEvent(event); err != nil {
		t.Fatalf("UUIDv7 event 校验失败: %v", err)
	}
	artifact := &ArtifactMetadata{
		Kind: ArtifactKind, Version: Version, Role: ArtifactResult,
		ExecutionID: "execution", Attempt: 1, Turn: 1,
		EventID: event.Event.ID, Sequence: 1, CreatedAt: time.Now().UTC(),
	}
	if err := ValidateArtifact(artifact); err != nil {
		t.Fatalf("UUIDv7 Artifact 校验失败: %v", err)
	}
	for _, invalidID := range []string{"550e8400-e29b-41d4-a716-446655440000", "custom-event-id"} {
		event.Event.ID = invalidID
		if err := ValidateEvent(event); err == nil {
			t.Fatalf("event.id %q 应被拒绝", invalidID)
		}
		artifact.EventID = invalidID
		if err := ValidateArtifact(artifact); err == nil {
			t.Fatalf("Artifact eventId %q 应被拒绝", invalidID)
		}
	}
}

func TestValidateEventPayloadByTypeAndSize(t *testing.T) {
	logEvent := validExecutionEvent(EventLogChunk, map[string]any{
		"stream": string(LogStdout), "content": strings.Repeat("x", MaxLogChunkBytes),
	})
	if err := ValidateEvent(logEvent); err != nil {
		t.Fatalf("边界日志校验失败: %v", err)
	}
	logEvent.Payload["content"] = strings.Repeat("x", MaxLogChunkBytes+1)
	if err := ValidateEvent(logEvent); err == nil {
		t.Fatal("超过 32 KiB 的日志应失败")
	}
	logEvent.Payload = map[string]any{"stream": "unknown", "content": "text"}
	if err := ValidateEvent(logEvent); err == nil {
		t.Fatal("未知日志 stream 应失败")
	}

	interaction := validExecutionEvent(EventInteractionRequested, map[string]any{
		"interactionId": "interaction-1", "kind": string(InteractionCommandApproval),
	})
	if err := ValidateEvent(interaction); err != nil {
		t.Fatalf("合法交互校验失败: %v", err)
	}
	delete(interaction.Payload, "kind")
	if err := ValidateEvent(interaction); err == nil {
		t.Fatal("缺少 kind 的交互应失败")
	}

	terminal := validExecutionEvent(EventExecutionTerminal, map[string]any{"status": string(TerminalCompleted)})
	if err := ValidateEvent(terminal); err != nil {
		t.Fatalf("合法终态校验失败: %v", err)
	}
	terminal.Payload["status"] = "UNKNOWN"
	if err := ValidateEvent(terminal); err == nil {
		t.Fatal("未知 terminal status 应失败")
	}

	conversation := validExecutionEvent(EventConversationMessage, map[string]any{
		"content": strings.Repeat("x", MaxAgentEventBytes),
	})
	if err := ValidateEvent(conversation); err == nil {
		t.Fatal("整体 JSON 超过 16 MiB 应失败")
	}
	conversation.Payload["content"] = string([]byte{0xff})
	if err := ValidateEvent(conversation); err == nil {
		t.Fatal("非法 UTF-8 文本应失败")
	}
}

func TestValidateRuntimeRequiredByWorkspaceAndSessionEvents(t *testing.T) {
	workspace := validExecutionEvent(EventWorkspaceReady, map[string]any{})
	if err := ValidateEvent(workspace); err == nil {
		t.Fatal("workspace.ready 缺少 runtime 应失败")
	}
	workspace.Runtime = &RuntimeInfo{WorktreePath: "/tmp/worktree"}
	if err := ValidateEvent(workspace); err != nil {
		t.Fatalf("workspace.ready 校验失败: %v", err)
	}
	session := validExecutionEvent(EventAgentSessionStarted, map[string]any{})
	session.Runtime = &RuntimeInfo{AgentSessionID: "session-1"}
	if err := ValidateEvent(session); err != nil {
		t.Fatalf("agent.session.started 校验失败: %v", err)
	}
}

func TestNormalizeRequestDefaultsUsesImplementMode(t *testing.T) {
	req := &ExecutionRequest{}
	if got := NormalizeRequestDefaults(req); got != req || req.Agent.WorkMode != WorkModeImplement {
		t.Fatalf("规范化结果 = %+v", got)
	}
}

func TestSanitizeHashAndCrossChunkRedaction(t *testing.T) {
	req := validRequest()
	req.Environment.Variables = []EnvironmentVariable{{Key: "TOKEN", Value: "abcdef", Sensitive: true}}
	copy, err := SanitizeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if copy.Environment.Variables[0].Value != SensitivePlaceholder || req.Environment.Variables[0].Value != "abcdef" {
		t.Fatalf("敏感请求副本错误: copy=%+v original=%+v", copy, req)
	}
	hashA, err := CanonicalPayloadHash(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Environment.Variables[0].Value = "different-secret"
	hashB, err := CanonicalPayloadHash(req)
	if err != nil || hashA != hashB {
		t.Fatalf("敏感值不应改变 hash: %q %q, err=%v", hashA, hashB, err)
	}

	redactor := NewRedactor([]string{"abcdef"})
	if got := redactor.RedactChunk("prefix-abc", false); got != "prefix-" {
		t.Fatalf("首块 = %q", got)
	}
	if got := redactor.RedactChunk("def-suffix", true); got != SensitivePlaceholder+"-suffix" {
		t.Fatalf("末块 = %q", got)
	}
}

func TestValidateRequestAndPaths(t *testing.T) {
	req := validRequest()
	if err := ValidateRequest(req, "worker-1"); err != nil {
		t.Fatalf("合法请求校验失败: %v", err)
	}
	req.Scope.ExpectedWorkerID = "worker-2"
	if err := ValidateRequest(req, "worker-1"); err == nil {
		t.Fatal("目标 Worker 不匹配时应失败")
	}
	root := t.TempDir()
	inside := root + "/task"
	if _, err := ValidateWorktreePath(root, inside); err != nil {
		t.Fatalf("合法 worktree 路径失败: %v", err)
	}
	if _, err := ValidateWorktreePath(root, root+"/../outside"); err == nil {
		t.Fatal("逃逸 worktree 路径应失败")
	}
	outside := t.TempDir()
	link := filepath.Join(root, "linked-outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorktreePath(root, filepath.Join(link, "task")); err == nil {
		t.Fatal("通过符号链接逃逸的 worktree 路径应失败")
	}
}

func TestValidateWorktreePathResolvesSafeAndDanglingLinks(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	safeLink := filepath.Join(root, "safe-link")
	if err := os.Symlink(realDir, safeLink); err != nil {
		t.Fatal(err)
	}
	resolvedRealDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(resolvedRealDir, "pending", "task")
	got, err := ValidateWorktreePath(root, filepath.Join(safeLink, "pending", "task"))
	if err != nil || got != want {
		t.Fatalf("根目录内链接解析结果 = %q, %v；期望 %q", got, err, want)
	}

	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing-target"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorktreePath(root, filepath.Join(dangling, "task")); err == nil {
		t.Fatal("悬空符号链接应失败")
	}
}

func validRequest() *ExecutionRequest {
	return &ExecutionRequest{
		Kind: RequestKind, Version: Version,
		Command:     Command{ID: "command-1", Operation: OperationStart, IssuedAt: time.Now().UTC()},
		Scope:       RequestScope{LocalTaskID: "task-1", ExecutionID: "execution-1", Attempt: 1, Turn: 1, ExpectedWorkerID: "worker-1"},
		Task:        TaskSpec{Title: "任务", Description: "描述", BaseBranch: "main"},
		Agent:       AgentSpec{Type: AgentCodex, WorkMode: WorkModeImplement},
		Project:     ProjectSpec{ID: "project-1", GitURL: "https://example.com/repo.git", DefaultBranch: "main", WorktreeNamePrefix: "task"},
		Worktree:    WorktreeSpec{Mode: WorktreeCreate},
		Commands:    Commands{Pre: []string{}, Post: []string{}},
		Environment: Environment{Variables: []EnvironmentVariable{}},
	}
}

func validExecutionEvent(eventType EventType, payload map[string]any) *ExecutionEvent {
	return &ExecutionEvent{
		Kind: EventKind, Version: Version,
		Event: EventHeader{
			ID: "018f0000-0000-7000-8000-000000000001", Sequence: 1,
			Type: eventType, OccurredAt: time.Unix(1, 0).UTC(),
		},
		Scope:   EventScope{LocalTaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker"},
		Payload: payload,
	}
}

func FuzzRedactorMatchesSinglePass(f *testing.F) {
	f.Add("prefix", "secret", "suffix")
	f.Fuzz(func(t *testing.T, left, secret, right string) {
		if secret == "" || len(secret) > 1024 {
			t.Skip()
		}
		redactor := NewRedactor([]string{secret})
		input := left + secret + right
		cut := len(input) / 2
		output := redactor.RedactChunk(input[:cut], false) + redactor.RedactChunk(input[cut:], true)
		want := NewRedactor([]string{secret}).Redact(input)
		if output != want {
			t.Fatalf("跨块结果 = %q，一次性结果 = %q", output, want)
		}
	})
}

func FuzzValidateLogEventPayload(f *testing.F) {
	f.Add("stdout", "hello")
	f.Add("invalid", "hello")
	f.Fuzz(func(t *testing.T, stream, content string) {
		if len(content) > MaxLogChunkBytes+1024 {
			t.Skip()
		}
		event := validExecutionEvent(EventLogChunk, map[string]any{"stream": stream, "content": content})
		err := ValidateEvent(event)
		valid := LogStream(stream).Valid() && content != "" && utf8.ValidString(content) &&
			utf8.RuneCountInString(content) <= MaxLogChunkBytes && len(content) <= MaxLogChunkBytes
		if valid && err != nil {
			t.Fatalf("合法日志被拒绝: %v", err)
		}
		if !valid && err == nil {
			t.Fatalf("非法日志被接受: stream=%q size=%d", stream, len(content))
		}
	})
}
