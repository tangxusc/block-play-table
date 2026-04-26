package store

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestMemoryStorePersistsAggregatesLogsConversationsAndEvents(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)

	project, err := domain.NewProject(domain.NewProjectInput{
		ID:                 "project-1",
		Name:               "Block Play Table",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "block-play-table",
		Now:                now,
	})
	if err != nil {
		t.Fatalf("NewProject returned error: %v", err)
	}
	task, err := domain.NewTask(domain.NewTaskInput{
		ID:         "task-1",
		Title:      "Task",
		ProjectID:  project.ID,
		AgentType:  domain.AgentCodex,
		BaseBranch: "main",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}

	if err := s.SaveProject(ctx, project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := s.SaveTask(ctx, task); err != nil {
		t.Fatalf("SaveTask returned error: %v", err)
	}
	if err := s.AppendTaskLog(ctx, domain.TaskLog{ID: "log-1", TaskID: task.ID, Stream: "stdout", Content: "hello", CreatedAt: now}); err != nil {
		t.Fatalf("AppendTaskLog returned error: %v", err)
	}
	if err := s.AppendConversation(ctx, domain.ConversationMessage{ID: "msg-1", TaskID: task.ID, Role: "assistant", Content: "done", CreatedAt: now}); err != nil {
		t.Fatalf("AppendConversation returned error: %v", err)
	}
	if err := s.AppendEvents(ctx, task.PullEvents()); err != nil {
		t.Fatalf("AppendEvents returned error: %v", err)
	}

	loaded, err := s.Task(ctx, task.ID)
	if err != nil {
		t.Fatalf("Task returned error: %v", err)
	}
	if loaded.Title != "Task" {
		t.Fatalf("loaded task title = %q", loaded.Title)
	}
	logs, err := s.TaskLogs(ctx, task.ID)
	if err != nil || len(logs) != 1 {
		t.Fatalf("TaskLogs = %d, %v; want one log", len(logs), err)
	}
	messages, err := s.TaskConversations(ctx, task.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("TaskConversations = %d, %v; want one message", len(messages), err)
	}
	events, err := s.DomainEvents(ctx, domain.EventFilter{AggregateID: task.ID})
	if err != nil || len(events) != 1 {
		t.Fatalf("DomainEvents = %d, %v; want one event", len(events), err)
	}
}
