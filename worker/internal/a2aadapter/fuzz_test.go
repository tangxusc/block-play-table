package a2aadapter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

// FuzzCodexJSONRPCMessage 验证 Codex JSON-RPC 消息解码、通知和交互映射不会 panic 或破坏合法消息。
func FuzzCodexJSONRPCMessage(f *testing.F) {
	seeds := []string{
		`{"method":"thread/started","params":{"thread":{"id":"thread-1"}}}`,
		`{"method":"item/completed","params":{"threadId":"thread-1","turnId":"turn-1","item":{"type":"agentMessage","text":"answer"}}}`,
		`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}`,
		`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"message":"failed"}}}}`,
		`{"method":"error","params":{"message":"server error"}}`,
		`{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","command":"make test","reason":"verify"}}`,
		`{"id":"request-8","method":"item/fileChange/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-2","grantRoot":"/workspace"}}`,
		`{"id":9,"method":"item/permissions/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","approvalId":"approval-1","permissions":{"network":{"enabled":true},"fileSystem":{"read":["/workspace"]}}}}`,
		`{"id":10,"method":"item/tool/requestUserInput","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-3","questions":[{"id":"question-1","header":"Choice","question":"Continue?"}]}}`,
		`{"id":1,"result":{}}`,
		`{"id":2,"result":{"thread":{"id":"thread-1"}}}`,
		`{"id":3,"error":{"code":-32000,"message":"request failed"}}`,
		``,
		`not-json`,
		`{"id":`,
		`null`,
		`[]`,
		`{"id":{"nested":true},"method":7,"params":"wrong"}`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > a2aext.MaxAgentEventBytes+1024 {
			t.Skip()
		}
		var message codexRPCMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			return
		}

		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatalf("合法 Codex JSON-RPC 消息无法编码：%v", err)
		}
		var roundTrip codexRPCMessage
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatalf("合法 Codex JSON-RPC 消息无法往返解码：%v", err)
		}
		if message.Method != roundTrip.Method || !reflect.DeepEqual(message.Error, roundTrip.Error) {
			t.Fatalf("Codex JSON-RPC 标量字段往返不一致：before=%+v after=%+v", message, roundTrip)
		}
		assertEquivalentRawJSON(t, "id", message.ID, roundTrip.ID)
		assertEquivalentRawJSON(t, "params", message.Params, roundTrip.Params)
		assertEquivalentRawJSON(t, "result", message.Result, roundTrip.Result)

		switch {
		case len(message.ID) == 0:
			var events []Event
			rpc := &codexRPC{
				state: newCodexRunState(),
				emit:  func(event Event) { events = append(events, event) },
			}
			rpc.handleNotification(message)
			for _, event := range events {
				if event.Type != EventConversation || event.Content == "" {
					t.Fatalf("Codex 通知产生非法事件：%+v", event)
				}
			}
		case message.Method != "":
			var params map[string]any
			if err := json.Unmarshal(message.Params, &params); err != nil {
				return
			}
			request := codexInteractionRequest(message.Method, params, string(message.Params))
			repeated := codexInteractionRequest(message.Method, params, string(message.Params))
			if !reflect.DeepEqual(request, repeated) || !request.Kind.Valid() || !strings.HasPrefix(request.ID, "codex_") {
				t.Fatalf("Codex 交互映射不稳定：first=%+v second=%+v", request, repeated)
			}
			for _, decision := range []a2aext.InteractionDecision{
				a2aext.DecisionApprove,
				a2aext.DecisionApproveForSession,
				a2aext.DecisionDeny,
				a2aext.DecisionCancel,
				a2aext.DecisionRespond,
			} {
				result, resultErr := codexInteractionRPCResult(message.Method, params, InteractionResponse{
					Decision: decision,
					Message:  "answer",
					Payload:  `{"answers":{"question-1":{"answers":["yes"]}}}`,
				})
				if resultErr != nil {
					continue
				}
				if encodedResult, err := json.Marshal(result); err != nil || !json.Valid(encodedResult) {
					t.Fatalf("Codex 交互响应无法编码：decision=%s result=%+v err=%v", decision, result, err)
				}
			}
		}
	})
}

