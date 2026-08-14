package a2aadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestCLIAdapterProbeAndRunErrorBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 CLI 脚本仅用于 Unix 测试")
	}
	if _, err := NewCodex("missing-agent-cli-for-test").Probe(nil); err == nil {
		t.Fatal("nil context 应失败")
	}
	if _, err := NewCodex("missing-agent-cli-for-test").Probe(context.Background()); err == nil {
		t.Fatal("不存在的 CLI 应失败")
	}
	failed := writeScript(t, "#!/bin/sh\necho version-error >&2\nexit 9\n")
	if _, err := NewCodex(failed).Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "version-error") {
		t.Fatalf("Probe 失败未保留输出: %v", err)
	}
	empty := writeScript(t, "#!/bin/sh\nexit 0\n")
	if _, err := NewClaude(empty).Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "未返回版本") {
		t.Fatalf("空版本应失败: %v", err)
	}

	input := validAdapterInput(t, a2aext.AgentCodex)
	input.Request.Kind = "invalid"
	adapter := &cliAdapter{agent: cliAgentFunc(func(context.Context, Input, func(Event)) error { return nil })}
	if err := adapter.Run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("非法 execution request 应失败")
	}
	input = validAdapterInput(t, a2aext.AgentCodex)
	input.WorktreePath = " "
	if err := adapter.Run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("空 worktreePath 应失败")
	}
	input = validAdapterInput(t, a2aext.AgentCodex)
	adapter.agent = cliAgentFunc(func(context.Context, Input, func(Event)) error { return ErrEventTooLarge })
	if err := adapter.Run(context.Background(), input, func(Event) {}); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("超限错误未原样保留: %v", err)
	}
	plainErr := errors.New("plain failure")
	adapter.agent = cliAgentFunc(func(context.Context, Input, func(Event)) error { return plainErr })
	if err := adapter.Run(context.Background(), input, func(Event) {}); !errors.Is(err, plainErr) {
		t.Fatalf("普通错误未原样保留: %v", err)
	}

	if normalizeAgentScannerError(nil) != nil {
		t.Fatal("nil scanner error 应保持 nil")
	}
	if err := normalizeAgentScannerError(errors.New("bufio.Scanner: token too long")); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("scanner 超长错误未规范化: %v", err)
	}
}

