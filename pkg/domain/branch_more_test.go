package domain

import (
	"errors"
	"testing"
	"time"
)

func TestAgentExecutionConfigRejectsCrossAgentAndInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		agent  AgentType
		config AgentExecutionConfig
	}{
		{"work mode", AgentCodex, AgentExecutionConfig{WorkMode: AgentWorkMode("invalid")}},
		{"agent", AgentType("invalid"), AgentExecutionConfig{}},
		{"codex on claude", AgentClaude, AgentExecutionConfig{Codex: CodexExecutionConfig{Model: "gpt"}}},
		{"claude on codex", AgentCodex, AgentExecutionConfig{Claude: ClaudeExecutionConfig{Model: "sonnet"}}},
		{"reasoning", AgentCodex, AgentExecutionConfig{Codex: CodexExecutionConfig{ReasoningEffort: CodexReasoningEffort("invalid")}}},
		{"sandbox", AgentCodex, AgentExecutionConfig{Codex: CodexExecutionConfig{SandboxMode: CodexSandboxMode("invalid")}}},
		{"approval", AgentCodex, AgentExecutionConfig{Codex: CodexExecutionConfig{ApprovalPolicy: CodexApprovalPolicy("invalid")}}},
		{"effort", AgentClaude, AgentExecutionConfig{Claude: ClaudeExecutionConfig{Effort: ClaudeEffort("invalid")}}},
		{"permission", AgentClaude, AgentExecutionConfig{Claude: ClaudeExecutionConfig{PermissionMode: ClaudePermissionMode("invalid")}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := testCase.config.NormalizedForAgent(testCase.agent); err == nil {
				t.Fatal("非法 Agent 配置未被拒绝")
			}
		})
	}

	codex, err := (AgentExecutionConfig{
		WorkMode: " implement ", Codex: CodexExecutionConfig{Model: " gpt ", ReasoningEffort: " high "},
		Claude: ClaudeExecutionConfig{},
	}).NormalizedForAgent(AgentCodex)
	if err != nil || codex.WorkMode != AgentWorkModeImplement || codex.Codex.Model != "gpt" || !codex.Claude.Empty() {
		t.Fatalf("Codex 配置规范化错误: %+v err=%v", codex, err)
	}
	claude, err := (AgentExecutionConfig{Claude: ClaudeExecutionConfig{Model: " sonnet "}}).NormalizedForAgent(AgentClaude)
	if err != nil || claude.Claude.Model != "sonnet" || !claude.Codex.Empty() {
		t.Fatalf("Claude 配置规范化错误: %+v err=%v", claude, err)
	}
}

func TestTaskTransitionRejectionAndAlternativeBranches(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	created := &Task{ID: "task", Status: TaskCreated, AgentType: AgentCodex, AgentConfig: AgentExecutionConfig{Codex: CodexExecutionConfig{Model: "gpt"}}}
	if err := created.AssignWorkerWithAgentConfig("", AgentCodex, nil, now); err == nil {
		t.Fatal("空 worker id 应失败")
	}
	missingAgent := &Task{ID: "task", Status: TaskCreated}
	if err := missingAgent.AssignWorkerWithAgent("worker", "", now); err == nil {
		t.Fatal("缺少 agent type 应失败")
	}
	if err := created.AssignWorkerWithAgent("worker", AgentClaude, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("Agent 冲突错误=%v", err)
	}
	invalidStored := &Task{ID: "task", Status: TaskCreated, AgentType: AgentCodex, AgentConfig: AgentExecutionConfig{Codex: CodexExecutionConfig{SandboxMode: "invalid"}}}
	if err := invalidStored.AssignWorkerWithAgent("worker", AgentCodex, now); err == nil {
		t.Fatal("已保存的非法配置应阻止分配")
	}
	running := &Task{ID: "task", Status: TaskRunning, AgentType: AgentCodex}
	if err := running.AssignWorkerWithAgent("worker", AgentCodex, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("运行中分配错误=%v", err)
	}
	if err := running.Update(NewTaskInput{Title: "title", ProjectID: "project", Now: now}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("运行中更新错误=%v", err)
	}

	update := &Task{ID: "task", Status: TaskCreated, AgentType: AgentCodex, AgentConfig: AgentExecutionConfig{Codex: CodexExecutionConfig{Model: "gpt"}}, StartDate: now, EndDate: now}
	if err := update.Update(NewTaskInput{Title: "title", ProjectID: "project", AgentType: AgentClaude, BaseBranch: "develop", OwnerUserID: "owner", Now: now}); err == nil {
		t.Fatal("更新为不兼容 Agent 应失败")
	}
	if err := update.Update(NewTaskInput{Title: "title", ProjectID: "project", AgentType: AgentCodex, BaseBranch: "develop", OwnerUserID: "owner", StartDate: now, EndDate: now, Now: now}); err != nil {
		t.Fatal(err)
	}
	if update.OwnerUserID != "owner" || update.BaseBranch != "develop" {
		t.Fatalf("任务更新未保存显式字段: %+v", update)
	}

	invalid := &Task{ID: "task", Status: TaskCreated}
	for name, call := range map[string]func() error{
		"log":             func() error { return invalid.AppendLog("stdout", "x", now) },
		"conversation":    func() error { return invalid.AppendConversation("assistant", "x", now) },
		"wait":            func() error { return invalid.WaitForInput(now) },
		"interrupt":       func() error { return invalid.RequestInterrupt(now) },
		"markInterrupted": func() error { return invalid.MarkInterrupted(now) },
		"complete":        func() error { return invalid.Complete("", now) },
		"result":          func() error { return invalid.RecordResult("x", now) },
	} {
		if err := call(); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s 非法转换错误=%v", name, err)
		}
	}
	if err := invalid.AppendUserConversation("answer", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("非完成态追加用户消息错误=%v", err)
	}

	waiting := &Task{ID: "task", Status: TaskWaitingInput, WorktreePath: "/old"}
	if err := waiting.MarkRunning("/new", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("worktree 变更错误=%v", err)
	}
	waiting.WorktreePath = ""
	if err := waiting.MarkRunning("/new", now); err != nil || waiting.Status != TaskWaitingInput || waiting.WorktreePath != "/new" {
		t.Fatalf("等待态绑定 worktree: %+v err=%v", waiting, err)
	}
	completed := &Task{ID: "task", Status: TaskCompleted}
	if err := completed.AppendUserConversation("continue", now); err != nil {
		t.Fatal(err)
	}
	if err := (&Task{ID: "task", Status: TaskRunning}).Complete("", now); err != nil {
		t.Fatal(err)
	}
	if err := (&Task{ID: "task", Status: TaskCompleted}).Archive(now); err != nil {
		t.Fatal(err)
	}
	if err := (&Task{ID: "task", Status: TaskInterrupted}).Archive(now); err != nil {
		t.Fatal(err)
	}
	if err := validateTaskDisplayDates(time.Time{}, now); err != nil {
		t.Fatalf("零起始日期应允许: %v", err)
	}
}

