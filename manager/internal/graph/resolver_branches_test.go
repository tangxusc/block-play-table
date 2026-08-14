package graph

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/graph/model"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

type branchReviewProxy struct {
	err           error
	includeBackup bool
	calls         []string
}

func (p *branchReviewProxy) ProxyTaskReview(_ context.Context, taskID, method, path string, input any, out any) error {
	p.calls = append(p.calls, method+" "+taskID+" "+path)
	if p.err != nil {
		return p.err
	}
	switch response := out.(type) {
	case *TaskGitDiffResponse:
		response.TaskID = taskID
	case *TaskGitStatusResponse:
		response.TaskID = taskID
	case *TaskGitChangeResponse:
		response.OK = true
		if p.includeBackup {
			response.Backup = &domain.TaskGitBackup{ID: "backup", TaskID: taskID}
		}
	case *TaskGitCommandResponse:
		response.OK = true
		response.Command = model.TaskGitCommandFetch.String()
	}
	return nil
}

func graphPtr[T any](value T) *T { return &value }

func TestResolverModelConversionsCoverOptionalBranches(t *testing.T) {
	if toModelTask(nil) != nil || toModelProject(nil) != nil || toModelWorker(nil) != nil ||
		toModelSettings(nil) != nil || toModelTaskInteraction(nil) != nil || toModelTaskGitDiff(nil) != nil ||
		toModelTaskGitChangeResult(nil) != nil || toModelTaskGitCommandResult(nil) != nil ||
		toModelTaskGitStatus(nil) != nil || toModelTaskGitBackup(nil) != nil {
		t.Fatal("nil domain 模型应转换为 nil GraphQL 模型")
	}
	now := time.Date(2026, 8, 14, 13, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	task := &domain.Task{ID: "task", CreatedAt: now, AgentConfig: domain.AgentExecutionConfig{
		WorkMode: domain.AgentWorkModeReview,
		Codex: domain.CodexExecutionConfig{
			Model: "gpt", ReasoningEffort: domain.CodexReasoningHigh, SandboxMode: domain.CodexSandboxReadOnly,
			ApprovalPolicy: domain.CodexApprovalNever, FullAuto: true,
		},
		Claude: domain.ClaudeExecutionConfig{
			Model: "claude", Effort: domain.ClaudeEffortMax, PermissionMode: domain.ClaudePermissionPlan,
		},
	}}
	converted := toModelTask(task)
	if converted.AgentType != nil || converted.StartDate.IsZero() || !converted.StartDate.Equal(converted.EndDate) {
		t.Fatalf("零日期/空 agent 转换错误: %+v", converted)
	}
	if converted.AgentConfig.WorkMode == nil || converted.AgentConfig.Codex == nil || converted.AgentConfig.Claude == nil ||
		converted.AgentConfig.Codex.ReasoningEffort == nil || converted.AgentConfig.Codex.SandboxMode == nil ||
		converted.AgentConfig.Codex.ApprovalPolicy == nil || converted.AgentConfig.Claude.Effort == nil ||
		converted.AgentConfig.Claude.PermissionMode == nil {
		t.Fatalf("完整 Agent 配置未保留: %+v", converted.AgentConfig)
	}
	if !modelTaskDisplayDate(time.Time{}).IsZero() {
		t.Fatal("零日期应保持零值")
	}
	interaction := toModelTaskInteraction(&domain.TaskInteraction{ResponseDecision: domain.TaskInteractionApprove})
	if interaction.ResponseDecision == nil {
		t.Fatal("interaction responseDecision 未转换")
	}
}

func TestResolverModelConversionsCoverEmptyOptionalBranches(t *testing.T) {
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	task := toModelTask(&domain.Task{
		ID: "task-with-dates", AgentType: domain.AgentClaude,
		StartDate: now, EndDate: now.Add(24 * time.Hour), CreatedAt: now.Add(-time.Hour),
	})
	if task.AgentType == nil || !task.StartDate.Equal(modelTaskDisplayDate(now)) ||
		!task.EndDate.Equal(modelTaskDisplayDate(now.Add(24*time.Hour))) {
		t.Fatalf("非空 Agent 与日期转换错误: %+v", task)
	}

	empty := toModelAgentExecutionConfig(domain.AgentExecutionConfig{})
	if empty.WorkMode != nil || empty.Codex != nil || empty.Claude != nil {
		t.Fatalf("空 Agent 配置不应生成可选字段: %+v", empty)
	}
	partial := toModelAgentExecutionConfig(domain.AgentExecutionConfig{
		Codex:  domain.CodexExecutionConfig{Model: "codex"},
		Claude: domain.ClaudeExecutionConfig{Model: "claude"},
	})
	if partial.Codex == nil || partial.Claude == nil || partial.Codex.ReasoningEffort != nil ||
		partial.Codex.SandboxMode != nil || partial.Codex.ApprovalPolicy != nil ||
		partial.Claude.Effort != nil || partial.Claude.PermissionMode != nil {
		t.Fatalf("部分 Agent 配置错误地生成了缺省枚举: %+v", partial)
	}

	if toModelTaskGitDiff(&TaskGitDiffResponse{TaskID: "task", Files: []TaskGitDiffFile{{Path: "README.md"}}}) == nil ||
		toModelTaskGitChangeResult(&TaskGitChangeResponse{}) == nil ||
		toModelTaskGitCommandResult(&TaskGitCommandResponse{}) == nil ||
		toModelTaskGitStatus(&TaskGitStatusResponse{}) == nil ||
		toModelTaskGitBackup(&domain.TaskGitBackup{ID: "backup"}) == nil {
		t.Fatal("非空 Git 响应应转换为 GraphQL 模型")
	}
}

func TestResolverInputConversionsCoverEveryOptionalField(t *testing.T) {
	mode := model.AgentWorkModeReview
	reasoning := model.CodexReasoningEffortHigh
	sandbox := model.CodexSandboxModeReadOnly
	approval := model.CodexApprovalPolicyNever
	effort := model.ClaudeEffortMax
	permission := model.ClaudePermissionModePlan
	input := &model.AgentExecutionConfigInput{
		WorkMode: &mode,
		Codex: &model.CodexExecutionConfigInput{
			Model: graphPtr("gpt"), ReasoningEffort: &reasoning, SandboxMode: &sandbox, ApprovalPolicy: &approval,
			FullAuto: graphPtr(true), BypassApprovalsAndSandbox: graphPtr(true),
		},
		Claude: &model.ClaudeExecutionConfigInput{Model: graphPtr("claude"), Effort: &effort, PermissionMode: &permission},
	}
	config := fromAgentExecutionConfigInput(input)
	if config == nil || config.WorkMode != domain.AgentWorkModeReview || config.Codex.Model != "gpt" ||
		config.Codex.ReasoningEffort != domain.CodexReasoningHigh || config.Codex.SandboxMode != domain.CodexSandboxReadOnly ||
		config.Codex.ApprovalPolicy != domain.CodexApprovalNever || !config.Codex.FullAuto || !config.Codex.BypassApprovalsAndSandbox ||
		config.Claude.Model != "claude" || config.Claude.Effort != domain.ClaudeEffortMax || config.Claude.PermissionMode != domain.ClaudePermissionPlan {
		t.Fatalf("Agent input 转换错误: %+v", config)
	}

	keyValues := fromKeyValueInputs([]*model.KeyValueInput{nil, {Key: "a", Value: "1"}})
	if keyValues["a"] != "1" || fromKeyValueInputs(nil) != nil || len(toKeyValues(map[string]string{"a": "1"})) != 1 {
		t.Fatal("key/value 可选输入转换错误")
	}
	env := fromWorkerAgentRuntimeEnvInputs([]*model.WorkerAgentRuntimeEnvInput{
		nil,
		{AgentType: model.AgentTypeCodex, Vars: []*model.AgentRuntimeEnvVarInput{
			nil,
			{Key: "TOKEN", Value: graphPtr("secret"), Description: graphPtr("token"), Enabled: true, Sensitive: true},
		}},
	})
	if len(env) != 1 || len(env[0].Vars) != 1 || env[0].Vars[0].Value != "secret" || fromWorkerAgentRuntimeEnvInputs(nil) != nil {
		t.Fatalf("Agent runtime env 转换错误: %+v", env)
	}
}

func TestResolverInputConversionsCoverOmittedOptionalFields(t *testing.T) {
	config := fromAgentExecutionConfigInput(&model.AgentExecutionConfigInput{})
	if config == nil || config.WorkMode != "" || !config.Codex.Empty() || !config.Claude.Empty() {
		t.Fatalf("空输入应保留空配置: %+v", config)
	}
	config = fromAgentExecutionConfigInput(&model.AgentExecutionConfigInput{
		Codex: &model.CodexExecutionConfigInput{}, Claude: &model.ClaudeExecutionConfigInput{},
	})
	if config == nil || !config.Codex.Empty() || !config.Claude.Empty() {
		t.Fatalf("空 Agent 子配置应保持空值: %+v", config)
	}
	if fromAgentExecutionConfigInput(nil) != nil {
		t.Fatal("nil Agent 输入应转换为 nil")
	}
}

func TestResolverFiltersSortsAndPageCoverConfiguredBranches(t *testing.T) {
	taskStatus := model.TaskStatus("RUNNING")
	workerStatus := model.WorkerStatus("ONLINE")
	agentType := model.AgentTypeCodex
	text := "value"
	flag := true
	taskSortField := model.TaskSortField("CREATED_AT")
	workerSortField := model.WorkerSortField("NAME")
	projectSortField := model.ProjectSortField("NAME")
	eventSortField := model.DomainEventSortField("OCCURRED_AT")
	direction := model.SortDirection("DESC")
	offset, limit := 2, 3

	if got := taskFilter(&model.TaskFilter{
		Status: &taskStatus, ProjectID: &text, WorkerID: &text, AgentType: &agentType,
		OwnerUserID: &text, IncludeArchived: &flag, Search: &text,
	}); got.Status == "" || got.ProjectID == "" || got.WorkerID == "" || got.AgentType == "" || got.OwnerUserID == "" || !got.IncludeArchived || got.Search == "" {
		t.Fatalf("task filter 转换错误: %+v", got)
	}
	if got := taskSort(&model.TaskSortInput{Field: &taskSortField, Direction: &direction}); got.Field == "" || got.Direction == "" {
		t.Fatalf("task sort 转换错误: %+v", got)
	}
	if got := workerFilter(&model.WorkerFilter{
		Status: &workerStatus, ProjectID: &text, AgentType: &agentType, IncludeDisabled: &flag, Search: &text,
	}); got.Status == "" || got.ProjectID == "" || got.AgentType == "" || !got.IncludeDisabled || got.Search == "" {
		t.Fatalf("worker filter 转换错误: %+v", got)
	}
	if got := workerSort(&model.WorkerSortInput{Field: &workerSortField, Direction: &direction}); got.Field == "" || got.Direction == "" {
		t.Fatalf("worker sort 转换错误: %+v", got)
	}
	if got := projectFilter(&model.ProjectFilter{IncludeArchived: &flag, Search: &text}); !got.IncludeArchived || got.Search == "" {
		t.Fatalf("project filter 转换错误: %+v", got)
	}
	if got := projectSort(&model.ProjectSortInput{Field: &projectSortField, Direction: &direction}); got.Field == "" || got.Direction == "" {
		t.Fatalf("project sort 转换错误: %+v", got)
	}
	if got := pageInput(&model.PageInput{Offset: &offset, Limit: &limit}); got.Offset != offset || got.Limit != limit {
		t.Fatalf("page 转换错误: %+v", got)
	}
	if got := eventFilter(&model.DomainEventFilter{AggregateID: &text, AggregateType: &text, EventType: &text, Search: &text}); got.AggregateID == "" || got.AggregateType == "" || got.EventType == "" || got.Search == "" {
		t.Fatalf("event filter 转换错误: %+v", got)
	}
	if got := domainEventSort(&model.DomainEventSortInput{Field: &eventSortField, Direction: &direction}); got.Field == "" || got.Direction == "" {
		t.Fatalf("event sort 转换错误: %+v", got)
	}
}

func TestResolverFiltersSortsAndPageCoverOmittedFields(t *testing.T) {
	if got := taskFilter(&model.TaskFilter{}); got != (app.TaskFilter{}) {
		t.Fatalf("空 task filter=%+v", got)
	}
	if got := taskSort(&model.TaskSortInput{}); got != (app.TaskSort{}) {
		t.Fatalf("空 task sort=%+v", got)
	}
	if got := workerFilter(&model.WorkerFilter{}); got != (app.WorkerFilter{}) {
		t.Fatalf("空 worker filter=%+v", got)
	}
	if got := workerSort(&model.WorkerSortInput{}); got != (app.WorkerSort{}) {
		t.Fatalf("空 worker sort=%+v", got)
	}
	if got := projectFilter(&model.ProjectFilter{}); got != (app.ProjectFilter{}) {
		t.Fatalf("空 project filter=%+v", got)
	}
	if got := projectSort(&model.ProjectSortInput{}); got != (app.ProjectSort{}) {
		t.Fatalf("空 project sort=%+v", got)
	}
	if got := pageInput(&model.PageInput{}); got != (app.PageInput{}) {
		t.Fatalf("空 page input=%+v", got)
	}
	if got := eventFilter(&model.DomainEventFilter{}); got != (domain.EventFilter{}) {
		t.Fatalf("空 event filter=%+v", got)
	}
	if got := domainEventSort(&model.DomainEventSortInput{}); got != (app.DomainEventSort{}) {
		t.Fatalf("空 event sort=%+v", got)
	}
}

func TestResolverSmallHelpersCoverAlternateBranches(t *testing.T) {
	primary, secondary := "primary", "secondary"
	if firstID(&primary, &secondary) != primary || firstID(nil, &secondary) != secondary || firstID(nil, nil) != "" {
		t.Fatal("firstID 分支错误")
	}
	if optionalString("") != nil || optionalString("x") == nil || valueOrEmpty(nil) != "" || valueOrEmpty(&primary) != primary {
		t.Fatal("optional string helper 分支错误")
	}
	if optionalTime(nil) != nil || optionalTime(graphPtr(time.Now())) == nil {
		t.Fatal("optional time helper 分支错误")
	}
	if eventPayloadString(domain.DomainEvent{Payload: []byte("{")}, "key") != "" ||
		eventPayloadString(domain.DomainEvent{Payload: []byte(`{"key":1}`)}, "key") != "" ||
		eventPayloadString(domain.DomainEvent{Payload: []byte(`{"key":"value"}`)}, "key") != "value" {
		t.Fatal("event payload helper 分支错误")
	}
	items, total := paginateResolverItems([]int{1, 2}, structToPage(-1, 1))
	if total != 2 || len(items) != 1 || items[0] != 1 {
		t.Fatalf("负 offset 分页错误: %#v total=%d", items, total)
	}
	items, _ = paginateResolverItems([]int{1, 2}, structToPage(9, 1))
	if len(items) != 0 {
		t.Fatalf("超限 offset 分页错误: %#v", items)
	}
}

func TestResolverReviewProxyCoversOptionalAndErrorBranches(t *testing.T) {
	ctx := context.Background()
	withoutProxy := NewResolver(app.NewService(store.NewMemoryStore()), nil)
	if _, err := withoutProxy.taskGitDiff(ctx, "task", model.TaskGitDiffScopeUncommitted, nil); err == nil {
		t.Fatal("未配置代理的 diff 请求应失败")
	}
	if _, err := withoutProxy.taskGitStatus(ctx, "task", nil, nil); err == nil {
		t.Fatal("未配置代理的 status 请求应失败")
	}
	if _, err := withoutProxy.gitChange(ctx, "stage", model.TaskGitChangeInput{TaskID: "task"}); err == nil {
		t.Fatal("未配置代理的 change 请求应失败")
	}
	if _, err := withoutProxy.runTaskGitCommand(ctx, model.TaskGitCommandInput{TaskID: "task"}); err == nil {
		t.Fatal("未配置代理的 command 请求应失败")
	}

	proxy := &branchReviewProxy{}
	resolver := NewResolver(app.NewService(store.NewMemoryStore()), proxy)
	staged := true
	if _, err := resolver.taskGitDiff(ctx, "task", model.TaskGitDiffScopeUncommitted, nil); err != nil {
		t.Fatalf("无 staged diff: %v", err)
	}
	if _, err := resolver.taskGitDiff(ctx, "task", model.TaskGitDiffScopeBranch, &staged); err != nil {
		t.Fatalf("带 staged diff: %v", err)
	}
	if _, err := resolver.taskGitStatus(ctx, "task", nil, nil); err != nil {
		t.Fatalf("无参数 status: %v", err)
	}
	remote, branch := "origin", "main"
	if _, err := resolver.taskGitStatus(ctx, "task", &remote, &branch); err != nil {
		t.Fatalf("带参数 status: %v", err)
	}
	if _, err := resolver.gitChange(ctx, "stage", model.TaskGitChangeInput{TaskID: "task"}); err != nil {
		t.Fatalf("无 backup change: %v", err)
	}
	proxy.includeBackup = true
	backupID := "prior-backup"
	if _, err := resolver.gitChange(ctx, "restore", model.TaskGitChangeInput{TaskID: "task", BackupID: &backupID}); err != nil {
		t.Fatalf("带 backup change: %v", err)
	}
	if _, err := resolver.runTaskGitCommand(ctx, model.TaskGitCommandInput{TaskID: "task", Command: model.TaskGitCommandFetch}); err != nil {
		t.Fatalf("无发布策略 command: %v", err)
	}
	strategy := model.TaskGitPublishStrategyFastForward
	if _, err := resolver.runTaskGitCommand(ctx, model.TaskGitCommandInput{TaskID: "task", Command: model.TaskGitCommandPublish, PublishStrategy: &strategy}); err != nil {
		t.Fatalf("带发布策略 command: %v", err)
	}
	if len(proxy.calls) != 8 || proxy.calls[0] != http.MethodGet+" task /diff?scope=UNCOMMITTED" {
		t.Fatalf("Review 代理调用错误: %#v", proxy.calls)
	}

	proxy.err = errors.New("proxy failed")
	if _, err := resolver.taskGitDiff(ctx, "task", model.TaskGitDiffScopeUncommitted, nil); !errors.Is(err, proxy.err) {
		t.Fatalf("diff 未透传代理错误: %v", err)
	}
	if _, err := resolver.taskGitStatus(ctx, "task", nil, nil); !errors.Is(err, proxy.err) {
		t.Fatalf("status 未透传代理错误: %v", err)
	}
	if _, err := resolver.gitChange(ctx, "stage", model.TaskGitChangeInput{TaskID: "task"}); !errors.Is(err, proxy.err) {
		t.Fatalf("change 未透传代理错误: %v", err)
	}
	if _, err := resolver.runTaskGitCommand(ctx, model.TaskGitCommandInput{TaskID: "task"}); !errors.Is(err, proxy.err) {
		t.Fatalf("command 未透传代理错误: %v", err)
	}
}

func structToPage(offset, limit int) app.PageInput {
	return app.PageInput{Offset: offset, Limit: limit}
}
