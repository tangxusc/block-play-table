package graph

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/graph/model"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestResolverCRUDQueriesAndA2AViews(t *testing.T) {
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)
	st := store.NewMemoryStore()
	service := app.NewService(st, app.WithClock(func() time.Time { return now }))
	resolver := NewResolver(service, nil)
	mutation := resolver.Mutation()
	query := resolver.Query()
	ctx := app.WithUserIdentity(context.Background(), "user-1", true)

	project, err := mutation.CreateProject(ctx, model.CreateProjectInput{Name: "Project", GitURL: "https://example.test/repo.git"})
	if err != nil || project == nil {
		t.Fatalf("CreateProject: project=%+v err=%v", project, err)
	}
	workerID := "worker-1"
	mode := model.WorkerProjectBindingModeAllProjects
	worker, err := mutation.CreateWorker(ctx, model.CreateWorkerInput{
		ID: &workerID, Name: "Worker", SupportedAgents: []model.AgentType{model.AgentTypeCodex},
		WorkDir: "/tmp/worker", ProjectBindingMode: &mode,
		Capabilities: []*model.KeyValueInput{nil, {Key: "a2a", Value: "true"}},
	})
	if err != nil || worker == nil {
		t.Fatalf("CreateWorker: worker=%+v err=%v", worker, err)
	}
	if _, err := mutation.RegisterWorker(ctx, model.RegisterWorkerInput{
		ID: &workerID, Name: "Worker", SupportedAgents: []model.AgentType{model.AgentTypeCodex},
		WorkDir: "/tmp/worker", ProjectBindingMode: &mode,
	}); err != nil {
		t.Fatalf("RegisterWorker 幂等更新: %v", err)
	}

	agent := model.AgentTypeCodex
	description, branch, owner := "Description", "main", "owner-override"
	start, end := now.Add(-24*time.Hour), now.Add(24*time.Hour)
	task, err := mutation.CreateTask(ctx, model.CreateTaskInput{
		Title: "Task", Description: &description, ProjectID: project.ID, AgentType: &agent,
		BaseBranch: &branch, OwnerUserID: &owner, StartDate: &start, EndDate: &end,
		PreCommands: []string{"pre"}, PostCommands: []string{"post"},
	})
	if err != nil || task == nil || task.OwnerUserID != owner {
		t.Fatalf("CreateTask: task=%+v err=%v", task, err)
	}
	task.Title = "should not mutate storage"
	updated, err := mutation.UpdateTask(ctx, model.UpdateTaskInput{
		ID: task.ID, Title: "Updated", Description: &description, ProjectID: project.ID,
		AgentType: &agent, BaseBranch: &branch, StartDate: &start, EndDate: &end,
	})
	if err != nil || updated.Title != "Updated" {
		t.Fatalf("UpdateTask: task=%+v err=%v", updated, err)
	}
	if _, err := mutation.AssignWorker(ctx, nil, nil, nil); err == nil {
		t.Fatal("AssignWorker 缺少 identity 未失败")
	}
	assigned, err := mutation.AssignWorker(ctx, &model.AssignWorkerInput{TaskID: task.ID, WorkerID: workerID, AgentType: &agent}, nil, nil)
	if err != nil || assigned.WorkerID == nil || *assigned.WorkerID != workerID {
		t.Fatalf("AssignWorker: task=%+v err=%v", assigned, err)
	}
	if _, err := mutation.StartTask(ctx, nil, nil, nil); err == nil {
		t.Fatal("StartTask 缺少 taskId 未失败")
	}
	if _, err := mutation.InterruptTask(ctx, nil, nil); err == nil {
		t.Fatal("InterruptTask 缺少 taskId 未失败")
	}
	if _, err := mutation.ArchiveTask(ctx, nil, nil); err == nil {
		t.Fatal("ArchiveTask 缺少 taskId 未失败")
	}
	if ok, err := mutation.DeleteTask(ctx, nil, nil); err == nil || ok {
		t.Fatal("DeleteTask 缺少 taskId 未失败")
	}
	if _, err := mutation.RetryTask(ctx, nil, nil); err == nil {
		t.Fatal("RetryTask 缺少 taskId 未失败")
	}

	if got, err := query.Task(ctx, task.ID); err != nil || got == nil || got.Title != "Updated" {
		t.Fatalf("Task query: task=%+v err=%v", got, err)
	}
	if got, err := query.Task(ctx, "missing"); err != nil || got != nil {
		t.Fatalf("缺失 Task query: task=%+v err=%v", got, err)
	}
	offset, limit := 0, 1
	if connection, err := query.Tasks(ctx, nil, nil, &model.PageInput{Offset: &offset, Limit: &limit}); err != nil || connection.TotalCount != 1 || len(connection.Nodes) != 1 {
		t.Fatalf("Tasks connection=%+v err=%v", connection, err)
	}
	if list, err := query.TaskList(ctx, &model.TaskFilter{ProjectID: &project.ID}, nil, nil); err != nil || len(list) != 1 {
		t.Fatalf("TaskList=%+v err=%v", list, err)
	}
	if got, err := query.Worker(ctx, workerID); err != nil || got.ID != workerID {
		t.Fatalf("Worker query=%+v err=%v", got, err)
	}
	if got, err := query.Workers(ctx, nil, nil); err != nil || len(got) != 1 {
		t.Fatalf("Workers=%+v err=%v", got, err)
	}
	if got, err := query.WorkersConnection(ctx, nil, nil, nil); err != nil || got.TotalCount != 1 {
		t.Fatalf("WorkersConnection=%+v err=%v", got, err)
	}
	if got, err := query.Project(ctx, project.ID); err != nil || got.ID != project.ID {
		t.Fatalf("Project query=%+v err=%v", got, err)
	}
	if got, err := query.Projects(ctx, nil, nil); err != nil || len(got) != 1 {
		t.Fatalf("Projects=%+v err=%v", got, err)
	}
	if got, err := query.ProjectsConnection(ctx, nil, nil, nil); err != nil || got.TotalCount != 1 {
		t.Fatalf("ProjectsConnection=%+v err=%v", got, err)
	}
	for _, boardID := range []*string{nil, stringPointer("calendar"), stringPointer("list"), stringPointer("custom")} {
		if board, err := query.Board(ctx, boardID, nil, nil, nil); err != nil || len(board.Columns) != 10 || len(board.Tasks) != 1 {
			t.Fatalf("Board(%v)=%+v err=%v", boardID, board, err)
		}
	}
	if settings, err := query.Settings(ctx); err != nil || settings == nil {
		t.Fatalf("Settings=%+v err=%v", settings, err)
	}
	if current, err := query.CurrentUser(ctx); err != nil || current.ID != "user-1" || !current.TrustMode {
		t.Fatalf("CurrentUser=%+v err=%v", current, err)
	}
	if employee, err := query.EmployeeByID(ctx, "employee-1"); err != nil || employee == nil {
		t.Fatalf("EmployeeByID=%+v err=%v", employee, err)
	}
	if employees, err := query.Employees(ctx); err != nil || len(employees) == 0 {
		t.Fatalf("Employees=%+v err=%v", employees, err)
	}

	if err := st.AppendTaskLog(ctx, domain.TaskLog{ID: "log", TaskID: task.ID, Stream: "stdout", Content: "line", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendConversation(ctx, domain.ConversationMessage{ID: "message", TaskID: task.ID, Role: "assistant", Content: "reply", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	interaction := domain.TaskInteraction{ID: "interaction", TaskID: task.ID, Kind: domain.TaskInteractionUserInput, Status: domain.TaskInteractionPending, CreatedAt: now, UpdatedAt: now}
	if err := st.SaveTaskInteraction(ctx, interaction); err != nil {
		t.Fatal(err)
	}
	if logs, err := query.TaskLogs(ctx, task.ID); err != nil || len(logs) != 1 || logs[0].Content != "line" {
		t.Fatalf("TaskLogs=%+v err=%v", logs, err)
	}
	if conversations, err := query.TaskConversations(ctx, task.ID); err != nil || len(conversations) != 1 {
		t.Fatalf("TaskConversations=%+v err=%v", conversations, err)
	}
	status := model.TaskInteractionStatusPending
	if interactions, err := query.TaskInteractions(ctx, task.ID, &status); err != nil || len(interactions) != 1 {
		t.Fatalf("TaskInteractions=%+v err=%v", interactions, err)
	}
	if rounds, err := query.TaskA2AExecutions(ctx, task.ID); err != nil || len(rounds) != 0 {
		t.Fatalf("TaskA2AExecutions=%+v err=%v", rounds, err)
	}
	if events, err := query.TaskEvents(ctx, task.ID); err != nil || len(events) == 0 {
		t.Fatalf("TaskEvents=%+v err=%v", events, err)
	}
	if events, err := query.DomainEvents(ctx, nil, nil, &task.ID, nil, nil); err != nil || len(events) == 0 {
		t.Fatalf("DomainEvents=%+v err=%v", events, err)
	}
	if events, err := query.DomainEventsConnection(ctx, nil, nil, &task.ID, nil, nil, nil); err != nil || events.TotalCount == 0 {
		t.Fatalf("DomainEventsConnection=%+v err=%v", events, err)
	}
	includePublished := true
	if outbox, err := query.OutboxMessages(ctx, &includePublished); err != nil || len(outbox) == 0 {
		t.Fatalf("OutboxMessages=%+v err=%v", outbox, err)
	}

	if _, err := mutation.UpdateWorkerHeartbeatTimeout(ctx, "45s"); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.DisableWorker(ctx, workerID); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.EnableWorker(ctx, workerID); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.UpdateWorkerProjectBindings(ctx, model.UpdateWorkerProjectBindingsInput{WorkerID: workerID, ProjectBindingMode: model.WorkerProjectBindingModeSpecificProjects, BoundProjectIds: []string{project.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.UpdateWorker(ctx, model.UpdateWorkerInput{ID: workerID, Name: "Worker 2", SupportedAgents: []model.AgentType{agent}, WorkDir: "/tmp/worker-2", ProjectBindingMode: model.WorkerProjectBindingModeAllProjects}); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.UpdateProject(ctx, model.UpdateProjectInput{ID: project.ID, Name: "Project 2", GitURL: project.GitURL, DefaultBranch: "main", WorktreeNamePrefix: "task"}); err != nil {
		t.Fatal(err)
	}

	disposableProject, err := mutation.CreateProject(ctx, model.CreateProjectInput{Name: "Disposable", GitURL: "https://example.test/disposable.git"})
	if err != nil {
		t.Fatal(err)
	}
	disposableTask, err := mutation.CreateTask(ctx, model.CreateTaskInput{Title: "Disposable", ProjectID: disposableProject.ID, AgentType: &agent})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendConversation(ctx, domain.ConversationMessage{ID: "fallback-message", TaskID: disposableTask.ID, Role: "assistant", Content: "fallback", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if logs, err := query.TaskLogs(ctx, disposableTask.ID); err != nil || len(logs) != 1 || logs[0].Stream != "assistant" {
		t.Fatalf("conversation 日志回退=%+v err=%v", logs, err)
	}
	archived, err := mutation.ArchiveTask(ctx, &disposableTask.ID, nil)
	if err != nil || archived.Status != model.TaskStatusArchived {
		t.Fatalf("ArchiveTask=%+v err=%v", archived, err)
	}
	if deleted, err := mutation.DeleteTask(ctx, &disposableTask.ID, nil); err != nil || !deleted {
		t.Fatalf("DeleteTask deleted=%v err=%v", deleted, err)
	}
	if archivedProject, err := mutation.ArchiveProject(ctx, disposableProject.ID); err != nil || !archivedProject.Archived {
		t.Fatalf("ArchiveProject=%+v err=%v", archivedProject, err)
	}
	disposableWorkerID := "worker-disposable"
	if _, err := mutation.CreateWorker(ctx, model.CreateWorkerInput{ID: &disposableWorkerID, Name: "Disposable Worker", SupportedAgents: []model.AgentType{agent}, WorkDir: "/tmp/disposable", ProjectBindingMode: &mode}); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.DisableWorker(ctx, disposableWorkerID); err != nil {
		t.Fatal(err)
	}
	if deleted, err := mutation.DeleteWorker(ctx, disposableWorkerID); err != nil || !deleted {
		t.Fatalf("DeleteWorker deleted=%v err=%v", deleted, err)
	}

	subscription := resolver.Subscription()
	for name, subscribe := range map[string]func(context.Context) error{
		"task": func(subCtx context.Context) error {
			_, err := subscription.TaskUpdated(subCtx, task.ID)
			return err
		},
		"log": func(subCtx context.Context) error {
			_, err := subscription.TaskLogAppended(subCtx, task.ID)
			return err
		},
		"conversation": func(subCtx context.Context) error {
			_, err := subscription.TaskConversationAppended(subCtx, task.ID)
			return err
		},
		"worker": func(subCtx context.Context) error {
			_, err := subscription.WorkerUpdated(subCtx, &workerID)
			return err
		},
		"events": func(subCtx context.Context) error {
			_, err := subscription.DomainEvents(subCtx, nil)
			return err
		},
	} {
		t.Run("subscription "+name, func(t *testing.T) {
			subCtx, cancel := context.WithCancel(ctx)
			if err := subscribe(subCtx); err != nil {
				t.Fatal(err)
			}
			cancel()
		})
	}
}

func TestResolverEnumConversionsAndPaginationBoundaries(t *testing.T) {
	for _, mode := range []domain.AgentWorkMode{domain.AgentWorkModePlan, domain.AgentWorkModeReview, domain.AgentWorkModeImplement} {
		if got := fromModelAgentWorkMode(toModelAgentWorkMode(mode)); got != mode {
			t.Fatalf("work mode 往返=%q，期望 %q", got, mode)
		}
	}
	for _, value := range []domain.CodexReasoningEffort{domain.CodexReasoningMinimal, domain.CodexReasoningLow, domain.CodexReasoningMedium, domain.CodexReasoningHigh, domain.CodexReasoningXHigh} {
		if got := fromModelCodexReasoningEffort(toModelCodexReasoningEffort(value)); got != value {
			t.Fatalf("reasoning effort 往返=%q，期望 %q", got, value)
		}
	}
	for _, value := range []domain.CodexSandboxMode{domain.CodexSandboxReadOnly, domain.CodexSandboxWorkspaceWrite, domain.CodexSandboxDangerFullAccess} {
		if got := fromModelCodexSandboxMode(toModelCodexSandboxMode(value)); got != value {
			t.Fatalf("sandbox mode 往返=%q，期望 %q", got, value)
		}
	}
	for _, value := range []domain.CodexApprovalPolicy{domain.CodexApprovalUntrusted, domain.CodexApprovalOnFailure, domain.CodexApprovalOnRequest, domain.CodexApprovalNever} {
		if got := fromModelCodexApprovalPolicy(toModelCodexApprovalPolicy(value)); got != value {
			t.Fatalf("approval policy 往返=%q，期望 %q", got, value)
		}
	}
	for _, value := range []domain.ClaudeEffort{domain.ClaudeEffortLow, domain.ClaudeEffortMedium, domain.ClaudeEffortHigh, domain.ClaudeEffortXHigh, domain.ClaudeEffortMax} {
		if got := fromModelClaudeEffort(toModelClaudeEffort(value)); got != value {
			t.Fatalf("Claude effort 往返=%q，期望 %q", got, value)
		}
	}
	for _, value := range []domain.ClaudePermissionMode{domain.ClaudePermissionAcceptEdits, domain.ClaudePermissionAuto, domain.ClaudePermissionBypassPermissions, domain.ClaudePermissionDefault, domain.ClaudePermissionDontAsk, domain.ClaudePermissionPlan} {
		if got := fromModelClaudePermissionMode(toModelClaudePermissionMode(value)); got != value {
			t.Fatalf("Claude permission 往返=%q，期望 %q", got, value)
		}
	}
	items, total := paginateResolverItems([]int{1, 2, 3}, app.PageInput{Offset: -1, Limit: 2})
	if total != 3 || len(items) != 2 || items[0] != 1 {
		t.Fatalf("负 offset 分页=%v total=%d", items, total)
	}
	items, total = paginateResolverItems([]int{1, 2, 3}, app.PageInput{Offset: 9, Limit: 2})
	if total != 3 || len(items) != 0 {
		t.Fatalf("越界 offset 分页=%v total=%d", items, total)
	}
	now := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	tasks := []*domain.Task{{ID: "old", CreatedAt: now.Add(-time.Hour)}, {ID: "new-b", CreatedAt: now}, {ID: "new-a", CreatedAt: now}}
	page, total := taskPageNewestFirst(tasks, app.PageInput{Offset: 1, Limit: 1})
	if total != 3 || len(page) != 1 || page[0].ID != "new-a" {
		t.Fatalf("newest first page=%+v total=%d", page, total)
	}
	event := domain.DomainEvent{Payload: []byte(`{"stream":"stdout","count":1}`)}
	if eventPayloadString(event, "stream") != "stdout" || eventPayloadString(event, "count") != "" || eventPayloadString(domain.DomainEvent{Payload: []byte(`{`)}, "stream") != "" {
		t.Fatal("eventPayloadString 边界错误")
	}
}

func stringPointer(value string) *string { return &value }
