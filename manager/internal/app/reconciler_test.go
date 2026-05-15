package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestReconcilerMarksWorkerLostAndFailsAssignedTasks(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	service := NewService(store.NewMemoryStore(), WithClock(clock))

	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-lost",
		Name:            "lost",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:     "T",
		ProjectID: project.ID,
		AgentType: domain.AgentCodex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskStarted(ctx, "started-1", task.ID, "/tmp/worktree"); err != nil {
		t.Fatal(err)
	}

	// Simulate stale heartbeat by advancing the clock past timeout.
	now = now.Add(10 * time.Minute)
	reconciler := NewReconciler(service, time.Minute, time.Second)
	if err := reconciler.ReconcileWorkerLiveness(ctx); err != nil {
		t.Fatalf("ReconcileWorkerLiveness returned error: %v", err)
	}

	loaded, err := service.Store().Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.TaskFailed {
		t.Fatalf("task status = %s, want FAILED", loaded.Status)
	}
	if loaded.Result == "" {
		t.Fatalf("task result should record worker_lost reason, got %q", loaded.Result)
	}
	loadedWorker, err := service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedWorker.Status != domain.WorkerOffline {
		t.Fatalf("worker status = %s, want OFFLINE", loadedWorker.Status)
	}
	if len(loadedWorker.CurrentTaskIDs) != 0 {
		t.Fatalf("worker current tasks = %+v, want empty", loadedWorker.CurrentTaskIDs)
	}
}

func TestReconcilerSkipsHealthyWorkers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))

	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-healthy",
		Name:            "healthy",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerHeartbeat(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	reconciler := NewReconciler(service, 5*time.Minute, 0)
	if err := reconciler.ReconcileWorkerLiveness(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.WorkerOnline {
		t.Fatalf("worker status = %s, want ONLINE", loaded.Status)
	}
}

func TestReconcilerRunStopsOnContextCancel(t *testing.T) {
	service := NewService(store.NewMemoryStore())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reconciler := NewReconciler(service, 100*time.Millisecond, 10*time.Millisecond, WithReconcilerLogger(logger))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciler.Run did not stop on cancel")
	}
}

func TestReconcilerRunNoOpForZeroTimeout(t *testing.T) {
	service := NewService(store.NewMemoryStore())
	reconciler := NewReconciler(service, 0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciler.Run with zero timeout should return immediately")
	}
}

func TestReconcileWorkerLivenessOfflineWithoutTasksIsNoOp(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "w-idle", Name: "idle", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/worker"}); err != nil {
		t.Fatal(err)
	}
	reconciler := NewReconciler(service, time.Minute, time.Second)
	if err := reconciler.ReconcileWorkerLiveness(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Store().Worker(ctx, "w-idle")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.WorkerRegistered {
		t.Fatalf("worker status = %s, want REGISTERED", loaded.Status)
	}
}

func TestServiceAssignedTasksAndDirectiveHelpers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))

	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:              "worker-watch",
		Name:            "watch",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex, WorkerID: worker.ID})
	if err != nil {
		t.Fatal(err)
	}

	if assigned, err := service.AssignedTasks(ctx, worker.ID); err != nil {
		t.Fatal(err)
	} else if len(assigned) != 0 {
		t.Fatalf("AssignedTasks before start should be empty, got %d", len(assigned))
	}

	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	assigned, err := service.AssignedTasks(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assigned) != 1 || assigned[0].ID != task.ID {
		t.Fatalf("AssignedTasks = %+v, want one starting task", assigned)
	}

	loadedTask, project2, worker2, err := service.AssignedTaskBundle(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedTask.ID != task.ID || project2.ID != project.ID || worker2.ID != worker.ID {
		t.Fatalf("AssignedTaskBundle returned mismatched aggregates")
	}
	if payload := service.BuildStartPayload(loadedTask, project2, worker2); payload.Task.ID != task.ID || payload.Project.ID != project.ID {
		t.Fatalf("BuildStartPayload payload = %+v", payload)
	}
	if payload := service.BuildContinuePayload(loadedTask, project2, worker2, "follow up"); payload.Message != "follow up" || payload.Task.ID != task.ID {
		t.Fatalf("BuildContinuePayload payload = %+v", payload)
	}

	// Drive the task into RUNNING + COMPLETED, then continue to populate a CONTINUE directive,
	// and verify AckTaskDirective clears it.
	if _, err := service.ApplyWorkerTaskStarted(ctx, "started-watch", task.ID, "/tmp/worktree"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskCompleted(ctx, "completed-watch", task.ID, "done", "session-1"); err != nil {
		t.Fatal(err)
	}
	continued, _, err := service.ContinueTask(ctx, ContinueTaskInput{TaskID: task.ID, Message: "follow up"})
	if err != nil {
		t.Fatal(err)
	}
	if continued.PendingDirective == nil || continued.PendingDirective.Kind != domain.TaskDirectiveContinue {
		t.Fatalf("expected continue directive, got %+v", continued.PendingDirective)
	}
	directiveID := continued.PendingDirective.ID
	if _, err := service.AckTaskDirective(ctx, task.ID, directiveID); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Store().Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PendingDirective != nil {
		t.Fatalf("PendingDirective should be cleared after Ack, got %+v", loaded.PendingDirective)
	}
	// Acking the same directive twice is a no-op.
	if _, err := service.AckTaskDirective(ctx, task.ID, directiveID); err != nil {
		t.Fatalf("idempotent Ack returned error: %v", err)
	}
}
