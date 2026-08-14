package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

// TestServiceTaskFilterBranches 验证每个任务过滤条件同时保留匹配项并排除非匹配项。
func TestServiceTaskFilterBranches(t *testing.T) {
	ctx := context.Background()
	storage := store.NewMemoryStore()
	service := NewService(storage)
	tasks := []*domain.Task{
		{ID: "match", Title: "Needle task", Description: "description", Status: domain.TaskRunning, ProjectID: "project", WorkerID: "worker", AgentType: domain.AgentCodex, OwnerUserID: "owner"},
		{ID: "other", Title: "Other task", Status: domain.TaskCreated, ProjectID: "other-project", WorkerID: "other-worker", AgentType: domain.AgentClaude, OwnerUserID: "other-owner"},
		{ID: "archived", Title: "Archived task", Status: domain.TaskArchived, ProjectID: "archived-project", AgentType: domain.AgentClaude},
	}
	for _, task := range tasks {
		if err := storage.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	filters := []TaskFilter{
		{Status: domain.TaskRunning, IncludeArchived: true},
		{ProjectID: "project", IncludeArchived: true},
		{WorkerID: "worker", IncludeArchived: true},
		{AgentType: domain.AgentCodex, IncludeArchived: true},
		{OwnerUserID: "owner", IncludeArchived: true},
		{Search: "needle", IncludeArchived: true},
	}
	for index, filter := range filters {
		got, total, err := service.TasksFilteredSorted(ctx, filter, TaskSort{}, PageInput{Limit: 100})
		if err != nil {
			t.Fatalf("filter %d: %v", index, err)
		}
		if total != 1 || len(got) != 1 || got[0].ID != "match" {
			t.Fatalf("filter %d result = %+v total=%d", index, got, total)
		}
	}
	got, total, err := service.TasksFilteredSorted(ctx, TaskFilter{}, TaskSort{}, PageInput{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("default archive filter = %+v total=%d", got, total)
	}
	got, total, err = service.TasksFilteredSorted(ctx, TaskFilter{IncludeArchived: true}, TaskSort{}, PageInput{Limit: 100})
	if err != nil || total != 3 || len(got) != 3 {
		t.Fatalf("include archived = %+v total=%d err=%v", got, total, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := service.TasksFilteredSorted(canceled, TaskFilter{}, TaskSort{}, PageInput{}); err != nil {
		// MemoryStore 的 Tasks 当前不读取 context；此断言固定其现有行为并覆盖空分页。
		t.Fatalf("memory task list unexpectedly used context: %v", err)
	}
}

// TestServiceWorkerFilterBranches 验证 Worker 状态、能力、项目绑定和全文检索过滤分支。
func TestServiceWorkerFilterBranches(t *testing.T) {
	ctx := context.Background()
	storage := store.NewMemoryStore()
	service := NewService(storage)
	workers := []*domain.Worker{
		{ID: "match", Name: "Needle runner", Status: domain.WorkerOnline, SupportedAgents: []domain.AgentType{domain.AgentCodex}, ProjectBindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{"project"}},
		{ID: "other", Name: "Other runner", Status: domain.WorkerOffline, SupportedAgents: []domain.AgentType{domain.AgentClaude}, ProjectBindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{"other-project"}},
		{ID: "disabled", Name: "Disabled runner", Status: domain.WorkerDisabled, SupportedAgents: []domain.AgentType{domain.AgentCodex}, ProjectBindingMode: domain.WorkerAllProjects},
	}
	for _, worker := range workers {
		if err := storage.SaveWorker(ctx, worker); err != nil {
			t.Fatal(err)
		}
	}

	filters := []WorkerFilter{
		{Status: domain.WorkerOnline, IncludeDisabled: true},
		{AgentType: domain.AgentCodex},
		{ProjectID: "project"},
		{Search: "needle"},
	}
	for index, filter := range filters {
		got, err := service.WorkersFiltered(ctx, filter)
		if err != nil {
			t.Fatalf("filter %d: %v", index, err)
		}
		if len(got) != 1 || got[0].ID != "match" {
			t.Fatalf("filter %d result = %+v", index, got)
		}
	}
	got, err := service.WorkersFiltered(ctx, WorkerFilter{})
	if err != nil || len(got) != 2 {
		t.Fatalf("default disabled filter = %+v err=%v", got, err)
	}
	got, err = service.WorkersFiltered(ctx, WorkerFilter{IncludeDisabled: true})
	if err != nil || len(got) != 3 {
		t.Fatalf("include disabled = %+v err=%v", got, err)
	}
}

// TestServiceInteractionValidationBranches 覆盖交互回复的输入、幂等和关联资源校验分支。
func TestServiceInteractionValidationBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	storage := store.NewMemoryStore()
	service := NewService(storage, WithClock(func() time.Time { return now }))

	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{}); err == nil {
		t.Fatal("empty interaction id should fail")
	}
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{InteractionID: "interaction", Decision: domain.TaskInteractionDecision("UNKNOWN")}); err == nil {
		t.Fatal("unsupported decision should fail")
	}
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{InteractionID: "missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing interaction err = %v", err)
	}

	answered := domain.TaskInteraction{ID: "answered", TaskID: "task", Kind: domain.TaskInteractionUserInput, Status: domain.TaskInteractionAnswered, ResponseMessage: "same", CreatedAt: now, UpdatedAt: now}
	if err := storage.SaveTaskInteraction(ctx, answered); err != nil {
		t.Fatal(err)
	}
	if got, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{InteractionID: answered.ID, Message: "same"}); err != nil || got.ID != answered.ID {
		t.Fatalf("idempotent response = %+v err=%v", got, err)
	}
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{InteractionID: answered.ID, Message: "different"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("different repeated response err = %v", err)
	}

	invalid := domain.TaskInteraction{ID: "invalid-status", TaskID: "task", Kind: domain.TaskInteractionUserInput, Status: domain.TaskInteractionStatus("EXPIRED"), CreatedAt: now, UpdatedAt: now}
	if err := storage.SaveTaskInteraction(ctx, invalid); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{InteractionID: invalid.ID, Message: "reply"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("invalid status err = %v", err)
	}

	if err := storage.SaveTask(ctx, &domain.Task{ID: "task-no-worker", Status: domain.TaskWaitingInput}); err != nil {
		t.Fatal(err)
	}
	pending := domain.TaskInteraction{ID: "pending-no-worker", TaskID: "task-no-worker", Kind: domain.TaskInteractionUserInput, Status: domain.TaskInteractionPending, CreatedAt: now, UpdatedAt: now}
	if err := storage.SaveTaskInteraction(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{InteractionID: pending.ID, Message: "reply"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("interaction without worker err = %v", err)
	}

	if err := storage.SaveTask(ctx, &domain.Task{ID: "task-no-round", Status: domain.TaskWaitingInput, WorkerID: "worker"}); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveWorker(ctx, &domain.Worker{ID: "worker", Status: domain.WorkerOnline}); err != nil {
		t.Fatal(err)
	}
	pending = domain.TaskInteraction{ID: "pending-no-round", TaskID: "task-no-round", Kind: domain.TaskInteractionUserInput, Status: domain.TaskInteractionPending, CreatedAt: now, UpdatedAt: now}
	if err := storage.SaveTaskInteraction(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{InteractionID: pending.ID, Message: "reply"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("interaction without round err = %v", err)
	}
}

// TestServiceReleaseWorkerAndMigrationHelperBranches 覆盖 Worker 释放、迁移状态和备份显式字段分支。
func TestServiceReleaseWorkerAndMigrationHelperBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)
	storage := store.NewMemoryStore()
	service := NewService(storage, WithClock(func() time.Time { return now.Add(time.Hour) }))

	if err := service.releaseWorkerFromTask(ctx, &domain.Task{ID: "task"}, now); err != nil {
		t.Fatal(err)
	}
	if err := service.releaseWorkerFromTask(ctx, &domain.Task{ID: "task", WorkerID: "missing"}, now); err != nil {
		t.Fatal(err)
	}
	worker := &domain.Worker{ID: "worker", CurrentTaskIDs: []string{"other", "task"}}
	if err := storage.SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := service.releaseWorkerIDFromTask(ctx, worker.ID, "absent", now); err != nil {
		t.Fatal(err)
	}
	if err := service.releaseWorkerIDFromTask(ctx, worker.ID, "task", now); err != nil {
		t.Fatal(err)
	}
	loaded, err := storage.Worker(ctx, worker.ID)
	if err != nil || len(loaded.CurrentTaskIDs) != 1 || loaded.CurrentTaskIDs[0] != "other" {
		t.Fatalf("released worker = %+v err=%v", loaded, err)
	}

	for _, status := range []domain.TaskStatus{domain.TaskStarting, domain.TaskRunning, domain.TaskWaitingInput, domain.TaskInterrupting} {
		if !isLegacyA2AActiveTask(status) {
			t.Fatalf("status %s should be active during migration", status)
		}
	}
	if isLegacyA2AActiveTask(domain.TaskCompleted) {
		t.Fatal("completed task should not be an active legacy task")
	}

	createdAt := now
	backup := domain.TaskGitBackup{ID: "backup-explicit", TaskID: "task", CreatedAt: createdAt}
	if err := service.SaveTaskGitBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	backups, err := storage.TaskGitBackups(ctx, backup.TaskID)
	if err != nil || len(backups) != 1 || backups[0].ID != backup.ID || !backups[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("explicit backup = %+v err=%v", backups, err)
	}
}

