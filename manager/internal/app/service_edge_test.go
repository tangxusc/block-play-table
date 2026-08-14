package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceStartTaskErrorsAndRuntimeEnvPayload(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:        "T",
		Description:  "D",
		ProjectID:    project.ID,
		AgentType:    domain.AgentCodex,
		PreCommands:  []string{"make pre"},
		PostCommands: []string{"make post"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("StartTask without worker err = %v, want conflict", err)
	}

	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-1", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	worker, err = service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	worker.MarkOffline(time.Date(2026, 4, 25, 10, 1, 0, 0, time.UTC))
	if err := service.Store().SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("StartTask with offline worker err = %v, want conflict", err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if worker, err = service.UpdateWorker(ctx, RegisterWorkerInput{
		ID:                     worker.ID,
		Name:                   "W",
		SupportedAgents:        []domain.AgentType{domain.AgentCodex},
		WorkDir:                "/tmp",
		ReplaceAgentRuntimeEnv: true,
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{
			{
				AgentType: domain.AgentCodex,
				Vars: []domain.AgentRuntimeEnvVar{
					{Key: "TOKEN", Value: "secret", Enabled: true, Sensitive: true},
					{Key: "DISABLED", Value: "hidden", Enabled: false, Sensitive: false},
				},
			},
			{
				AgentType: domain.AgentClaude,
				Vars: []domain.AgentRuntimeEnvVar{
					{Key: "CLAUDE_ONLY", Value: "ignored", Enabled: true, Sensitive: false},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	started, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != domain.TaskStarting {
		t.Fatalf("status = %s, want STARTING", started.Status)
	}
	request := preparedA2ARequestForTask(t, ctx, service, task.ID)
	if len(request.Request.Environment.Variables) != 1 || request.Request.Environment.Variables[0].Key != "TOKEN" || request.Request.Environment.Variables[0].Value != "secret" {
		t.Fatalf("runtime env = %+v", request.Request.Environment.Variables)
	}
	if len(request.Request.Commands.Pre) != 1 || len(request.Request.Commands.Post) != 1 {
		t.Fatalf("request commands = %+v", request.Request.Commands)
	}
}

func TestServiceContinueTaskValidatesBeforeAppendingUserMessage(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedCompletedTaskWithSession(t, ctx, service)
	task.AgentSessionID = ""
	if err := service.Store().SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	if _, err := service.ContinueTask(ctx, ContinueTaskInput{TaskID: task.ID, Message: "follow up"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ContinueTask missing session err = %v, want conflict", err)
	}
	messages, err := service.Store().TaskConversations(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %+v, want none after rejected continuation", messages)
	}
	if _, err := service.ContinueTask(ctx, ContinueTaskInput{TaskID: task.ID, Message: "   "}); err == nil {
		t.Fatal("ContinueTask should reject blank messages")
	}
}

func TestServiceContinueTaskRequiresOriginalWorkerOnlineButAllowsConcurrency(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedCompletedTaskWithSession(t, ctx, service)
	worker, err := service.Store().Worker(ctx, task.WorkerID)
	if err != nil {
		t.Fatal(err)
	}
	worker.MarkOffline(time.Date(2026, 4, 25, 10, 1, 0, 0, time.UTC))
	if err := service.Store().SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ContinueTask(ctx, ContinueTaskInput{TaskID: task.ID, Message: "follow up"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ContinueTask offline worker err = %v, want conflict", err)
	}

	worker.Connect(time.Date(2026, 4, 25, 10, 2, 0, 0, time.UTC))
	if err := worker.AssignTask("other-task", time.Date(2026, 4, 25, 10, 2, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := service.Store().SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ContinueTask(ctx, ContinueTaskInput{TaskID: task.ID, Message: "follow up"}); err != nil {
		t.Fatalf("ContinueTask with concurrent worker task returned error: %v", err)
	}
}

func TestServiceA2AEventsAreIdempotentWithinRound(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	task := seedRunningTask(t, ctx, service)

	round, workspace := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventWorkspaceReady, &a2aext.RuntimeInfo{WorktreePath: "/tmp/one"}, map[string]any{})
	if err := applyTestA2ARawEvent(ctx, service, round, workspace, domain.TaskA2ARemoteStatusWorking); !errors.Is(err, ErrA2AProtocolConflict) {
		t.Fatalf("worktree 发生变化时错误 = %v，期望 A2A 协议冲突", err)
	}
	round, logEvent := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventLogChunk, nil, map[string]any{"stream": string(a2aext.LogStdout), "content": "first"})
	if err := applyTestA2ARawEvent(ctx, service, round, logEvent, domain.TaskA2ARemoteStatusWorking); err != nil {
		t.Fatalf("首次 log.chunk 返回错误: %v", err)
	}
	if err := applyTestA2ARawEvent(ctx, service, round, logEvent, domain.TaskA2ARemoteStatusWorking); err != nil {
		t.Fatalf("重复 log.chunk 返回错误: %v", err)
	}
	loaded := loadTestA2ATask(t, ctx, service, task.ID)
	if loaded.ID != task.ID {
		t.Fatalf("duplicate log returned task %s, want %s", loaded.ID, task.ID)
	}
	round, conversationEvent := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventConversationMessage, nil, map[string]any{"role": "assistant", "content": "first"})
	if err := applyTestA2ARawEvent(ctx, service, round, conversationEvent, domain.TaskA2ARemoteStatusWorking); err != nil {
		t.Fatalf("首次 conversation.message 返回错误: %v", err)
	}
	if err := applyTestA2ARawEvent(ctx, service, round, conversationEvent, domain.TaskA2ARemoteStatusWorking); err != nil {
		t.Fatalf("重复 conversation.message 返回错误: %v", err)
	}
	round, terminalEvent := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, nil, map[string]any{"status": string(a2aext.TerminalCompleted), "result": "done"})
	if err := applyTestA2ARawEvent(ctx, service, round, terminalEvent, domain.TaskA2ARemoteStatusCompleted); err != nil {
		t.Fatalf("首次 execution.terminal 返回错误: %v", err)
	}
	if err := applyTestA2ARawEvent(ctx, service, round, terminalEvent, domain.TaskA2ARemoteStatusCompleted); err != nil {
		t.Fatalf("重复 execution.terminal 返回错误: %v", err)
	}
	logs, err := service.Store().TaskLogs(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	messages, err := service.Store().TaskConversations(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(messages))
	}
}

func TestRegisterWorkerPreservesRuntimeEnvWhenPayloadOmitsEnv(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:                     "worker-preserve-env",
		Name:                   "W",
		SupportedAgents:        []domain.AgentType{domain.AgentCodex},
		WorkDir:                "/tmp",
		ReplaceAgentRuntimeEnv: true,
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{
			AgentType: domain.AgentCodex,
			Vars:      []domain.AgentRuntimeEnvVar{{Key: "TOKEN", Value: "secret", Enabled: true, Sensitive: true}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              worker.ID,
		Name:            "W reconnected",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/other",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime := again.EnabledRuntimeEnv(domain.AgentCodex); len(runtime) != 1 || runtime[0].Value != "secret" {
		t.Fatalf("runtime env should be preserved on register without env: %+v", again.AgentRuntimeEnv)
	}
}

func TestUpdateWorkerPreservesCapabilitiesWhenPayloadOmitsCapabilities(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-preserve-capabilities",
		Name:            "W",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp",
		Capabilities: map[string]string{
			"terminal_enabled": "true",
			"terminal_host":    "127.0.0.1",
			"terminal_port":    "41001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateWorker(ctx, RegisterWorkerInput{
		ID:              worker.ID,
		Name:            "W edited",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/edited",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Capabilities["terminal_enabled"]; got != "true" {
		t.Fatalf("terminal_enabled capability = %q, want true; capabilities=%+v", got, updated.Capabilities)
	}
	if got := updated.Capabilities["terminal_host"]; got != "127.0.0.1" {
		t.Fatalf("terminal_host capability = %q, want 127.0.0.1; capabilities=%+v", got, updated.Capabilities)
	}
	if got := updated.Capabilities["terminal_port"]; got != "41001" {
		t.Fatalf("terminal_port capability = %q, want 41001; capabilities=%+v", got, updated.Capabilities)
	}
}

func TestRegisterWorkerRefreshesCapabilitiesWhenPayloadProvidesCapabilities(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-refresh-capabilities",
		Name:            "W",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp",
		Capabilities: map[string]string{
			"terminal_enabled": "true",
			"terminal_host":    "127.0.0.1",
			"terminal_port":    "41001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              worker.ID,
		Name:            "W reconnected",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/reconnected",
		Capabilities: map[string]string{
			"terminal_enabled": "true",
			"terminal_host":    "127.0.0.1",
			"terminal_port":    "42002",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Capabilities["terminal_port"]; got != "42002" {
		t.Fatalf("terminal_port capability = %q, want refreshed port 42002; capabilities=%+v", got, again.Capabilities)
	}
}

func TestRegisterAndUpdateWorkerRejectDuplicateNames(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-a", Name: "Shared Name", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/a"}); err != nil {
		t.Fatalf("register first worker: %v", err)
	}
	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-b", Name: "Shared Name", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/b"}); err == nil {
		t.Fatal("registering a second worker with the same name should fail")
	}
	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-b", Name: "Other Name", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/b"}); err != nil {
		t.Fatalf("register second worker: %v", err)
	}
	if _, err := service.UpdateWorker(ctx, RegisterWorkerInput{ID: "worker-b", Name: "Shared Name", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/b"}); err == nil {
		t.Fatal("updating a worker to an existing name should fail")
	}
	if _, err := service.UpdateWorker(ctx, RegisterWorkerInput{ID: "worker-a", Name: "Shared Name", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/a"}); err != nil {
		t.Fatalf("updating a worker without changing its name should succeed: %v", err)
	}
}

func TestServiceValidationNotFoundAndExistingWorkerBranches(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())

	if _, err := service.CreateProject(ctx, CreateProjectInput{Name: "", GitURL: "git://repo"}); err == nil {
		t.Fatal("CreateProject with blank name should fail")
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: "missing", AgentType: domain.AgentCodex}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("CreateTask missing project err = %v, want ErrNotFound", err)
	}
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentType("bad")}); err == nil {
		t.Fatal("CreateTask with unsupported agent should fail")
	}
	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "bad-worker", Name: "W", WorkDir: "/tmp"}); err == nil {
		t.Fatal("RegisterWorker without supported agents should fail")
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-existing", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: worker.ID, Name: "Ignored", SupportedAgents: []domain.AgentType{domain.AgentClaude}, WorkDir: "/tmp/other"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "Ignored" || len(again.SupportedAgents) != 1 || again.SupportedAgents[0] != domain.AgentClaude {
		t.Fatalf("existing worker should refresh registration payload, got %+v", again)
	}
	if _, err := service.WorkerConnected(ctx, "missing-worker"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("WorkerConnected missing err = %v, want ErrNotFound", err)
	}
	if _, err := service.WorkerHeartbeat(ctx, "missing-worker"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("WorkerHeartbeat missing err = %v, want ErrNotFound", err)
	}
	if _, err := service.ArchiveProject(ctx, "missing-project"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ArchiveProject missing err = %v, want ErrNotFound", err)
	}
	if _, err := service.AssignWorker(ctx, "missing-task", worker.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("AssignWorker missing task err = %v, want ErrNotFound", err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, "missing-worker"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("AssignWorker missing worker err = %v, want ErrNotFound", err)
	}
	if _, err := service.StartTask(ctx, "missing-task"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("StartTask missing task err = %v, want ErrNotFound", err)
	}
}

func TestServiceRejectsA2AUpdateForUnknownRound(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	err = service.ApplyA2ARemoteUpdate(ctx, task.ID, A2ARemoteUpdate{})
	if !errors.Is(err, ErrA2AProjection) || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("未知 round 的 A2A 更新错误 = %v", err)
	}
}