// FuzzClaudeStreamJSONLine 验证 Claude stream-json 行解析、权限去重和交互映射保持确定且不会 panic。
func FuzzClaudeStreamJSONLine(f *testing.F) {
	seeds := []string{
		`{"type":"system","session_id":"session-1"}`,
		`{"type":"assistant","session_id":"session-1","message":{"content":[{"type":"text","text":"first"},{"type":"tool_use","name":"Bash"},{"type":"text","text":"second"}]}}`,
		`{"type":"result","session_id":"session-1","result":"completed"}`,
		`{"type":"result","session_id":"session-1","result":"approval required","permission_denials":[{"tool_name":"Bash","tool_use_id":"tool-1","tool_input":{"command":"make test"}},{"tool_name":"Write","tool_use_id":"tool-2","tool_input":{"file_path":"README.md"}},{"tool_name":"WebFetch","tool_use_id":"tool-3","tool_input":{"url":"https://example.com"}},{"tool_name":"Bash","tool_use_id":"duplicate","tool_input":{"command":"make test"}}]}`,
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"item.completed","threadId":"thread-1","item":{"type":"agent_message","text":"item answer"}}`,
		`{"message":"fallback conversation","conversationId":"conversation-1"}`,
		``,
		`not-json`,
		`{"type":`,
		`null`,
		`[]`,
		`{"type":"assistant","message":{"content":[null,7,{"type":"text","text":false}]}}`,
		`{"type":"result","permission_denials":{"tool_name":"Bash"}}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, line string) {
		if len(line) > a2aext.MaxAgentEventBytes+1024 {
			t.Skip()
		}
		parsed := parseClaudeJSONLine(line)
		if repeated := parseClaudeJSONLine(line); !reflect.DeepEqual(parsed, repeated) {
			t.Fatalf("Claude stream-json 相同行解析结果不稳定：first=%+v second=%+v", parsed, repeated)
		}

		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			if !reflect.DeepEqual(parsed, claudeLineParse{}) {
				t.Fatalf("畸形 Claude stream-json 产生了业务字段：%+v", parsed)
			}
			return
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("合法 Claude stream-json 无法编码：%v", err)
		}
		if roundTrip := parseClaudeJSONLine(string(canonical)); !reflect.DeepEqual(parsed, roundTrip) {
			t.Fatalf("Claude stream-json 往返不一致：before=%+v after=%+v", parsed, roundTrip)
		}

		seen := map[string]struct{}{}
		for _, denial := range parsed.PermissionDenials {
			key := denial.ToolName + "\x00" + jsonString(denial.ToolInput)
			if _, exists := seen[key]; exists {
				t.Fatalf("Claude 权限拒绝未按工具和输入去重：%q", key)
			}
			seen[key] = struct{}{}
			if !json.Valid([]byte(denial.RawPayload)) {
				t.Fatalf("Claude 权限拒绝 raw payload 非法：%q", denial.RawPayload)
			}
			request := claudeInteractionRequest(denial, parsed.FinalResult, parsed.SessionID)
			if request.Kind != denial.kind() || !request.Kind.Valid() || request.Title == "" || request.Body == "" || request.AgentSessionID != parsed.SessionID {
				t.Fatalf("Claude 权限交互映射非法：denial=%+v request=%+v", denial, request)
			}
			approved := claudeApprovalResumeMessage(denial, InteractionResponse{Decision: a2aext.DecisionApprove, Message: "continue"})
			denied := claudeDenialResumeMessage(denial, InteractionResponse{Decision: a2aext.DecisionDeny, Message: "skip"})
			if approved == "" || denied == "" || approved == denied {
				t.Fatalf("Claude 权限回复映射非法：approved=%q denied=%q", approved, denied)
			}
		}
	})
}

func assertEquivalentRawJSON(t *testing.T, field string, before, after json.RawMessage) {
	t.Helper()
	if len(before) == 0 || len(after) == 0 {
		if len(before) != len(after) {
			t.Fatalf("Codex JSON-RPC %s 是否存在发生变化：before=%q after=%q", field, before, after)
		}
		return
	}
	var beforeValue any
	if err := json.Unmarshal(before, &beforeValue); err != nil {
		t.Fatalf("Codex JSON-RPC %s 原值非法：%v", field, err)
	}
	var afterValue any
	if err := json.Unmarshal(after, &afterValue); err != nil {
		t.Fatalf("Codex JSON-RPC %s 往返值非法：%v", field, err)
	}
	if !reflect.DeepEqual(beforeValue, afterValue) {
		t.Fatalf("Codex JSON-RPC %s 往返不一致：before=%v after=%v", field, beforeValue, afterValue)
	}
}