func TestClaudeParsingAndInteractionBranches(t *testing.T) {
	tests := []struct {
		line         string
		session      string
		conversation string
		denials      int
	}{
		{`{"type":"thread.started","thread_id":"thread-fallback"}`, "thread-fallback", "", 0},
		{`{"type":"item.completed","item":{"type":"tool_call","text":"ignored"}}`, "", "", 0},
		{`{"type":"item.completed","item":"invalid"}`, "", "", 0},
		{`{"type":"assistant","message":"invalid"}`, "", "", 0},
		{`{"type":"assistant","message":{"content":"invalid"}}`, "", "", 0},
		{`{"type":"assistant","message":{"content":[null,{"type":"text","text":""},{"type":"text","text":"answer"}]}}`, "", "answer", 0},
		{`{"type":"system","subtype":"permission_denied","session_id":"session","tool_name":"Bash","tool_use_id":"tool"}`, "session", "", 1},
		{`{"message":"fallback"}`, "", "fallback", 0},
		{`{"type":"result","permission_denials":"invalid"}`, "", "", 0},
		{`{"type":"result","permission_denials":[null,{"tool_name":"Read"},{"tool_name":"Read"}]}`, "", "", 1},
	}
	parsedToolUse := parseClaudeJSONLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool","name":"Bash","input":{"command":"make test"}}]}}`)
	if len(parsedToolUse.ToolUses) != 1 || parsedToolUse.ToolUses[0].allowedTool() != "Bash(make test)" {
		t.Fatalf("Claude tool_use 解析错误: %+v", parsedToolUse)
	}
	enriched := enrichClaudePermissionDenials(
		[]claudePermissionDenial{{ToolUseID: "tool", ToolInput: map[string]any{}}},
		map[string]claudePermissionDenial{"tool": parsedToolUse.ToolUses[0]},
	)
	if len(enriched) != 1 || enriched[0].allowedTool() != "Bash(make test)" {
		t.Fatalf("Claude permission_denied 关联错误: %+v", enriched)
	}
	for _, testCase := range tests {
		parsed := parseClaudeJSONLine(testCase.line)
		if parsed.SessionID != testCase.session || parsed.Conversation != testCase.conversation || len(parsed.PermissionDenials) != testCase.denials {
			t.Fatalf("parseClaudeJSONLine(%s) = %+v", testCase.line, parsed)
		}
	}

	denials := claudePermissionDenials(map[string]any{"permission_denials": []any{
		map[string]any{"tool_name": "Bash"},
		map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"path": "a.go"}},
		map[string]any{"tool_name": "MultiEdit", "tool_input": map[string]any{"file_path": "b.go"}},
		map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "c.go"}},
		map[string]any{"tool_name": ""},
	}})
	if len(denials) != 5 {
		t.Fatalf("permission denials = %+v", denials)
	}
	for _, denial := range denials {
		request := claudeInteractionRequest(denial, "", "session")
		if request.Title == "" || request.Body == "" {
			t.Fatalf("交互请求不完整: %+v", request)
		}
	}
	if got := claudeInteractionRequest(claudePermissionDenial{}, "", ""); got.Body != "Tool: tool" {
		t.Fatalf("完全空 denial 未生成兜底摘要: body=%q", got.Body)
	}
	if got := claudeApprovalResumeMessage(denials[0], InteractionResponse{Decision: a2aext.DecisionApproveForSession}); !strings.Contains(got, "task session") {
		t.Fatalf("session approval message=%q", got)
	}
	if got := claudeApprovalResumeMessage(denials[0], InteractionResponse{Decision: a2aext.DecisionApprove}); strings.Contains(got, "User note:") {
		t.Fatalf("空 note 不应输出: %q", got)
	}
	if got := claudeDenialResumeMessage(denials[0], InteractionResponse{Decision: a2aext.DecisionCancel}); !strings.Contains(got, "canceled") {
		t.Fatalf("cancel message=%q", got)
	}
	if got := claudeDenialResumeMessage(denials[0], InteractionResponse{Decision: a2aext.DecisionDeny}); strings.Contains(got, "User note:") {
		t.Fatalf("空 note 不应输出: %q", got)
	}
	if jsonString(make(chan int)) != "" || directSessionID(map[string]any{"session_id": 7}) != "" {
		t.Fatal("不可编码值或非字符串 session 不应产生文本")
	}
}

func TestClaudeOperationAndPermissionDecisionBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 CLI 脚本仅用于 Unix 测试")
	}
	agent := newClaudeAgent("unused", func() string { return "fixed-session" })
	input := validAdapterInput(t, a2aext.AgentClaude)
	input.Request.Command.Operation = a2aext.OperationContinue
	if err := agent.run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("CONTINUE 缺 message/session 应失败")
	}
	input.Message = "continue"
	if err := agent.run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("CONTINUE 缺 session 应失败")
	}
	input.Request.Command.Operation = a2aext.OperationInteractionResponse
	if err := agent.run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("未知 Claude operation 应失败")
	}

	binary := writeScript(t, `#!/bin/sh
case "$*" in
  *"The user"*)
    printf '%s\n' '{"type":"system","session_id":"fixed-session"}'
    printf '%s\n' '{"type":"result","session_id":"fixed-session","result":"completed after interaction"}'
    ;;
  *)
    printf '%s\n' 'stderr-event' >&2
    printf '%s\n' '{"type":"result","result":"permission needed","permission_denials":[{"tool_name":"Bash","tool_input":{"command":"make test"}}]}'
    ;;
esac
`)
	for _, decision := range []a2aext.InteractionDecision{a2aext.DecisionApprove, a2aext.DecisionApproveForSession, a2aext.DecisionDeny} {
		input = validAdapterInput(t, a2aext.AgentClaude)
		input.Request.Command.Operation = a2aext.OperationRetry
		input.RequestInteraction = func(context.Context, InteractionRequest) (InteractionResponse, error) {
			return InteractionResponse{Decision: decision}, nil
		}
		var events []Event
		var eventsMu sync.Mutex
		if err := newClaudeAgent(binary, func() string { return "fixed-session" }).run(context.Background(), input, func(event Event) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		}); err != nil {
			t.Fatalf("decision %s: %v", decision, err)
		}
		eventsMu.Lock()
		if !hasEvent(events, EventStderr, "stderr-event") || !hasEvent(events, EventCompleted, "completed after interaction") {
			t.Fatalf("decision %s events=%+v", decision, events)
		}
		eventsMu.Unlock()
	}

	input = validAdapterInput(t, a2aext.AgentClaude)
	if err := newClaudeAgent(binary, func() string { return "fixed-session" }).run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("缺少交互路由应失败")
	}
	input.RequestInteraction = func(context.Context, InteractionRequest) (InteractionResponse, error) {
		return InteractionResponse{}, errors.New("interaction failed")
	}
	if err := newClaudeAgent(binary, func() string { return "fixed-session" }).run(context.Background(), input, func(Event) {}); err == nil || !strings.Contains(err.Error(), "interaction failed") {
		t.Fatalf("交互错误未传播: %v", err)
	}
	input.RequestInteraction = func(context.Context, InteractionRequest) (InteractionResponse, error) {
		return InteractionResponse{Decision: a2aext.InteractionDecision("invalid")}, nil
	}
	if err := newClaudeAgent(binary, func() string { return "fixed-session" }).run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("未知交互决定应失败")
	}

	noSession := writeScript(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"done\"}'\n")
	result, err := newClaudeAgent(noSession, func() string { return "" }).runSessionOnce(context.Background(), t.TempDir(), nil, nil, "", func(Event) {})
	if err == nil || result.SessionID != "" {
		t.Fatalf("缺 session 应失败: result=%+v err=%v", result, err)
	}
	if _, err := newClaudeAgent("missing-claude-cli", nil).runSessionOnce(context.Background(), t.TempDir(), nil, nil, "session", func(Event) {}); err == nil {
		t.Fatal("CLI 启动失败应返回错误")
	}
}

