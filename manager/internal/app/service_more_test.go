package app

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceSettingsConversationFailureHeartbeatAndArchive(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)
	if _, err := service.ApplyWorkerConversation(ctx, "conv-1", task.ID, "assistant", "hello"); err != nil {
		t.Fatalf("ApplyWorkerConversation returned error: %v", err)
	}
	if messages, _ := service.Store().TaskConversations(ctx, task.ID); len(messages) != 1 {
		t.Fatalf("conversation count = %d", len(messages))
	}
	if _, err := service.ApplyWorkerTaskFailed(ctx, "failed-1", task.ID, "boom"); err != nil {
		t.Fatalf("ApplyWorkerTaskFailed returned error: %v", err)
	}
	loaded, _ := service.Task(ctx, task.ID)
	if loaded.Status != domain.TaskFailed {
		t.Fatalf("status = %s, want failed", loaded.Status)
	}
	worker, err := service.Store().Worker(ctx, "worker-1")
	if err != nil {
		t.Fatalf("Worker returned error: %v", err)
	}
	if worker.CurrentTaskID != "" {
		t.Fatalf("worker current task = %q, want released after failure", worker.CurrentTaskID)
	}
	if _, err := service.WorkerHeartbeat(ctx, "worker-1"); err != nil {
		t.Fatalf("WorkerHeartbeat returned error: %v", err)
	}
	settings, err := service.UpdateAgentRuntimeEnvVars(ctx, []domain.AgentRuntimeEnvVar{{Key: "OPENAI_API_KEY", Value: "secret", Enabled: true, Sensitive: true}})
	if err != nil {
		t.Fatalf("UpdateAgentRuntimeEnvVars returned error: %v", err)
	}
	if settings.EnabledRuntimeEnv()["OPENAI_API_KEY"] != "secret" {
		t.Fatalf("settings did not keep runtime value")
	}
	project, _ := service.CreateProject(ctx, CreateProjectInput{Name: "Archive", GitURL: "git://archive"})
	if archived, err := service.ArchiveProject(ctx, project.ID); err != nil || !archived.Archived {
		t.Fatalf("ArchiveProject = %+v, %v", archived, err)
	}
}

func TestServiceRejectsUnavailableWorker(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-1", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentClaude}, WorkDir: "/tmp", BindingMode: domain.WorkerAllProjects})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.WorkerConnected(ctx, worker.ID)
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err == nil {
		t.Fatal("AssignWorker should reject unsupported agent")
	}
}

func TestServiceListHelpersAndSettings(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-list", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex}); err != nil {
		t.Fatal(err)
	}
	if projects, err := service.Projects(ctx); err != nil || len(projects) != 1 {
		t.Fatalf("Projects = %d, %v", len(projects), err)
	}
	if workers, err := service.Workers(ctx); err != nil || len(workers) != 1 {
		t.Fatalf("Workers = %d, %v", len(workers), err)
	}
	if tasks, err := service.Tasks(ctx); err != nil || len(tasks) != 1 {
		t.Fatalf("Tasks = %d, %v", len(tasks), err)
	}
	if settings, err := service.Settings(ctx); err != nil || settings.ID == "" {
		t.Fatalf("Settings = %+v, %v", settings, err)
	}
}
