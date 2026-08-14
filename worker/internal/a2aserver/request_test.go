package a2aserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestParseExecutionRequestValidatesOperationTaskAndContextIdentity(t *testing.T) {
	tests := []struct {
		name      string
		operation a2aext.Operation
		taskID    a2a.TaskID
		contextID string
		wantError bool
	}{
		{name: "START 新 Task 和 Context", operation: a2aext.OperationStart},
		{name: "START 禁止既有 Context", operation: a2aext.OperationStart, contextID: "context-old", wantError: true},
		{name: "RETRY 新 Task 和 Context", operation: a2aext.OperationRetry},
		{name: "RETRY 禁止既有 Task", operation: a2aext.OperationRetry, taskID: "task-old", wantError: true},
		{name: "CONTINUE 复用 Context 并新建 Task", operation: a2aext.OperationContinue, contextID: "context-old"},
		{name: "CONTINUE 缺少 Context", operation: a2aext.OperationContinue, wantError: true},
		{name: "CONTINUE 禁止既有 Task", operation: a2aext.OperationContinue, taskID: "task-old", contextID: "context-old", wantError: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := serverTestRequest(testCase.operation)
			message := serverTestMessage(request)
			message.TaskID = testCase.taskID
			message.ContextID = testCase.contextID
			_, err := ParseExecutionRequest(message, "worker-1")
			if (err != nil) != testCase.wantError {
				t.Fatalf("ParseExecutionRequest() err=%v, wantError=%v", err, testCase.wantError)
			}
		})
	}
}

func TestParseExecutionRequestRequiresMessageCommandIDAndStrictData(t *testing.T) {
	request := serverTestRequest(a2aext.OperationStart)
	mismatched := serverTestMessage(request)
	mismatched.ID = "different-command"
	if _, err := ParseExecutionRequest(mismatched, "worker-1"); err == nil {
		t.Fatal("message.id 与 command.id 不一致时应拒绝")
	}

	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["unknown"] = true
	unknown := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("执行任务"), a2a.NewDataPart(value))
	unknown.ID = request.Command.ID
	unknown.Extensions = []string{a2aext.ExtensionURI}
	if _, err := ParseExecutionRequest(unknown, "worker-1"); err == nil {
		t.Fatal("execution DataPart 未知字段应拒绝")
	}
}

func TestBuildAgentCardAlwaysPublishesBothAdapterSkills(t *testing.T) {
	card := buildAgentCard(Config{
		WorkerID: "worker-1", AgentTypes: []a2aext.AgentType{a2aext.AgentCodex},
	}, "http://127.0.0.1:18081/a2a")
	if len(card.Skills) != 2 || card.Skills[0].ID != "block-play-table-codex" || card.Skills[1].ID != "block-play-table-claude" {
		t.Fatalf("Agent Card Skills = %+v", card.Skills)
	}
	if !card.Capabilities.Streaming || len(card.Capabilities.Extensions) != 1 || !card.Capabilities.Extensions[0].Required {
		t.Fatalf("Agent Card capabilities = %+v", card.Capabilities)
	}
}

func serverTestMessage(request *a2aext.ExecutionRequest) *a2a.Message {
	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("执行任务"), a2a.NewDataPart(request))
	message.ID = request.Command.ID
	message.Extensions = []string{a2aext.ExtensionURI}
	return message
}

func serverTestRequest(operation a2aext.Operation) *a2aext.ExecutionRequest {
	request := &a2aext.ExecutionRequest{
		Kind: a2aext.RequestKind, Version: a2aext.Version,
		Command: a2aext.Command{ID: "command-1", Operation: operation, IssuedAt: time.Now().UTC()},
		Scope: a2aext.RequestScope{
			LocalTaskID: "local-task-1", ExecutionID: "execution-1", Attempt: 1, Turn: 1, ExpectedWorkerID: "worker-1",
		},
		Task:     a2aext.TaskSpec{Title: "测试任务", Description: "验证请求", BaseBranch: "main"},
		Agent:    a2aext.AgentSpec{Type: a2aext.AgentCodex, WorkMode: a2aext.WorkModeImplement},
		Project:  a2aext.ProjectSpec{ID: "project-1", GitURL: "https://example.com/repository.git", DefaultBranch: "main", WorktreeNamePrefix: "task"},
		Commands: a2aext.Commands{Pre: []string{}, Post: []string{}},
		Environment: a2aext.Environment{
			Variables: []a2aext.EnvironmentVariable{},
		},
	}
	switch operation {
	case a2aext.OperationRetry:
		request.Worktree.Mode = a2aext.WorktreeRecreate
	case a2aext.OperationContinue:
		request.Scope.Turn = 2
		request.Worktree.Mode = a2aext.WorktreeResume
		request.Resume = &a2aext.Resume{AgentSessionID: "session-1", WorktreePath: "/tmp/worktree-1"}
	default:
		request.Worktree.Mode = a2aext.WorktreeCreate
	}
	return request
}
