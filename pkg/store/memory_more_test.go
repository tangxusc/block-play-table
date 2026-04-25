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
	worker, err := domain.NewWorker(domain.NewWorkerInput{ID: "worker-1", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", Now: now})
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
	if projects, _ := s.Projects(ctx); len(projects) != 1 {
		t.Fatalf("projects count = %d", len(projects))
	}
	settings := domain.NewSettings(now)
	settings.UpdateAgentRuntimeEnvVars([]domain.AgentRuntimeEnvVar{{Key: "A", Value: "B", Enabled: true}}, now)
	if err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AgentRuntimeEnvVars[0].Value != "B" {
		t.Fatalf("settings env = %+v", loaded.AgentRuntimeEnvVars)
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