func TestCodexOperationNotificationAndMappingBranches(t *testing.T) {
	agent := &codexAppServerAgent{binary: "unused"}
	input := validAdapterInput(t, a2aext.AgentCodex)
	input.Request.Command.Operation = a2aext.OperationContinue
	if err := agent.run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("Codex CONTINUE 缺 message/session 应失败")
	}
	input.Message = "continue"
	if err := agent.run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("Codex CONTINUE 缺 session 应失败")
	}
	input.Request.Command.Operation = a2aext.OperationInteractionResponse
	if err := agent.run(context.Background(), input, func(Event) {}); err == nil {
		t.Fatal("未知 Codex operation 应失败")
	}

	var events []Event
	rpc := &codexRPC{state: newCodexRunState(), emit: func(event Event) { events = append(events, event) }}
	rpc.handleNotification(codexRPCMessage{Method: "thread/started", Params: json.RawMessage(`{`)})
	rpc.handleNotification(codexRPCMessage{Method: "thread/started", Params: json.RawMessage(`{"thread":{}}`)})
	rpc.handleNotification(codexRPCMessage{Method: "item/completed", Params: json.RawMessage(`{`)})
	rpc.handleNotification(codexRPCMessage{Method: "item/completed", Params: json.RawMessage(`{"item":{"type":"toolCall","text":"ignored"}}`)})
	rpc.handleNotification(codexRPCMessage{Method: "item/completed", Params: json.RawMessage(`{"item":{"type":"agentMessage","text":""}}`)})
	rpc.handleNotification(codexRPCMessage{Method: "error", Params: json.RawMessage(`{`)})
	rpc.handleNotification(codexRPCMessage{Method: "error", Params: json.RawMessage(`{"message":""}`)})
	if len(events) != 0 {
		t.Fatalf("无效通知不应产生事件: %+v", events)
	}

	failed := newCodexRunState()
	rpc.state = failed
	rpc.handleNotification(codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"turn":{"status":"failed"}}`)})
	if _, err := failed.wait(context.Background()); err == nil || err.Error() != "codex turn failed" {
		t.Fatalf("默认失败消息=%v", err)
	}
	interrupted := newCodexRunState()
	rpc.state = interrupted
	rpc.handleNotification(codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"turn":{"status":"interrupted"}}`)})
	if _, err := interrupted.wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted=%v", err)
	}
	rpc.handleNotification(codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{`)})

	requestCases := []struct {
		method string
		params map[string]any
	}{
		{"item/commandExecution/requestApproval", map[string]any{"command": "make test"}},
		{"item/fileChange/requestApproval", map[string]any{"grantRoot": "/work"}},
		{"item/permissions/requestApproval", map[string]any{}},
		{"item/tool/requestUserInput", map[string]any{"questions": "invalid"}},
		{"item/tool/requestUserInput", map[string]any{"questions": []any{"invalid"}}},
		{"item/tool/requestUserInput", map[string]any{"questions": []any{map[string]any{}}}},
		{"unknown", map[string]any{}},
	}
	for _, testCase := range requestCases {
		request := codexInteractionRequest(testCase.method, testCase.params, "raw")
		if request.ID == "" || request.Title == "" || request.Body == "" {
			t.Fatalf("request mapping %+v", request)
		}
	}

	questions := map[string]any{"questions": []any{"invalid", map[string]any{}, map[string]any{"id": "q1"}}}
	rawResult, err := codexInteractionRPCResult("item/tool/requestUserInput", questions, InteractionResponse{Decision: a2aext.DecisionRespond, Message: "answer", Payload: "not-json"})
	if err != nil {
		t.Fatal(err)
	}
	result := rawResult.(map[string]any)
	if len(result["answers"].(map[string]any)) != 1 {
		t.Fatalf("fallback answers=%+v", result)
	}
	rawUnknown, err := codexInteractionRPCResult("unknown", nil, InteractionResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rawUnknown.(map[string]any); len(got) != 0 {
		t.Fatalf("unknown result=%+v", got)
	}
	if got := codexGrantedPermissions("invalid"); len(got) != 0 {
		t.Fatalf("invalid permissions=%+v", got)
	}
	if got := codexGrantedPermissions(map[string]any{"network": nil, "fileSystem": nil}); len(got) != 0 {
		t.Fatalf("nil permissions=%+v", got)
	}
	for decision, want := range map[a2aext.InteractionDecision]string{
		a2aext.DecisionApprove: "accept", a2aext.DecisionApproveForSession: "acceptForSession",
		a2aext.DecisionDeny: "decline", a2aext.DecisionCancel: "cancel",
	} {
		if got, err := codexApprovalDecision(decision); err != nil || got != want {
			t.Fatalf("Codex 决策 %q = %q, err=%v", decision, got, err)
		}
	}
	if _, err := codexApprovalDecision(a2aext.DecisionRespond); err == nil || codexPermissionScope(a2aext.DecisionApprove) != "turn" {
		t.Fatal("Codex 非法审批决定未拒绝")
	}
}

func TestCodexRPCCallAndStateBranches(t *testing.T) {
	writer := &bufferWriteCloser{}
	rpc := &codexRPC{stdin: writer, pending: map[string]chan codexRPCMessage{}, done: make(chan struct{})}
	callResult := make(chan error, 1)
	go func() {
		var out map[string]any
		callResult <- rpc.call(context.Background(), "test", nil, &out)
	}()
	waitForPendingCall(t, rpc, "1")
	rpc.resolveCall(codexRPCMessage{ID: json.RawMessage(`1`), Error: &codexRPCError{Message: "failed"}})
	if err := <-callResult; err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("RPC error=%v", err)
	}

	go func() {
		var out map[string]any
		callResult <- rpc.call(context.Background(), "invalid-result", nil, &out)
	}()
	waitForPendingCall(t, rpc, "2")
	rpc.resolveCall(codexRPCMessage{ID: json.RawMessage(`2`), Result: json.RawMessage(`{`)})
	if err := <-callResult; err == nil {
		t.Fatal("非法 RPC result 应失败")
	}
	rpc.resolveCall(codexRPCMessage{ID: json.RawMessage(`999`), Result: json.RawMessage(`{}`)})

	closed := &codexRPC{stdin: &bufferWriteCloser{}, pending: map[string]chan codexRPCMessage{}, done: make(chan struct{})}
	close(closed.done)
	if err := closed.call(context.Background(), "closed", nil, nil); err == nil {
		t.Fatal("进程结束时 call 应失败")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pending := &codexRPC{stdin: &bufferWriteCloser{}, pending: map[string]chan codexRPCMessage{}, done: make(chan struct{})}
	if err := pending.call(ctx, "canceled", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消 call=%v", err)
	}

	broken := &codexRPC{stdin: errorWriteCloser{}, pending: map[string]chan codexRPCMessage{}, done: make(chan struct{})}
	if err := broken.call(context.Background(), "write-failed", nil, nil); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write failure=%v", err)
	}
	if err := rpc.write(make(chan int)); err == nil {
		t.Fatal("不可编码 RPC message 应失败")
	}
	if err := broken.sendResult(json.RawMessage(`1`), map[string]any{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("send result failure=%v", err)
	}
	if rpc.processError() != io.ErrUnexpectedEOF {
		t.Fatal("空 process error 应回退 EOF")
	}
	rpc.processErr = errors.New("process failed")
	if rpc.processError().Error() != "process failed" {
		t.Fatal("process error 未保留")
	}

	state := newCodexRunState()
	state.setThread("")
	state.setTurn("")
	state.complete()
	state.fail(errors.New("ignored"))
	if result, err := state.wait(context.Background()); err != nil || result != "completed" {
		t.Fatalf("空消息完成 result=%q err=%v", result, err)
	}
	waiting := newCodexRunState()
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitCancel()
	if _, err := waiting.wait(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("state wait cancel=%v", err)
	}
}

func TestCodexHandleServerRequestErrorBranches(t *testing.T) {
	message := codexRPCMessage{ID: json.RawMessage(`7`), Method: "item/tool/requestUserInput", Params: json.RawMessage(`{}`)}
	rpc := &codexRPC{}
	if err := rpc.handleServerRequest(context.Background(), message); err == nil {
		t.Fatal("缺交互路由应失败")
	}
	rpc.requestInteraction = func(context.Context, InteractionRequest) (InteractionResponse, error) {
		return InteractionResponse{}, nil
	}
	message.Params = json.RawMessage(`{`)
	if err := rpc.handleServerRequest(context.Background(), message); err == nil {
		t.Fatal("非法 params 应失败")
	}

	message.Params = json.RawMessage(`{}`)
	rpc.requestInteraction = func(context.Context, InteractionRequest) (InteractionResponse, error) {
		return InteractionResponse{}, errors.New("interaction failed")
	}
	if err := rpc.handleServerRequest(context.Background(), message); err == nil || !strings.Contains(err.Error(), "interaction failed") {
		t.Fatalf("interaction failure=%v", err)
	}

	processExited := make(chan struct{})
	close(processExited)
	rpc = &codexRPC{requestInteraction: func(ctx context.Context, _ InteractionRequest) (InteractionResponse, error) {
		<-ctx.Done()
		return InteractionResponse{}, ctx.Err()
	}, processDone: processExited}
	if err := rpc.handleServerRequest(context.Background(), message); err == nil || !strings.Contains(err.Error(), "处理") {
		t.Fatalf("process exit interaction=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rpc = &codexRPC{requestInteraction: func(ctx context.Context, _ InteractionRequest) (InteractionResponse, error) {
		<-ctx.Done()
		return InteractionResponse{}, ctx.Err()
	}, processDone: make(chan struct{})}
	if err := rpc.handleServerRequest(ctx, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("interaction context cancel=%v", err)
	}

	rpc = &codexRPC{stdin: errorWriteCloser{}, requestInteraction: func(context.Context, InteractionRequest) (InteractionResponse, error) {
		return InteractionResponse{Decision: a2aext.DecisionRespond, Message: "answer"}, nil
	}, processDone: make(chan struct{})}
	if err := rpc.handleServerRequest(context.Background(), message); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("send result failure=%v", err)
	}
}

type bufferWriteCloser struct {
	bytes.Buffer
}

func (w *bufferWriteCloser) Close() error { return nil }

type errorWriteCloser struct{}

func (errorWriteCloser) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (errorWriteCloser) Close() error              { return nil }

func waitForPendingCall(t *testing.T, rpc *codexRPC, key string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rpc.pendingMu.Lock()
		_, ok := rpc.pending[key]
		rpc.pendingMu.Unlock()
		if ok {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("等待 RPC pending %s 超时", key)
}