// TestServiceAssignFirstAvailableWorkerBranches 验证自动分配会跳过不兼容 Worker，并在无候选时明确失败。
func TestServiceAssignFirstAvailableWorkerBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC)
	storage := store.NewMemoryStore()
	service := NewService(storage, WithClock(func() time.Time { return now }))
	task := &domain.Task{ID: "task-auto", Status: domain.TaskCreated, AgentType: domain.AgentCodex, ProjectID: "project"}
	if _, err := service.assignFirstAvailableWorker(ctx, task, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("assign without worker err=%v", err)
	}
	for _, worker := range []*domain.Worker{
		{ID: "worker-incompatible", Status: domain.WorkerOnline, SupportedAgents: []domain.AgentType{domain.AgentClaude}, ProjectBindingMode: domain.WorkerAllProjects},
		{ID: "worker-compatible", Status: domain.WorkerOnline, SupportedAgents: []domain.AgentType{domain.AgentCodex}, ProjectBindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{"project"}},
	} {
		if err := storage.SaveWorker(ctx, worker); err != nil {
			t.Fatal(err)
		}
	}
	assigned, err := service.assignFirstAvailableWorker(ctx, task, now)
	if err != nil || assigned.WorkerID != "worker-compatible" || assigned.Status != domain.TaskAssigned {
		t.Fatalf("automatic assignment=%+v err=%v", assigned, err)
	}
	if _, err := service.assignFirstAvailableWorker(ctx, &domain.Task{ID: "invalid", Status: domain.TaskRunning, AgentType: domain.AgentCodex, ProjectID: "project"}, now); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("assignment transition err=%v", err)
	}
}

