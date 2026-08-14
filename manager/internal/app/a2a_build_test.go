package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestBuildA2ARequestOperationsAgentsAndSensitivePayload(t *testing.T) {
	now := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	project := &domain.Project{ID: "project", GitURL: "https://example.test/repo.git", DefaultBranch: "main", WorktreeNamePrefix: "task"}
	worker := &domain.Worker{
		ID: "worker", SupportedAgents: []domain.AgentType{domain.AgentCodex, domain.AgentClaude},
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{
			{AgentType: domain.AgentCodex, Vars: []domain.AgentRuntimeEnvVar{{Key: "TOKEN", Value: "secret", Enabled: true, Sensitive: true}}},
		},
	}
	round := &domain.TaskA2ARound{ExecutionID: "execution", Attempt: 1, Turn: 1, CreatedAt: now}
	for _, testCase := range []struct {
		name      string
		agent     domain.AgentType
		operation domain.TaskA2AOperation
		wantMode  a2aext.WorktreeMode
	}{
		{"codex start", domain.AgentCodex, domain.TaskA2AOperationStart, a2aext.WorktreeCreate},
		{"codex retry", domain.AgentCodex, domain.TaskA2AOperationRetry, a2aext.WorktreeRecreate},
		{"codex continue", domain.AgentCodex, domain.TaskA2AOperationContinue, a2aext.WorktreeResume},
		{"claude start", domain.AgentClaude, domain.TaskA2AOperationStart, a2aext.WorktreeCreate},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			task := &domain.Task{
				ID: "task", Title: "Task", Description: "Description", ProjectID: project.ID,
				AgentType: testCase.agent, WorkerID: worker.ID, AgentSessionID: "session", WorktreePath: "/tmp/worktree",
				PreCommands: []string{"pre"}, PostCommands: []string{"post"},
			}
			if testCase.agent == domain.AgentCodex {
				task.AgentConfig = domain.AgentExecutionConfig{WorkMode: domain.AgentWorkModeImplement, Codex: domain.CodexExecutionConfig{Model: "gpt", ReasoningEffort: domain.CodexReasoningHigh}}
			} else {
				task.AgentConfig = domain.AgentExecutionConfig{WorkMode: domain.AgentWorkModeReview, Claude: domain.ClaudeExecutionConfig{Model: "sonnet", Effort: domain.ClaudeEffortHigh}}
			}
			request, err := buildA2ARequest(task, project, worker, round, "command", testCase.operation, nil)
			if err != nil {
				t.Fatal(err)
			}
			if request.Worktree.Mode != testCase.wantMode || request.Task.BaseBranch != "main" || request.Agent.Type != a2aext.AgentType(testCase.agent) {
				t.Fatalf("request=%+v", request)
			}
			if testCase.wantMode == a2aext.WorktreeResume && (request.Resume == nil || request.Resume.AgentSessionID != "session") {
				t.Fatalf("resume request=%+v", request.Resume)
			}
			if testCase.agent == domain.AgentCodex {
				if len(request.Environment.Variables) != 1 || request.Environment.Variables[0].Value != "secret" {
					t.Fatalf("runtime env=%+v", request.Environment)
				}
				raw, err := marshalA2AIntentPayload(request, "start")
				if err != nil || strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), a2aext.SensitivePlaceholder) {
					t.Fatalf("脱敏 payload=%s err=%v", raw, err)
				}
			}
		})
	}
	if _, err := buildA2ARequest(nil, project, worker, round, "command", domain.TaskA2AOperationStart, nil); err == nil {
		t.Fatal("nil task 未失败")
	}
	invalidTask := &domain.Task{ID: "task", Title: "Task", AgentType: domain.AgentType("unknown")}
	if _, err := buildA2ARequest(invalidTask, project, worker, round, "command", domain.TaskA2AOperationStart, nil); err == nil {
		t.Fatal("未知 agent 未失败")
	}
	validTask := &domain.Task{ID: "task", Title: "Task", AgentType: domain.AgentCodex}
	if _, err := buildA2ARequest(validTask, project, worker, round, "command", domain.TaskA2AOperationCancel, nil); err == nil {
		t.Fatal("未知 request operation 未失败")
	}
}

