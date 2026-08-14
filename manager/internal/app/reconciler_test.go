package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestReconcilerMarksWorkerOfflineAndPreservesA2ATasksForRecovery(t *testing.T) {
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
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	bindTestA2ARound(t, ctx, service, task.ID)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventWorkspaceReady, domain.TaskA2ARemoteStatusWorking, &a2aext.RuntimeInfo{WorktreePath: "/tmp/worktree"}, map[string]any{})

	// 保留运行中任务，等待 A2A 对账确认远端终态，避免仅凭心跳误判失败。
	now = now.Add(10 * time.Minute)
	reconciler := NewReconciler(service, time.Minute, time.Second)
	if err := reconciler.ReconcileWorkerLiveness(ctx); err != nil {
		t.Fatalf("ReconcileWorkerLiveness returned error: %v", err)
	}

	loaded, err := service.Store().Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.TaskRunning {
		t.Fatalf("task status = %s, want RUNNING", loaded.Status)
	}
	if loaded.Result != "" {
		t.Fatalf("离线期间不应提前写入失败结果，实际为 %q", loaded.Result)
	}
	loadedWorker, err := service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedWorker.Status != domain.WorkerOffline {
		t.Fatalf("worker status = %s, want OFFLINE", loadedWorker.Status)
	}
	if len(loadedWorker.CurrentTaskIDs) != 1 || loadedWorker.CurrentTaskIDs[0] != task.ID {
		t.Fatalf("worker current tasks = %+v, want [%s]", loadedWorker.CurrentTaskIDs, task.ID)
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
