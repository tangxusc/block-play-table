package a2aadapter

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestPromptAndWorkModePrefixes(t *testing.T) {
	request := &a2aext.ExecutionRequest{Scope: a2aext.RequestScope{LocalTaskID: "task"}}
	if got := promptForRequest(request); got != "Complete task task" {
		t.Fatalf("空任务 prompt=%q", got)
	}
	request.Task.Title = " Title "
	if got := promptForRequest(request); got != "Title" {
		t.Fatalf("title prompt=%q", got)
	}
	request.Task.Description = " Description "
	for _, mode := range []a2aext.WorkMode{a2aext.WorkModePlan, a2aext.WorkModeImplement, a2aext.WorkModeReview} {
		request.Agent.WorkMode = mode
		got := promptForRequest(request)
		if !strings.Contains(got, "Description") || workModePromptPrefix(mode) == "" {
			t.Fatalf("mode %q prompt=%q", mode, got)
		}
	}
	if workModePromptPrefix(a2aext.WorkMode("unknown")) != "" {
		t.Fatal("未知 work mode 应无前缀")
	}
	if defaultBinary(" ", "fallback") != "fallback" || defaultBinary("configured", "fallback") != "configured" {
		t.Fatal("defaultBinary fallback 错误")
	}
}

func TestCodexParameterMappingCoversExplicitAndAutomaticModes(t *testing.T) {
	request := &a2aext.ExecutionRequest{Agent: a2aext.AgentSpec{Config: a2aext.AgentConfig{
		Model: "gpt", ReasoningEffort: "high", ApprovalPolicy: "on-request", SandboxMode: "read-only",
	}}}
	start := codexThreadStartParams(request, "/work")
	resume := codexThreadResumeParams(request, "/work", "thread")
	turn := codexTurnStartParams(request, "/work", "thread", "message")
	if start["model"] != "gpt" || resume["threadId"] != "thread" || turn["effort"] != "high" || turn["approvalPolicy"] != "on-request" {
		t.Fatalf("Codex params start=%+v resume=%+v turn=%+v", start, resume, turn)
	}
	for name, params := range map[string]map[string]any{"start": start, "resume": resume, "turn": turn} {
		if params["approvalsReviewer"] != "user" {
			t.Fatalf("Codex %s approvalsReviewer=%v，必须由 Manager 用户审批", name, params["approvalsReviewer"])
		}
	}
	for _, testCase := range []struct {
		config       a2aext.AgentConfig
		wantApproval string
		wantSandbox  string
	}{
		{a2aext.AgentConfig{}, "", ""},
		{a2aext.AgentConfig{FullAuto: true}, "on-failure", "workspace-write"},
		{a2aext.AgentConfig{ApprovalPolicy: "never", SandboxMode: "danger-full-access"}, "never", "danger-full-access"},
		{a2aext.AgentConfig{BypassApprovalsAndSandbox: true, ApprovalPolicy: "on-request", SandboxMode: "read-only"}, "never", "danger-full-access"},
	} {
		if got := codexApprovalPolicy(testCase.config); got != testCase.wantApproval {
			t.Fatalf("approval=%q，期望 %q", got, testCase.wantApproval)
		}
		if got := codexSandboxMode(testCase.config); got != testCase.wantSandbox {
			t.Fatalf("sandbox=%q，期望 %q", got, testCase.wantSandbox)
		}
	}
}

func TestClaudeArgumentAndAllowedToolNormalization(t *testing.T) {
	config := a2aext.AgentConfig{Model: "sonnet", Effort: "high", PermissionMode: "acceptEdits"}
	start := claudeCommandArgs(config, "session", "message", false, []string{" Bash(ls) ", "", "Bash(ls)", "Read"})
	resume := claudeCommandArgs(a2aext.AgentConfig{PermissionMode: "default"}, "session", "continue", true, nil)
	if !reflect.DeepEqual(uniqueNonEmptyStrings([]string{" a ", "", "a", "b"}), []string{"a", "b"}) {
		t.Fatal("allowed tools 未去空白和去重")
	}
	if !containsArgumentPair(start, "--session-id", "session") || !containsArgumentPair(start, "--model", "sonnet") || !containsArgumentPair(resume, "--resume", "session") {
		t.Fatalf("Claude args start=%v resume=%v", start, resume)
	}
	if tools := approvedToolsList(map[string]bool{"z": true, "a": false, "m": true}); !reflect.DeepEqual(tools, []string{"a", "m", "z"}) {
		t.Fatalf("approved tools=%v", tools)
	}
	command := claudePermissionDenial{ToolName: "Bash", ToolInput: map[string]any{"command": "go test ./..."}}
	if command.allowedTool() != "Bash(go test ./...)" {
		t.Fatalf("command allowed tool=%q", command.allowedTool())
	}
	if (claudePermissionDenial{ToolName: "Bash"}).allowedTool() != "Bash" || (claudePermissionDenial{ToolName: "Read"}).allowedTool() != "Read" {
		t.Fatal("allowedTool fallback 错误")
	}
}

func containsArgumentPair(args []string, key, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key && args[index+1] == value {
			return true
		}
	}
	return false
}
