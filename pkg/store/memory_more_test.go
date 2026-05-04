package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestMemoryStoreListsFiltersSettingsAndMessageDedup(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	worker, err := domain.NewWorker(domain.NewWorkerInput{
		ID:              "worker-1",
		Name:            "W",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp",
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{
			AgentType: domain.AgentCodex,
			Vars:      []domain.AgentRuntimeEnvVar{{Key: "A", Value: "B", Enabled: true}},
		}},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := domain.NewProject(domain.NewProjectInput{ID: "project-1", Name: "P", GitURL: "git://repo", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if workers, _ := s.Workers(ctx); len(workers) != 1 {
		t.Fatalf("workers count = %d", len(workers))
	}
	loadedWorker, err := s.Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if runtime := loadedWorker.EnabledRuntimeEnv(domain.AgentCodex); len(runtime) != 1 || runtime[0].Value != "B" {
		t.Fatalf("worker env = %+v", loadedWorker.AgentRuntimeEnv)
	}
	if projects, _ := s.Projects(ctx); len(projects) != 1 {
		t.Fatalf("projects count = %d", len(projects))
	}
	settings := domain.NewSettings(now)
	settings.UpdateWorkerHeartbeatTimeout("45s", now)
	if err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkerHeartbeat != "45s" {
		t.Fatalf("settings = %+v", loaded)
	}
	first, err := s.MarkMessageProcessed(ctx, "msg-1")
	if err != nil || !first {
		t.Fatalf("first mark = %v, %v", first, err)
	}
	second, err := s.MarkMessageProcessed(ctx, "msg-1")
	if err != nil || second {
		t.Fatalf("second mark = %v, %v", second, err)
	}
	empty, err := s.MarkMessageProcessed(ctx, "")
	if err != nil || !empty {
		t.Fatalf("empty mark = %v, %v", empty, err)
	}
}

func TestMemoryStorePersistsOutboxMessages(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	task, err := domain.NewTask(domain.NewTaskInput{ID: "task-1", Title: "T", ProjectID: "project-1", AgentType: domain.AgentCodex, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	events := task.PullEvents()
	if err := s.AppendEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	pending, err := s.OutboxMessages(ctx, false)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending outbox = %d, %v", len(pending), err)
	}
	if pending[0].Event.EventType != "TaskCreated" || pending[0].Status != domain.OutboxPending {
		t.Fatalf("pending message = %+v", pending[0])
	}
	if err := s.MarkOutboxPublished(ctx, []string{pending[0].ID}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if pending, _ := s.OutboxMessages(ctx, false); len(pending) != 0 {
		t.Fatalf("pending outbox after publish = %d", len(pending))
	}
	all, err := s.OutboxMessages(ctx, true)
	if err != nil || len(all) != 1 {
		t.Fatalf("all outbox = %d, %v", len(all), err)
	}
	if all[0].Status != domain.OutboxPublished || all[0].PublishedAt == nil {
		t.Fatalf("published message = %+v", all[0])
	}
}

func TestMemoryStoreTaskInteractionCRUD(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	interaction := domain.TaskInteraction{
		ID:             "interaction-1",
		TaskID:         "task-1",
		Kind:           domain.TaskInteractionCommandApproval,
		Status:         domain.TaskInteractionPending,
		Title:          "Approve command",
		Body:           "Run make test",
		RawPayload:     `{"command":"make test"}`,
		AgentSessionID: "session-1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.SaveTaskInteraction(ctx, interaction); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.TaskInteraction(ctx, interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RawPayload != interaction.RawPayload || loaded.Status != domain.TaskInteractionPending {
		t.Fatalf("loaded interaction = %+v", loaded)
	}
	all, err := s.TaskInteractions(ctx, "task-1", "")
	if err != nil || len(all) != 1 {
		t.Fatalf("all interactions = %+v, %v", all, err)
	}
	pending, err := s.TaskInteractions(ctx, "task-1", domain.TaskInteractionPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending interactions = %+v, %v", pending, err)
	}
	if err := s.CancelPendingTaskInteractions(ctx, "task-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	canceled, err := s.TaskInteraction(ctx, interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.TaskInteractionCanceled || canceled.ResponseDecision != domain.TaskInteractionCancel {
		t.Fatalf("canceled interaction = %+v", canceled)
	}
}

func TestMemoryStoreDeleteTaskRemovesTaskAndDetailRows(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	task, err := domain.NewTask(domain.NewTaskInput{ID: "task-delete", Title: "Delete Me", ProjectID: "project-1", AgentType: domain.AgentCodex, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTaskLog(ctx, domain.TaskLog{ID: "log-delete", TaskID: task.ID, Stream: "stdout", Content: "hello", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendConversation(ctx, domain.ConversationMessage{ID: "msg-delete", TaskID: task.ID, Role: "assistant", Content: "done", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTaskInteraction(ctx, domain.TaskInteraction{
		ID:        "interaction-delete",
		TaskID:    task.ID,
		Kind:      domain.TaskInteractionCommandApproval,
		Status:    domain.TaskInteractionPending,
		Title:     "Approve",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask returned error: %v", err)
	}
	if _, err := s.Task(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted Task err = %v, want not found", err)
	}
	if logs, err := s.TaskLogs(ctx, task.ID); err != nil || len(logs) != 0 {
		t.Fatalf("TaskLogs after delete = %+v, %v", logs, err)
	}
	if messages, err := s.TaskConversations(ctx, task.ID); err != nil || len(messages) != 0 {
		t.Fatalf("TaskConversations after delete = %+v, %v", messages, err)
	}
	if interactions, err := s.TaskInteractions(ctx, task.ID, ""); err != nil || len(interactions) != 0 {
		t.Fatalf("TaskInteractions after delete = %+v, %v", interactions, err)
	}
	if _, err := s.TaskInteraction(ctx, "interaction-delete"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted TaskInteraction err = %v, want not found", err)
	}
	if err := s.DeleteTask(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteTask missing err = %v, want not found", err)
	}
}

func TestMemoryStoreNotFoundErrors(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	for name, fn := range map[string]func() error{
		"task":    func() error { _, err := s.Task(ctx, "missing"); return err },
		"worker":  func() error { _, err := s.Worker(ctx, "missing"); return err },
		"project": func() error { _, err := s.Project(ctx, "missing"); return err },
	} {
		if err := fn(); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestMemoryStoreListsDeletesAndFiltersEvents(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping returned error: %v", err)
	}
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	taskA, err := domain.NewTask(domain.NewTaskInput{ID: "task-a", Title: "A", ProjectID: "project-1", AgentType: domain.AgentCodex, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	taskB, err := domain.NewTask(domain.NewTaskInput{ID: "task-b", Title: "B", ProjectID: "project-1", AgentType: domain.AgentClaude, Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTask(ctx, taskB); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTask(ctx, taskA); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != "task-a" {
		t.Fatalf("tasks order = %+v", tasks)
	}
	worker, err := domain.NewWorker(domain.NewWorkerInput{ID: "worker-delete", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", Capabilities: map[string]string{"os": "linux"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWorker(ctx, worker.ID); err != nil {
		t.Fatalf("DeleteWorker returned error: %v", err)
	}
	if err := s.DeleteWorker(ctx, worker.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteWorker missing err = %v, want not found", err)
	}
	if err := s.AppendEvents(ctx, append(taskA.PullEvents(), domain.DomainEvent{EventID: "evt-worker", EventType: "WorkerConnected", AggregateType: "Worker", AggregateID: "worker-1", OccurredAt: now})); err != nil {
		t.Fatal(err)
	}
	events, err := s.DomainEvents(ctx, domain.EventFilter{AggregateType: "Worker", EventType: "WorkerConnected"})
	if err != nil || len(events) != 1 || events[0].AggregateID != "worker-1" {
		t.Fatalf("filtered events = %+v, %v", events, err)
	}
	events, err = s.DomainEvents(ctx, domain.EventFilter{Search: "workerconnected"})
	if err != nil || len(events) != 1 || events[0].EventType != "WorkerConnected" {
		t.Fatalf("searched events = %+v, %v", events, err)
	}
	if err := s.MarkOutboxPublished(ctx, nil, now); err != nil {
		t.Fatalf("MarkOutboxPublished(nil) returned error: %v", err)
	}
}