func TestA2AErrorAndCopyHelpersPreserveIdentity(t *testing.T) {
	if a2aProtocolError(nil) != nil || a2aProjectionError(nil) != nil {
		t.Fatal("nil 错误不应被包装")
	}
	protocolCause := errors.New("protocol")
	protocol := a2aProtocolError(protocolCause)
	if !errors.Is(protocol, ErrA2AProtocolConflict) || !errors.Is(protocol, protocolCause) || a2aProtocolError(protocol) != protocol {
		t.Fatalf("协议错误包装=%v", protocol)
	}
	projectionCause := errors.New("projection")
	projection := a2aProjectionError(projectionCause)
	if !errors.Is(projection, ErrA2AProjection) || !errors.Is(projection, projectionCause) || a2aProjectionError(projection) != projection {
		t.Fatalf("投影错误包装=%v", projection)
	}
	if cloneStringMap(nil) != nil {
		t.Fatal("空 map clone 应为 nil")
	}
	original := map[string]string{"key": "value"}
	copy := cloneStringMap(original)
	copy["key"] = "changed"
	if original["key"] != "value" {
		t.Fatal("cloneStringMap 未隔离输入")
	}
	if firstString("", "second", "third") != "second" || firstString("", "") != "" {
		t.Fatal("firstString fallback 错误")
	}
}

func TestA2ATextDecisionAndIdentityHelpers(t *testing.T) {
	if a2aStartText(nil) != "Start task" || a2aStartText(&domain.Task{Title: " Title "}) != "Title" || a2aStartText(&domain.Task{Title: "Title", Description: " Description "}) != "Description" {
		t.Fatal("start text fallback 错误")
	}
	if a2aInteractionText(nil) != "Interaction response" ||
		a2aInteractionText(&domain.TaskInteraction{ResponseDecision: domain.TaskInteractionApprove}) != "Interaction decision: APPROVE" ||
		a2aInteractionText(&domain.TaskInteraction{ResponseMessage: " reply "}) != "reply" {
		t.Fatal("interaction text fallback 错误")
	}
	for decision, want := range map[domain.TaskInteractionDecision]a2aext.InteractionDecision{
		domain.TaskInteractionApprove:           a2aext.DecisionApprove,
		domain.TaskInteractionApproveForSession: a2aext.DecisionApproveForSession,
		domain.TaskInteractionDeny:              a2aext.DecisionDeny,
		domain.TaskInteractionCancel:            a2aext.DecisionCancel,
	} {
		got, err := a2aInteractionDecision(decision, "", "")
		if err != nil || got != want {
			t.Fatalf("decision %q=%q err=%v", decision, got, err)
		}
	}
	if got, err := a2aInteractionDecision("", "response", ""); err != nil || got != a2aext.DecisionRespond {
		t.Fatalf("文本回复 decision=%q err=%v", got, err)
	}
	if _, err := a2aInteractionDecision("", "", ""); err == nil {
		t.Fatal("空 interaction reply 未失败")
	}
	operation, executionID, attempt := nextA2AStartIdentity(nil)
	if operation != domain.TaskA2AOperationStart || attempt != 1 || !strings.HasPrefix(executionID, "execution_") {
		t.Fatalf("首次 identity=%s/%s/%d", operation, executionID, attempt)
	}
	operation, executionID, attempt = nextA2AStartIdentity([]domain.TaskA2ARound{{Attempt: 2}, {Attempt: 4}, {Attempt: 3}})
	if operation != domain.TaskA2AOperationRetry || attempt != 5 || !strings.HasPrefix(executionID, "execution_") {
		t.Fatalf("重试 identity=%s/%s/%d", operation, executionID, attempt)
	}
}

func TestValidateTaskInteractionResponseRequiresKindSpecificDecision(t *testing.T) {
	tests := []struct {
		name    string
		kind    domain.TaskInteractionKind
		input   RespondTaskInteractionInput
		wantErr bool
	}{
		{name: "user text", kind: domain.TaskInteractionUserInput, input: RespondTaskInteractionInput{Message: "answer"}},
		{name: "user payload", kind: domain.TaskInteractionUserInput, input: RespondTaskInteractionInput{Payload: `{}`}},
		{name: "user cancel", kind: domain.TaskInteractionUserInput, input: RespondTaskInteractionInput{Decision: domain.TaskInteractionCancel}},
		{name: "user empty", kind: domain.TaskInteractionUserInput, input: RespondTaskInteractionInput{}, wantErr: true},
		{name: "user implicit approval", kind: domain.TaskInteractionUserInput, input: RespondTaskInteractionInput{Decision: domain.TaskInteractionApprove}, wantErr: true},
		{name: "approval explicit", kind: domain.TaskInteractionCommandApproval, input: RespondTaskInteractionInput{Decision: domain.TaskInteractionApprove}},
		{name: "approval missing", kind: domain.TaskInteractionFileApproval, input: RespondTaskInteractionInput{Message: "looks good"}, wantErr: true},
		{name: "permission deny", kind: domain.TaskInteractionPermissionApproval, input: RespondTaskInteractionInput{Decision: domain.TaskInteractionDeny}},
		{name: "unknown kind", kind: domain.TaskInteractionKind("UNKNOWN"), input: RespondTaskInteractionInput{Decision: domain.TaskInteractionDeny}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateTaskInteractionResponse(test.kind, test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateTaskInteractionResponse() err=%v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}