// TestServiceMarkWorkerLostBranches 验证 Worker 丢失时按任务状态释放或失败，且跳过不存在的任务。
func TestServiceMarkWorkerLostBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC)
	storage := store.NewMemoryStore()
	service := NewService(storage, WithClock(func() time.Time { return now }))
	worker := &domain.Worker{
		ID: "worker-lost", Status: domain.WorkerOnline,
		CurrentTaskIDs: []string{"missing", "created", "assigned", "completed", "running"},
	}
	if err := storage.SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	for _, task := range []*domain.Task{
		{ID: "created", Status: domain.TaskCreated, WorkerID: worker.ID},
		{ID: "assigned", Status: domain.TaskAssigned, WorkerID: worker.ID},
		{ID: "completed", Status: domain.TaskCompleted, WorkerID: worker.ID},
		{ID: "running", Status: domain.TaskRunning, WorkerID: worker.ID},
	} {
		if err := storage.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	lost, err := service.MarkWorkerLost(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lost.Status != domain.WorkerOffline {
		t.Fatalf("lost worker status=%s", lost.Status)
	}
	running, err := storage.Task(ctx, "running")
	if err != nil || running.Status != domain.TaskFailed {
		t.Fatalf("running task=%+v err=%v", running, err)
	}
	for _, taskID := range lost.CurrentTaskIDs {
		if taskID == "assigned" || taskID == "running" {
			t.Fatalf("released task %s remains on worker: %+v", taskID, lost.CurrentTaskIDs)
		}
	}
	if _, err := service.MarkWorkerLost(ctx, "missing-worker"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing worker err=%v", err)
	}
	if _, err := service.MarkWorkerLost(ctx, worker.ID); err != nil {
		t.Fatalf("already offline worker=%v", err)
	}
}

// TestServiceCreateTaskRejectsArchivedProjectAndUnboundConfig 验证任务创建前的项目与 Agent 配置约束。
func TestServiceCreateTaskRejectsArchivedProjectAndUnboundConfig(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "Project", GitURL: "file:///tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	config := &domain.AgentExecutionConfig{WorkMode: domain.AgentWorkModeImplement}
	if _, err := service.CreateTask(ctx, CreateTaskInput{Title: "Task", ProjectID: project.ID, AgentType: domain.AgentCodex, AgentConfig: config}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unbound config err=%v", err)
	}
	if _, err := service.ArchiveProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{Title: "Task", ProjectID: project.ID, AgentType: domain.AgentCodex}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archived project err=%v", err)
	}
}
