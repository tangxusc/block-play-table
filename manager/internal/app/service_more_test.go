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
