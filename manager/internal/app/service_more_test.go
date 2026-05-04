package app

import (
	"context"
	"errors"
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
	if len(worker.CurrentTaskIDs) != 0 {
		t.Fatalf("worker current tasks = %+v, want released after failure", worker.CurrentTaskIDs)
	}
	if _, err := service.WorkerHeartbeat(ctx, "worker-1"); err != nil {
		t.Fatalf("WorkerHeartbeat returned error: %v", err)
	}
	project, _ := service.CreateProject(ctx, CreateProjectInput{Name: "Archive", GitURL: "git://archive"})
	if archived, err := service.ArchiveProject(ctx, project.ID); err != nil || !archived.Archived {
		t.Fatalf("ArchiveProject = %+v, %v", archived, err)
	}
}

func TestServiceTaskInteractionLifecycleAndDedup(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)

	waiting, err := service.ApplyWorkerTaskInteractionRequest(ctx, "interaction-msg-1", TaskInteractionRequestInput{
		InteractionID:  "interaction-1",
		TaskID:         task.ID,
		Kind:           domain.TaskInteractionCommandApproval,
		Title:          "Command approval",
		Body:           "Run tests",
		RawPayload:     `{"command":"make test"}`,
		AgentSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("ApplyWorkerTaskInteractionRequest returned error: %v", err)
	}
	if waiting.Status != domain.TaskWaitingInput || waiting.AgentSessionID != "session-1" {
		t.Fatalf("task after request = %+v", waiting)
	}
	if _, err := service.ApplyWorkerTaskInteractionRequest(ctx, "interaction-msg-dup", TaskInteractionRequestInput{
		InteractionID: "interaction-1",
		TaskID:        task.ID,
		Kind:          domain.TaskInteractionCommandApproval,
		Title:         "Duplicate",
	}); err != nil {
		t.Fatalf("duplicate interaction request returned error: %v", err)
	}
	interactions, err := service.Store().TaskInteractions(ctx, task.ID, domain.TaskInteractionPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 1 || interactions[0].Title != "Command approval" {
		t.Fatalf("interactions = %+v", interactions)
	}

	_, _, payload, err := service.PrepareTaskInteractionResponse(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-1",
		Decision:      domain.TaskInteractionApprove,
	})
	if err != nil {
		t.Fatalf("PrepareTaskInteractionResponse returned error: %v", err)
	}
	if payload.TaskID != task.ID || payload.Decision != domain.TaskInteractionApprove {
		t.Fatalf("response payload = %+v", payload)
	}
	answered, err := service.MarkTaskInteractionAnswered(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-1",
		Decision:      domain.TaskInteractionApprove,
	})
	if err != nil {
		t.Fatalf("MarkTaskInteractionAnswered returned error: %v", err)
	}
	if answered.Status != domain.TaskInteractionAnswered || answered.ResponseDecision != domain.TaskInteractionApprove {
		t.Fatalf("answered interaction = %+v", answered)
	}
	resumed, err := service.ApplyWorkerTaskInteractionResolved(ctx, "interaction-resolved-1", TaskInteractionResolvedInput{
		InteractionID: "interaction-1",
		TaskID:        task.ID,
	})
	if err != nil {
		t.Fatalf("ApplyWorkerTaskInteractionResolved returned error: %v", err)
	}
	if resumed.Status != domain.TaskRunning {
		t.Fatalf("task after resolved = %+v", resumed)
	}
}

