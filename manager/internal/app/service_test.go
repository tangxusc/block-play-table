package app

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceCreatesAssignsStartsAndCompletesTask(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))

	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-1",
		Name:            "local",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatalf("RegisterWorker returned error: %v", err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatalf("WorkerConnected returned error: %v", err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:       "Implement",
		Description: "Do it",
		ProjectID:   project.ID,
		AgentType:   domain.AgentCodex,
		BaseBranch:  "main",
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatalf("AssignWorker returned error: %v", err)
	}
	started, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if started.Status != domain.TaskStarting {
		t.Fatalf("status = %s, want %s", started.Status, domain.TaskStarting)
	}
	request := preparedA2ARequestForTask(t, ctx, service, task.ID)
	if request.Request.Scope.LocalTaskID != task.ID || request.Request.Project.ID != project.ID {
		t.Fatalf("A2A request should include task and project")
	}
	if len(request.Request.Environment.Variables) != 0 {
		t.Fatalf("default settings should not send env vars")
	}
	bindTestA2ARound(t, ctx, service, task.ID)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventWorkspaceReady, domain.TaskA2ARemoteStatusWorking, &a2aext.RuntimeInfo{WorktreePath: "/tmp/worktree"}, map[string]any{})
	completed := applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusCompleted, &a2aext.RuntimeInfo{WorktreePath: "/tmp/worktree"}, map[string]any{"status": string(a2aext.TerminalCompleted), "result": "done"})
	if completed.Status != domain.TaskCompleted {
		t.Fatalf("status = %s, want completed", completed.Status)
	}
}

func TestServiceContinuesCompletedTaskOnOriginalWorker(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-continue",
		Name:            "W",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	config := domain.AgentExecutionConfig{
		WorkMode: domain.AgentWorkModeImplement,
		Codex: domain.CodexExecutionConfig{
			Model:           "gpt-5.4",
			ReasoningEffort: domain.CodexReasoningHigh,
			ApprovalPolicy:  domain.CodexApprovalNever,
		},
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:       "T",
		ProjectID:   project.ID,
		WorkerID:    worker.ID,
		AgentType:   domain.AgentCodex,
		AgentConfig: &config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	startRequest := preparedA2ARequestForTask(t, ctx, service, task.ID)
	if startRequest.Request.Agent.Config.Model != "gpt-5.4" {
		t.Fatalf("start request agent config = %+v", startRequest.Request.Agent.Config)
	}
	bindTestA2ARound(t, ctx, service, task.ID)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventWorkspaceReady, domain.TaskA2ARemoteStatusWorking, &a2aext.RuntimeInfo{WorktreePath: "/tmp/worktree"}, map[string]any{})
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusCompleted, &a2aext.RuntimeInfo{AgentSessionID: "session-1", WorktreePath: "/tmp/worktree"}, map[string]any{"status": string(a2aext.TerminalCompleted), "result": "first result"})

	continued, err := service.ContinueTask(ctx, ContinueTaskInput{TaskID: task.ID, Message: "follow up"})
	if err != nil {
		t.Fatalf("ContinueTask returned error: %v", err)
	}
	if continued.Status != domain.TaskStarting || continued.Result != "" || continued.AgentSessionID != "session-1" {
		t.Fatalf("continued task = %+v", continued)
	}
	continueRequest := preparedA2ARequestForTask(t, ctx, service, task.ID)
	if continueRequest.Request.Scope.LocalTaskID != task.ID || continueRequest.Text != "follow up" || continueRequest.Request.Resume.AgentSessionID != "session-1" || continueRequest.Request.Resume.WorktreePath != "/tmp/worktree" {
		t.Fatalf("continue request = %+v", continueRequest)
	}
	if continueRequest.Request.Agent.Config.Model != "gpt-5.4" || continueRequest.Request.Agent.Config.ReasoningEffort != string(domain.CodexReasoningHigh) {
		t.Fatalf("continue request agent config = %+v", continueRequest.Request.Agent.Config)
	}
	loadedWorker, err := service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedWorker.CurrentTaskIDs) != 1 || loadedWorker.CurrentTaskIDs[0] != task.ID {
		t.Fatalf("worker current tasks = %+v, want [%q]", loadedWorker.CurrentTaskIDs, task.ID)
	}
	messages, err := service.Store().TaskConversations(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "user" || messages[0].Content != "follow up" {
		t.Fatalf("conversation messages = %+v", messages)
	}
}

