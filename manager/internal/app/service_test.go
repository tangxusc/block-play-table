package app

import (
	"context"
	"testing"
	"time"

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
		Title:        "Implement",
		Description:  "Do it",
		ProjectID:    project.ID,
		AgentType:    domain.AgentCodex,
		BaseBranch:   "main",
		TargetBranch: "task/implement",
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatalf("AssignWorker returned error: %v", err)
	}
	started, command, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if started.Status != domain.TaskStarting {
		t.Fatalf("status = %s, want %s", started.Status, domain.TaskStarting)
	}
	if command.Task.ID != task.ID || command.Project.ID != project.ID {
		t.Fatalf("start command should include task and project")
	}
	if len(command.AgentRuntimeEnv) != 0 {
		t.Fatalf("default settings should not send env vars")
	}
	completed, err := service.ApplyWorkerTaskCompleted(ctx, "msg-1", task.ID, "done")
	if err != nil {
		t.Fatalf("ApplyWorkerTaskCompleted returned error: %v", err)
	}
	if completed.Status != domain.TaskCompleted {
		t.Fatalf("status = %s, want completed", completed.Status)
	}
}

func TestServiceDeduplicatesWorkerMessages(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)

	if _, err := service.ApplyWorkerTaskLog(ctx, "msg-1", task.ID, "stdout", "first"); err != nil {
		t.Fatalf("first ApplyWorkerTaskLog returned error: %v", err)
	}
	if _, err := service.ApplyWorkerTaskLog(ctx, "msg-1", task.ID, "stdout", "duplicate"); err != nil {
		t.Fatalf("duplicate ApplyWorkerTaskLog returned error: %v", err)
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

	started, command, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if started.Status != domain.TaskStarting {
		t.Fatalf("status = %s, want STARTING", started.Status)
	}
	if started.WorkerID != worker.ID {
		t.Fatalf("worker id = %q, want %q", started.WorkerID, worker.ID)
	}
	if command.Task.ID != task.ID {
		t.Fatalf("start command task id = %q, want %q", command.Task.ID, task.ID)
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
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex, BaseBranch: "main", TargetBranch: "task/t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	running, err := service.ApplyWorkerTaskStarted(ctx, "started-1", task.ID, "/tmp/worktree")
	if err != nil {
		t.Fatal(err)
	}
	return running
}
