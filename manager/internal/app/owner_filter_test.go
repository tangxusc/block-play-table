package app

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestCreateTaskSetsOwnerUserID(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	}))

	project, err := service.CreateProject(ctx, CreateProjectInput{
		Name:               "test-project",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "test",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:       "my task",
		ProjectID:   project.ID,
		OwnerUserID: "user-abc",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.OwnerUserID != "user-abc" {
		t.Errorf("OwnerUserID = %q, want %q", task.OwnerUserID, "user-abc")
	}
}

func TestTasksFilteredByOwnerUserID(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	}))

	project, _ := service.CreateProject(ctx, CreateProjectInput{
		Name: "proj", GitURL: "file:///tmp/r", DefaultBranch: "main", WorktreeNamePrefix: "p",
	})

	service.CreateTask(ctx, CreateTaskInput{
		Title: "task-a", ProjectID: project.ID, OwnerUserID: "alice",
	})
	service.CreateTask(ctx, CreateTaskInput{
		Title: "task-b", ProjectID: project.ID, OwnerUserID: "bob",
	})
	service.CreateTask(ctx, CreateTaskInput{
		Title: "task-c", ProjectID: project.ID, OwnerUserID: "alice",
	})

	tasks, total, err := service.TasksFilteredSorted(ctx, TaskFilter{OwnerUserID: "alice"}, TaskSort{}, PageInput{Limit: 100})
	if err != nil {
		t.Fatalf("TasksFilteredSorted: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	for _, task := range tasks {
		if task.OwnerUserID != "alice" {
			t.Errorf("task %s has OwnerUserID %q, want %q", task.ID, task.OwnerUserID, "alice")
		}
	}

	allTasks, allTotal, _ := service.TasksFilteredSorted(ctx, TaskFilter{}, TaskSort{}, PageInput{Limit: 100})
	if allTotal != 3 {
		t.Errorf("unfiltered total = %d, want 3", allTotal)
	}
	_ = allTasks
	_ = domain.TaskCreated
}