func TestServiceDeduplicatesWorkerMessages(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)

	round, event := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventLogChunk, nil, map[string]any{"stream": string(a2aext.LogStdout), "content": "first"})
	if err := applyTestA2ARawEvent(ctx, service, round, event, domain.TaskA2ARemoteStatusWorking); err != nil {
		t.Fatalf("首次 A2A log.chunk 返回错误: %v", err)
	}
	if err := applyTestA2ARawEvent(ctx, service, round, event, domain.TaskA2ARemoteStatusWorking); err != nil {
		t.Fatalf("重复 A2A log.chunk 返回错误: %v", err)
	}
	logs, err := service.Store().TaskLogs(ctx, task.ID)
	if err != nil {
		t.Fatalf("TaskLogs returned error: %v", err)
	}
	if len(logs) != 1 || logs[0].Content != "first" {
		t.Fatalf("logs = %+v, want only first message", logs)
	}
}

func TestServiceStartTaskAutoAssignsAvailableWorker(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-auto",
		Name:            "auto",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatalf("RegisterWorker returned error: %v", err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatalf("WorkerConnected returned error: %v", err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:      "Auto assign",
		ProjectID:  project.ID,
		AgentType:  domain.AgentCodex,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	started, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if started.Status != domain.TaskStarting {
		t.Fatalf("status = %s, want STARTING", started.Status)
	}
	if started.WorkerID != worker.ID {
		t.Fatalf("worker id = %q, want %q", started.WorkerID, worker.ID)
	}
	if request := preparedA2ARequestForTask(t, ctx, service, task.ID); request.Request.Scope.LocalTaskID != task.ID {
		t.Fatalf("A2A request task id = %q, want %q", request.Request.Scope.LocalTaskID, task.ID)
	}
}

func TestServiceCreatesUnassignedTaskWithoutAgent(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}

	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:     "Agentless",
		ProjectID: project.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.Status != domain.TaskCreated {
		t.Fatalf("status = %s, want CREATED", task.Status)
	}
	if task.AgentType != "" {
		t.Fatalf("agent type = %q, want empty", task.AgentType)
	}
}

func TestServiceCreateTaskWithWorkerAssignsImmediately(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-create",
		Name:            "creator",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatalf("RegisterWorker returned error: %v", err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatalf("WorkerConnected returned error: %v", err)
	}

	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:     "Assigned on create",
		ProjectID: project.ID,
		WorkerID:  worker.ID,
		AgentType: domain.AgentCodex,
		AgentConfig: &domain.AgentExecutionConfig{
			WorkMode: domain.AgentWorkModeImplement,
			Codex: domain.CodexExecutionConfig{
				Model:           "gpt-5.4",
				ReasoningEffort: domain.CodexReasoningHigh,
				SandboxMode:     domain.CodexSandboxWorkspaceWrite,
				ApprovalPolicy:  domain.CodexApprovalNever,
			},
		},
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.Status != domain.TaskAssigned || task.WorkerID != worker.ID {
		t.Fatalf("task assignment = status %s worker %q, want ASSIGNED %q", task.Status, task.WorkerID, worker.ID)
	}
	if task.AgentConfig.Codex.Model != "gpt-5.4" || task.AgentConfig.Codex.ReasoningEffort != domain.CodexReasoningHigh {
		t.Fatalf("task agent config = %+v", task.AgentConfig)
	}
	_, err = service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	request := preparedA2ARequestForTask(t, ctx, service, task.ID)
	if request.Request.Agent.Config.Model != "gpt-5.4" || request.Request.Agent.WorkMode != a2aext.WorkModeImplement {
		t.Fatalf("start request agent config = %+v", request.Request.Agent)
	}
	loadedWorker, err := service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatalf("Worker returned error: %v", err)
	}
	if len(loadedWorker.CurrentTaskIDs) != 1 || loadedWorker.CurrentTaskIDs[0] != task.ID {
		t.Fatalf("worker current tasks = %+v, want [%q]", loadedWorker.CurrentTaskIDs, task.ID)
	}
}

func TestServiceCreateTaskWithWorkerRequiresAgent(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-create",
		Name:            "creator",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatalf("RegisterWorker returned error: %v", err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatalf("WorkerConnected returned error: %v", err)
	}

	if _, err := service.CreateTask(ctx, CreateTaskInput{
		Title:     "Missing agent",
		ProjectID: project.ID,
		WorkerID:  worker.ID,
	}); err == nil {
		t.Fatal("CreateTask with worker but no agent should fail")
	}
}