func TestWorkerValidationAndNormalizationBranches(t *testing.T) {
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	valid := NewWorkerInput{ID: "worker", Name: "Worker", WorkDir: "/work", SupportedAgents: []AgentType{AgentCodex}, Now: now}
	for name, mutate := range map[string]func(*NewWorkerInput){
		"id":        func(input *NewWorkerInput) { input.ID = "" },
		"name":      func(input *NewWorkerInput) { input.Name = "" },
		"workdir":   func(input *NewWorkerInput) { input.WorkDir = "" },
		"agents":    func(input *NewWorkerInput) { input.SupportedAgents = nil },
		"bad-agent": func(input *NewWorkerInput) { input.SupportedAgents = []AgentType{"invalid"} },
	} {
		input := valid
		mutate(&input)
		if _, err := NewWorker(input); err == nil {
			t.Fatalf("%s 非法 Worker 未被拒绝", name)
		}
	}
	worker, err := NewWorker(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.AssignTask("", now); err == nil {
		t.Fatal("空 task id 应失败")
	}
	if err := worker.AssignTask("task", now); err != nil {
		t.Fatal(err)
	}
	version := worker.Version
	if err := worker.AssignTask("task", now); err != nil || worker.Version != version {
		t.Fatalf("重复分配应幂等: version=%d err=%v", worker.Version, err)
	}
	worker.ReleaseTask("missing", now)
	if worker.Version != version {
		t.Fatal("释放未知任务不应变更 Worker")
	}

	existing := []WorkerAgentRuntimeEnv{{AgentType: AgentCodex, Vars: []AgentRuntimeEnvVar{{Key: "TOKEN", Value: "secret", Sensitive: true}}}}
	normalized := normalizeAgentRuntimeEnv([]WorkerAgentRuntimeEnv{
		{AgentType: AgentType("invalid"), Vars: []AgentRuntimeEnvVar{{Key: "IGNORED", Value: "x"}}},
		{AgentType: AgentCodex, Vars: []AgentRuntimeEnvVar{{Key: " ", Value: "x"}, {Key: "TOKEN", Sensitive: true}, {Key: "DUP", Value: "first"}, {Key: "DUP", Value: "last"}}},
		{AgentType: AgentClaude, Vars: []AgentRuntimeEnvVar{{Key: "IGNORED", Value: "x"}}},
	}, existing, []AgentType{AgentCodex, AgentType("invalid")})
	if len(normalized) != 1 || len(normalized[0].Vars) != 2 || normalized[0].Vars[0].Value != "secret" || normalized[0].Vars[1].Value != "last" {
		t.Fatalf("环境变量归一化错误: %+v", normalized)
	}
	withoutFilter := normalizeAgentRuntimeEnv([]WorkerAgentRuntimeEnv{{AgentType: AgentClaude, Vars: []AgentRuntimeEnvVar{{Key: "KEY", Value: "value"}}}}, nil, nil)
	if len(withoutFilter) != 1 || withoutFilter[0].AgentType != AgentClaude {
		t.Fatalf("无 supportedAgents 时应保留合法分组: %+v", withoutFilter)
	}
	if cloneAgentRuntimeEnv(nil) != nil {
		t.Fatal("nil runtime env clone 应保持 nil")
	}
}

func TestProjectExplicitDefaultsAndEmptyRemoteBindingStatus(t *testing.T) {
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)
	project, err := NewProject(NewProjectInput{ID: "project", Name: "Project", GitURL: "https://example.com/repo.git", DefaultBranch: "develop", WorktreeNamePrefix: "prefix", Now: now})
	if err != nil || project.DefaultBranch != "develop" || project.WorktreeNamePrefix != "prefix" {
		t.Fatalf("显式项目默认值错误: %+v err=%v", project, err)
	}
	if err := project.Update("Updated", project.GitURL, "", "", now); err != nil || project.DefaultBranch != "main" || project.WorktreeNamePrefix != project.ID {
		t.Fatalf("项目更新默认值错误: %+v err=%v", project, err)
	}
	round := &TaskA2ARound{}
	if err := round.BindRemote("remote", "context", "", now); err != nil || round.RemoteStatus != "" || round.UnknownSince != nil {
		t.Fatalf("空远端状态绑定错误: %+v err=%v", round, err)
	}
}
