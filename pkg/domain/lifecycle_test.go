package domain

import (
	"errors"
	"testing"
	"time"
)

func TestTaskInterruptArchiveWaitingAndConversation(t *testing.T) {
	now := time.Now().UTC()
	task, err := NewTask(NewTaskInput{ID: "task-2", Title: "T", ProjectID: "project-1", AgentType: AgentClaude, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = task.PullEvents()
	if err := task.AssignWorkerWithAgentConfig("worker-1", AgentClaude, &AgentExecutionConfig{
		WorkMode: AgentWorkModeImplement,
		Claude: ClaudeExecutionConfig{
			Model:          "sonnet",
			Effort:         ClaudeEffortHigh,
			PermissionMode: ClaudePermissionDefault,
		},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := task.Start(now); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkRunning("/tmp/w", now); err != nil {
		t.Fatal(err)
	}
	if err := task.AppendConversation("assistant", "hello", now); err != nil {
		t.Fatal(err)
	}
	if err := task.WaitForInput(now); err != nil {
		t.Fatal(err)
	}
	if err := task.Resume(now); err != nil {
		t.Fatal(err)
	}
	if err := task.RequestInterrupt(now); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkInterrupted(now); err != nil {
		t.Fatal(err)
	}
	if err := task.Archive(now); err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskArchived {
		t.Fatalf("status = %s, want archived", task.Status)
	}
	if len(task.PullEvents()) == 0 {
		t.Fatal("expected lifecycle events")
	}
}

func TestTaskConstructorValidation(t *testing.T) {
	tests := []NewTaskInput{
		{ID: "", Title: "T", ProjectID: "p", AgentType: AgentCodex},
		{ID: "t", Title: "", ProjectID: "p", AgentType: AgentCodex},
		{ID: "t", Title: "T", ProjectID: "", AgentType: AgentCodex},
		{ID: "t", Title: "T", ProjectID: "p", AgentType: AgentType("bad")},
	}
	for _, input := range tests {
		if _, err := NewTask(input); err == nil {
			t.Fatalf("NewTask(%+v) should fail", input)
		}
	}
}

func TestWorkerLifecycleControlsAvailability(t *testing.T) {
	now := time.Now().UTC()
	worker, err := NewWorker(NewWorkerInput{ID: "worker-2", Name: "W", SupportedAgents: []AgentType{AgentCodex, AgentClaude}, WorkDir: "/tmp", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	worker.Connect(now)
	worker.Heartbeat(now)
	worker.BindProjects([]string{"project-1"}, now)
	if !worker.CanAcceptTask(AgentClaude, "project-1") {
		t.Fatal("worker should accept bound claude task")
	}
	worker.Disable(now)
	if worker.CanAcceptTask(AgentClaude, "project-1") {
		t.Fatal("disabled worker should not accept task")
	}
	worker.Enable(now)
	worker.Connect(now)
	worker.MarkOffline(now)
	if worker.Status != WorkerOffline {
		t.Fatalf("status = %s, want offline", worker.Status)
	}
	if len(worker.PullEvents()) == 0 {
		t.Fatal("expected worker events")
	}
}

func TestWorkerDeleteProducesDomainEvent(t *testing.T) {
	now := time.Now().UTC()
	worker, err := NewWorker(NewWorkerInput{ID: "worker-delete", Name: "Delete Me", SupportedAgents: []AgentType{AgentCodex}, WorkDir: "/tmp", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = worker.PullEvents()

	worker.Delete(now.Add(time.Second))

	events := worker.PullEvents()
	if len(events) != 1 {
		t.Fatalf("worker delete events = %d, want 1", len(events))
	}
	event := events[0]
	if event.EventType != "WorkerDeleted" || event.AggregateType != "Worker" || event.AggregateID != worker.ID {
		t.Fatalf("delete event = %+v", event)
	}
	if event.AggregateVersion != worker.Version {
		t.Fatalf("event version = %d, want worker version %d", event.AggregateVersion, worker.Version)
	}
}

func TestProjectUpdateArchiveAndValidation(t *testing.T) {
	now := time.Now().UTC()
	project, err := NewProject(NewProjectInput{ID: "project-1", Name: "P", GitURL: "git://repo", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if project.DefaultBranch != "main" || project.WorktreeNamePrefix != "project-1" {
		t.Fatalf("defaults not applied: %+v", project)
	}
	if err := project.Update("P2", "git://repo2", "develop", "prefix", now); err != nil {
		t.Fatal(err)
	}
	project.Archive(now)
	if !project.Archived {
		t.Fatal("project should be archived")
	}
	if _, err := NewProject(NewProjectInput{ID: "", Name: "P", GitURL: "git://repo"}); err == nil {
		t.Fatal("blank project id should fail")
	}
}

func TestSettingsUpdatesHeartbeatAndEmitEvents(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	settings := NewSettings(now)
	if settings.ID != "settings" || settings.WorkerHeartbeat != "90s" || settings.SecurityPolicy != "TRUSTED" {
		t.Fatalf("default settings = %+v", settings)
	}
	settings.UpdateWorkerHeartbeatTimeout("", now.Add(3*time.Minute))
	if settings.WorkerHeartbeat != "90s" {
		t.Fatalf("empty heartbeat should reset default, got %q", settings.WorkerHeartbeat)
	}
	settings.UpdateWorkerHeartbeatTimeout("45s", now.Add(4*time.Minute))
	if settings.WorkerHeartbeat != "45s" {
		t.Fatalf("heartbeat = %q, want 45s", settings.WorkerHeartbeat)
	}
	if events := settings.PullEvents(); len(events) != 2 {
		t.Fatalf("settings events = %d, want 2", len(events))
	}
}

func TestTaskUpdateRetryResultFailureAndRestoreEvents(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	task, err := NewTask(NewTaskInput{ID: "task-update", Title: "T", ProjectID: "project-1", AgentType: AgentCodex, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Update(NewTaskInput{Title: "T2", ProjectID: "project-2", AgentType: AgentClaude, PreCommands: []string{"pre"}, PostCommands: []string{"post"}, Now: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if task.Title != "T2" || task.BaseBranch != "main" || len(task.PreCommands) != 1 {
		t.Fatalf("updated task = %+v", task)
	}
	for _, input := range []NewTaskInput{
		{Title: "", ProjectID: "project-1", AgentType: AgentCodex},
		{Title: "T", ProjectID: "", AgentType: AgentCodex},
		{Title: "T", ProjectID: "project-1", AgentType: AgentType("bad")},
	} {
		if err := task.Update(input); err == nil {
			t.Fatalf("Update(%+v) should fail", input)
		}
	}
	if err := task.AssignWorker("worker-1", now); err != nil {
		t.Fatal(err)
	}
	if err := task.Start(now); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkRunning("/tmp/worktree", now); err != nil {
		t.Fatal(err)
	}
	if err := task.RecordResult("partial", now); err != nil {
		t.Fatalf("RecordResult returned error: %v", err)
	}
	if task.Result != "partial" {
		t.Fatalf("result = %q, want partial", task.Result)
	}
	if err := task.Fail("boom", now); err != nil {
		t.Fatalf("Fail returned error: %v", err)
	}
	if task.Result != "boom" || task.Status != TaskFailed {
		t.Fatalf("failed task = %+v", task)
	}
	if err := task.Retry(now); err != nil {
		t.Fatalf("Retry returned error: %v", err)
	}
	if task.Status != TaskCreated || task.WorkerID != "" || !task.AgentConfig.Empty() || task.WorktreePath != "" || task.Result != "" {
		t.Fatalf("retried task = %+v", task)
	}
	if err := task.Retry(now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Retry from CREATED err = %v, want invalid transition", err)
	}
	task.RestoreEvents([]DomainEvent{{EventID: "evt-restore"}})
	if events := task.PullEvents(); events[len(events)-1].EventID != "evt-restore" {
		t.Fatalf("restored events = %+v", events)
	}
}

func TestTaskContinueFromCompletedRequiresPersistedAgentSession(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	task, err := NewTask(NewTaskInput{ID: "task-continue", Title: "T", ProjectID: "project-1", AgentType: AgentCodex, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := task.AssignWorker("worker-1", now); err != nil {
		t.Fatal(err)
	}
	if err := task.Start(now); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkRunning("/tmp/worktree", now); err != nil {
		t.Fatal(err)
	}
	task.AgentSessionID = "session-1"
	if err := task.Complete("first result", now); err != nil {
		t.Fatal(err)
	}

	if err := task.Continue(now.Add(time.Minute)); err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}
	if task.Status != TaskStarting || task.Result != "" {
		t.Fatalf("continued task = status %s result %q, want STARTING and cleared result", task.Status, task.Result)
	}
	events := task.PullEvents()
	last := events[len(events)-1]
	if last.EventType != "TaskContinueRequested" {
		t.Fatalf("last event = %s, want TaskContinueRequested", last.EventType)
	}

	for _, tt := range []struct {
		name string
		task Task
	}{
		{name: "not completed", task: Task{ID: "task-a", Status: TaskRunning, WorkerID: "worker-1", WorktreePath: "/tmp/w", AgentSessionID: "session-1"}},
		{name: "missing worker", task: Task{ID: "task-b", Status: TaskCompleted, WorktreePath: "/tmp/w", AgentSessionID: "session-1"}},
		{name: "missing worktree", task: Task{ID: "task-c", Status: TaskCompleted, WorkerID: "worker-1", AgentSessionID: "session-1"}},
		{name: "missing session", task: Task{ID: "task-d", Status: TaskCompleted, WorkerID: "worker-1", WorktreePath: "/tmp/w"}},
	} {
		if err := tt.task.Continue(now); !errors.Is(err, ErrInvalidTransition) && !errors.Is(err, ErrConflict) {
			t.Fatalf("%s Continue err = %v, want invalid transition or conflict", tt.name, err)
		}
	}
}

func TestWorkerUpdateValidationRestoreEventsAndDisabledOffline(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	worker, err := NewWorker(NewWorkerInput{ID: "worker-update", Name: "W", SupportedAgents: []AgentType{AgentCodex}, WorkDir: "/tmp", Capabilities: map[string]string{"os": "darwin"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if worker.Capabilities["os"] != "darwin" {
		t.Fatalf("capabilities = %+v", worker.Capabilities)
	}
	if err := worker.Update(NewWorkerInput{Name: "W2", SupportedAgents: []AgentType{AgentClaude}, WorkDir: "/tmp/w2", ProjectBindingMode: WorkerSpecificProjects, BoundProjectIDs: []string{"project-1"}, Capabilities: map[string]string{"cpu": "arm64"}, Now: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if worker.Name != "W2" || worker.Capabilities["cpu"] != "arm64" || len(worker.BoundProjectIDs) != 1 {
		t.Fatalf("updated worker = %+v", worker)
	}
	for _, input := range []NewWorkerInput{
		{Name: "", SupportedAgents: []AgentType{AgentCodex}, WorkDir: "/tmp"},
		{Name: "W", SupportedAgents: []AgentType{AgentCodex}, WorkDir: ""},
		{Name: "W", WorkDir: "/tmp"},
		{Name: "W", SupportedAgents: []AgentType{AgentType("bad")}, WorkDir: "/tmp"},
	} {
		if err := worker.Update(input); err == nil {
			t.Fatalf("Update(%+v) should fail", input)
		}
	}
	worker.Disable(now)
	worker.MarkOffline(now)
	if worker.Status != WorkerDisabled {
		t.Fatalf("disabled worker should stay disabled when marked offline, got %s", worker.Status)
	}
	if err := worker.AssignTask("task-1", now); err != nil {
		t.Fatal(err)
	}
	if err := worker.AssignTask("task-2", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("assign occupied err = %v, want conflict", err)
	}
	worker.RestoreEvents([]DomainEvent{{EventID: "evt-worker"}})
	if events := worker.PullEvents(); events[len(events)-1].EventID != "evt-worker" {
		t.Fatalf("restored worker events = %+v", events)
	}
}

func TestProjectRestoreEventsAndValidationBranches(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	for _, input := range []NewProjectInput{
		{ID: "project", Name: "", GitURL: "git://repo"},
		{ID: "project", Name: "P", GitURL: ""},
	} {
		if _, err := NewProject(input); err == nil {
			t.Fatalf("NewProject(%+v) should fail", input)
		}
	}
	project, err := NewProject(NewProjectInput{ID: "project-restore", Name: "P", GitURL: "git://repo", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := project.Update("", "git://repo", "", "", now); err == nil {
		t.Fatal("blank project name update should fail")
	}
	if err := project.Update("P", "", "", "", now); err == nil {
		t.Fatal("blank git url update should fail")
	}
	project.RestoreEvents([]DomainEvent{{EventID: "evt-project"}})
	if events := project.PullEvents(); events[len(events)-1].EventID != "evt-project" {
		t.Fatalf("restored project events = %+v", events)
	}
}