func TestServiceAssignsAgentlessTaskWithAgent(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-agentless",
		Name:            "agentless",
		SupportedAgents: []domain.AgentType{domain.AgentClaude},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatalf("RegisterWorker returned error: %v", err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatalf("WorkerConnected returned error: %v", err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:     "Agentless",
		ProjectID: project.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	assigned, err := service.AssignWorkerWithConfig(ctx, task.ID, worker.ID, &domain.AgentExecutionConfig{
		WorkMode: domain.AgentWorkModePlan,
		Claude: domain.ClaudeExecutionConfig{
			Model:          "sonnet",
			Effort:         domain.ClaudeEffortHigh,
			PermissionMode: domain.ClaudePermissionPlan,
		},
	}, domain.AgentClaude)
	if err != nil {
		t.Fatalf("AssignWorker returned error: %v", err)
	}
	if assigned.Status != domain.TaskAssigned || assigned.AgentType != domain.AgentClaude {
		t.Fatalf("assigned task = status %s agent %q, want ASSIGNED claude", assigned.Status, assigned.AgentType)
	}
	if assigned.AgentConfig.Claude.Model != "sonnet" || assigned.AgentConfig.Claude.PermissionMode != domain.ClaudePermissionPlan {
		t.Fatalf("assigned agent config = %+v", assigned.AgentConfig)
	}
}

func TestServiceRejectsAgentlessTaskStart(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:     "Agentless",
		ProjectID: project.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	if _, err := service.StartTask(ctx, task.ID); err == nil {
		t.Fatal("StartTask should reject agentless task")
	}
}

func TestServiceInterruptsTaskAndReleasesWorkerWhenWorkerConfirms(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)

	interrupting, err := service.InterruptTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("InterruptTask returned error: %v", err)
	}
	if interrupting.Status != domain.TaskInterrupting {
		t.Fatalf("status = %s, want INTERRUPTING", interrupting.Status)
	}
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventResultUpdated, domain.TaskA2ARemoteStatusWorking, nil, map[string]any{"result": "interrupted"})
	interrupted := applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusCanceled, nil, map[string]any{"status": string(a2aext.TerminalCanceled), "result": "interrupted"})
	if interrupted.Status != domain.TaskInterrupted || interrupted.Result != "interrupted" {
		t.Fatalf("interrupted task = %+v", interrupted)
	}
	worker, err := service.Store().Worker(ctx, task.WorkerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(worker.CurrentTaskIDs) != 0 {
		t.Fatalf("worker current tasks = %+v, want released", worker.CurrentTaskIDs)
	}
}

func TestServiceRecordsTaskResultBeforeCompletion(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)

	withResult := applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventResultUpdated, domain.TaskA2ARemoteStatusWorking, nil, map[string]any{"result": "agent result"})
	if withResult.Status != domain.TaskRunning || withResult.Result != "agent result" {
		t.Fatalf("task after result = %+v", withResult)
	}
	completed := applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusCompleted, nil, map[string]any{"status": string(a2aext.TerminalCompleted)})
	if completed.Result != "agent result" {
		t.Fatalf("completion should preserve prior result, got %q", completed.Result)
	}
}

func seedRunningTask(t *testing.T, ctx context.Context, service *Service) *domain.Task {
	t.Helper()
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "file:///tmp/repo", DefaultBranch: "main", WorktreeNamePrefix: "p"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-1", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", BindingMode: domain.WorkerAllProjects})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex, BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	bindTestA2ARound(t, ctx, service, task.ID)
	return applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventWorkspaceReady, domain.TaskA2ARemoteStatusWorking, &a2aext.RuntimeInfo{WorktreePath: "/tmp/worktree"}, map[string]any{})
}

func seedCompletedTaskWithSession(t *testing.T, ctx context.Context, service *Service) *domain.Task {
	t.Helper()
	running := seedRunningTask(t, ctx, service)
	return applyTestA2AEvent(t, ctx, service, running.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusCompleted, &a2aext.RuntimeInfo{AgentSessionID: "session-1", WorktreePath: running.WorktreePath}, map[string]any{"status": string(a2aext.TerminalCompleted), "result": "done"})
}
