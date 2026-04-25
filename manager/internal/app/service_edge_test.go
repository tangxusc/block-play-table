package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceStartTaskErrorsAndRuntimeEnvPayload(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo", SetupCommands: []string{"make setup"}})
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
	if _, _, err := service.StartTask(ctx, task.ID); !errors.Is(err, domain.ErrConflict) {
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
	if _, _, err := service.StartTask(ctx, task.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("StartTask with offline worker err = %v, want conflict", err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAgentRuntimeEnvVars(ctx, []domain.AgentRuntimeEnvVar{
		{Key: "TOKEN", Value: "secret", Enabled: true, Sensitive: true},
		{Key: "DISABLED", Value: "hidden", Enabled: false, Sensitive: false},
	}); err != nil {
		t.Fatal(err)
	}
	started, payload, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != domain.TaskStarting {
		t.Fatalf("status = %s, want STARTING", started.Status)
	}
	if len(payload.AgentRuntimeEnv) != 1 || payload.AgentRuntimeEnv[0].Key != "TOKEN" || payload.AgentRuntimeEnv[0].Value != "secret" {
		t.Fatalf("runtime env = %+v", payload.AgentRuntimeEnv)
	}
	if len(payload.Project.SetupCommands) != 1 || len(payload.Task.PreCommands) != 1 || len(payload.Task.PostCommands) != 1 {
		t.Fatalf("payload commands = %+v %+v", payload.Project, payload.Task)
	}
}

func TestServiceDuplicateRuntimeMessagesReturnExistingTask(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	task := seedRunningTask(t, ctx, service)

	if _, err := service.ApplyWorkerTaskStarted(ctx, "started-dupe", task.ID, "/tmp/one"); err == nil {
		t.Fatal("second started event should be rejected by task state")
	}
	if _, err := service.ApplyWorkerTaskLog(ctx, "log-dupe", task.ID, "stdout", "first"); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.ApplyWorkerTaskLog(ctx, "log-dupe", task.ID, "stdout", "second")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != task.ID {
		t.Fatalf("duplicate log returned task %s, want %s", loaded.ID, task.ID)
	}
	if _, err := service.ApplyWorkerConversation(ctx, "conv-dupe", task.ID, "assistant", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerConversation(ctx, "conv-dupe", task.ID, "assistant", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskCompleted(ctx, "complete-dupe", task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskCompleted(ctx, "complete-dupe", task.ID, "done again"); err != nil {
		t.Fatal(err)
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
	if again.Name != worker.Name {
		t.Fatalf("existing worker name = %q, want %q", again.Name, worker.Name)
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
	if _, _, err := service.StartTask(ctx, "missing-task"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("StartTask missing task err = %v, want ErrNotFound", err)
	}
}

func TestServiceDuplicateProcessedRuntimeMessages(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	task := seedRunningTask(t, ctx, service)
	for _, messageID := range []string{"started-processed", "failed-processed"} {
		if ok, err := service.Store().MarkMessageProcessed(ctx, messageID); err != nil || !ok {
			t.Fatalf("MarkMessageProcessed(%s) = %v, %v", messageID, ok, err)
		}
	}
	if loaded, err := service.ApplyWorkerTaskStarted(ctx, "started-processed", task.ID, "/tmp/ignored"); err != nil || loaded.ID != task.ID {
		t.Fatalf("duplicate started = %+v, %v", loaded, err)
	}
	if loaded, err := service.ApplyWorkerTaskFailed(ctx, "failed-processed", task.ID, "ignored"); err != nil || loaded.ID != task.ID {
		t.Fatalf("duplicate failed = %+v, %v", loaded, err)
	}
}

func TestServiceRejectsRuntimeMessagesForCreatedTask(t *testing.T) {
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
	if _, err := service.ApplyWorkerTaskLog(ctx, "created-log", task.ID, "stdout", "ignored"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ApplyWorkerTaskLog err = %v, want invalid transition", err)
	}
	if _, err := service.ApplyWorkerConversation(ctx, "created-conv", task.ID, "assistant", "ignored"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ApplyWorkerConversation err = %v, want invalid transition", err)
	}
	if _, err := service.ApplyWorkerTaskCompleted(ctx, "created-complete", task.ID, "ignored"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ApplyWorkerTaskCompleted err = %v, want invalid transition", err)
	}
	if _, err := service.ApplyWorkerTaskFailed(ctx, "created-failed", task.ID, "ignored"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ApplyWorkerTaskFailed err = %v, want invalid transition", err)
	}
}