func TestServiceTaskInteractionResponseRequiresOnlineWorkerAndTerminalCancelsPending(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)
	if _, err := service.ApplyWorkerTaskInteractionRequest(ctx, "interaction-msg-offline", TaskInteractionRequestInput{
		InteractionID: "interaction-offline",
		TaskID:        task.ID,
		Kind:          domain.TaskInteractionUserInput,
		Title:         "Question",
		Body:          "Need input",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerDisconnected(ctx, task.WorkerID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := service.PrepareTaskInteractionResponse(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-offline",
		Message:       "answer",
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("offline response err = %v, want conflict", err)
	}

	if _, err := service.WorkerConnected(ctx, task.WorkerID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskCompleted(ctx, "completed-with-pending", task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	canceled, err := service.Store().TaskInteraction(ctx, "interaction-offline")
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.TaskInteractionCanceled || canceled.ResponseDecision != domain.TaskInteractionCancel {
		t.Fatalf("terminal task should cancel pending interaction, got %+v", canceled)
	}
}

func TestServiceTaskInteractionResolvedWithResponseBeatsFastCompletion(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)
	if _, err := service.ApplyWorkerTaskInteractionRequest(ctx, "interaction-msg-race", TaskInteractionRequestInput{
		InteractionID: "interaction-race",
		TaskID:        task.ID,
		Kind:          domain.TaskInteractionCommandApproval,
		Title:         "Command approval",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskInteractionResolved(ctx, "interaction-resolved-race", TaskInteractionResolvedInput{
		InteractionID: "interaction-race",
		TaskID:        task.ID,
		Responded:     true,
		Decision:      domain.TaskInteractionApprove,
	}); err != nil {
		t.Fatalf("ApplyWorkerTaskInteractionResolved returned error: %v", err)
	}
	if _, err := service.ApplyWorkerTaskCompleted(ctx, "interaction-completed-race", task.ID, "done"); err != nil {
		t.Fatalf("ApplyWorkerTaskCompleted returned error: %v", err)
	}
	answered, err := service.MarkTaskInteractionAnswered(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-race",
		Decision:      domain.TaskInteractionApprove,
	})
	if err != nil {
		t.Fatalf("MarkTaskInteractionAnswered should be idempotent after resolved response: %v", err)
	}
	if answered.Status != domain.TaskInteractionAnswered || answered.ResponseDecision != domain.TaskInteractionApprove {
		t.Fatalf("interaction after fast completion = %+v", answered)
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

func TestServicePublishesDomainEventsAndMarksOutbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{EventType: "ProjectCreated"})
	defer unsubscribe()

	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.EventType != "ProjectCreated" || event.AggregateID != project.ID {
			t.Fatalf("published event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for domain event")
	}

	stored, err := service.DomainEvents(ctx, domain.EventFilter{AggregateID: project.ID})
	if err != nil || len(stored) != 1 {
		t.Fatalf("DomainEvents = %d, %v", len(stored), err)
	}
	outbox, err := service.OutboxMessages(ctx, true)
	if err != nil || len(outbox) != 1 {
		t.Fatalf("OutboxMessages = %d, %v", len(outbox), err)
	}
	if outbox[0].Status != domain.OutboxPublished || outbox[0].PublishedAt == nil {
		t.Fatalf("outbox message was not marked published: %+v", outbox[0])
	}
}

func TestServiceDeleteWorkerPublishesDomainEventAndMarksOutbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-delete", Name: "Delete Me", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{AggregateType: "Worker", EventType: "WorkerDeleted"})
	defer unsubscribe()

	if err := service.DeleteWorker(ctx, worker.ID); err != nil {
		t.Fatalf("DeleteWorker returned error: %v", err)
	}

	select {
	case event := <-events:
		if event.EventType != "WorkerDeleted" || event.AggregateID != worker.ID {
			t.Fatalf("published delete event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker delete event")
	}
	stored, err := service.DomainEvents(ctx, domain.EventFilter{AggregateID: worker.ID, EventType: "WorkerDeleted"})
	if err != nil || len(stored) != 1 {
		t.Fatalf("DomainEvents = %d, %v", len(stored), err)
	}
	outbox, err := service.OutboxMessages(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range outbox {
		if message.Event.EventType == "WorkerDeleted" && message.Event.AggregateID == worker.ID {
			found = message.Status == domain.OutboxPublished && message.PublishedAt != nil
		}
	}
	if !found {
		t.Fatalf("worker delete outbox was not published: %+v", outbox)
	}
}

func TestServiceDeleteTaskRequiresArchivedAndPublishesDomainEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "Delete Me", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteTask(ctx, task.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("DeleteTask active err = %v, want conflict", err)
	}
	if _, err := service.ArchiveTask(ctx, task.ID); err != nil {
		t.Fatalf("ArchiveTask returned error: %v", err)
	}
	if err := service.Store().AppendTaskLog(ctx, domain.TaskLog{ID: "log-delete", TaskID: task.ID, Stream: "stdout", Content: "hello", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := service.Store().AppendConversation(ctx, domain.ConversationMessage{ID: "msg-delete", TaskID: task.ID, Role: "assistant", Content: "done", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{AggregateType: "Task", EventType: "TaskDeleted"})
	defer unsubscribe()

	if err := service.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask archived returned error: %v", err)
	}
	select {
	case event := <-events:
		if event.EventType != "TaskDeleted" || event.AggregateID != task.ID {
			t.Fatalf("published delete event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task delete event")
	}
	if _, err := service.Task(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted Task err = %v, want not found", err)
	}
	if logs, err := service.Store().TaskLogs(ctx, task.ID); err != nil || len(logs) != 0 {
		t.Fatalf("TaskLogs after delete = %+v, %v", logs, err)
	}
	stored, err := service.DomainEvents(ctx, domain.EventFilter{AggregateID: task.ID, EventType: "TaskDeleted"})
	if err != nil || len(stored) != 1 {
		t.Fatalf("DomainEvents = %d, %v", len(stored), err)
	}
	outbox, err := service.OutboxMessages(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range outbox {
		if message.Event.EventType == "TaskDeleted" && message.Event.AggregateID == task.ID {
			found = message.Status == domain.OutboxPublished && message.PublishedAt != nil
		}
	}
	if !found {
		t.Fatalf("task delete outbox was not published: %+v", outbox)
	}
	if err := service.DeleteTask(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteTask missing err = %v, want not found", err)
	}
}

func TestServiceDeleteOccupiedWorkerDoesNotPublishDeleteEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-occupied", Name: "Busy", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{AggregateType: "Worker", EventType: "WorkerDeleted"})
	defer unsubscribe()

	if err := service.DeleteWorker(ctx, worker.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("DeleteWorker occupied err = %v, want conflict", err)
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected delete event = %+v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestServiceMarksStaleWorkersOfflineAndReconnects(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-stale", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(91 * time.Second)
	if err := service.MarkStaleWorkersOffline(ctx, 90*time.Second); err != nil {
		t.Fatalf("MarkStaleWorkersOffline returned error: %v", err)
	}
	loaded, err := service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.WorkerOffline {
		t.Fatalf("status = %s, want OFFLINE", loaded.Status)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	loaded, _ = service.Store().Worker(ctx, worker.ID)
	if loaded.Status != domain.WorkerOnline {
		t.Fatalf("status = %s, want ONLINE after reconnect", loaded.Status)
	}
}

func TestServiceAutoAssignAllowsWorkersWithRunningTasks(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	targetProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Target", GitURL: "git://target"})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Other", GitURL: "git://other"})
	if err != nil {
		t.Fatal(err)
	}
	offline, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-offline", Name: "Offline", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	unsupported, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-unsupported", Name: "Unsupported", SupportedAgents: []domain.AgentType{domain.AgentClaude}, WorkDir: "/tmp"})
	wrongProject, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-wrong-project", Name: "Wrong Project", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", BindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{otherProject.ID}})
	occupied, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-occupied", Name: "Occupied", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	for _, workerID := range []string{unsupported.ID, wrongProject.ID, occupied.ID} {
		if _, err := service.WorkerConnected(ctx, workerID); err != nil {
			t.Fatal(err)
		}
	}
	offline.MarkOffline(time.Now())
	if err := service.Store().SaveWorker(ctx, offline); err != nil {
		t.Fatal(err)
	}
	otherTask, err := service.CreateTask(ctx, CreateTaskInput{Title: "Other Task", ProjectID: targetProject.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, otherTask.ID, occupied.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, otherTask.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "Needs Worker", ProjectID: targetProject.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	started, _, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if started.WorkerID != occupied.ID {
		t.Fatalf("worker id = %q, want %q", started.WorkerID, occupied.ID)
	}
}
