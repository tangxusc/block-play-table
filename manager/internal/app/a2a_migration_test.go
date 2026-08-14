package app

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

// TestServiceMigrateLegacyA2ATasksFailsWaitingInputAndReleasesResources 验证旧等待任务迁移时原子结束并释放关联资源。
func TestServiceMigrateLegacyA2ATasksFailsWaitingInputAndReleasesResources(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task, worker, interaction := seedLegacyWaitingInputTask(t, ctx, service, now)

	migrated, err := service.MigrateLegacyA2ATasks(ctx)
	if err != nil {
		t.Fatalf("MigrateLegacyA2ATasks 返回错误: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("迁移数量 = %d，期望 1", migrated)
	}

	failed, err := service.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantReason := string(a2aext.ErrorA2AMigrationRetryRequired) + ": " + legacyA2AMigrationMessage
	if failed.Status != domain.TaskFailed || failed.Result != wantReason {
		t.Fatalf("迁移后任务 = %+v，期望 FAILED 且原因 %q", failed, wantReason)
	}
	loadedWorker, err := service.Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedWorker.CurrentTaskIDs) != 0 {
		t.Fatalf("迁移后 Worker 当前任务 = %+v，期望已释放", loadedWorker.CurrentTaskIDs)
	}
	canceled, err := service.Store().TaskInteraction(ctx, interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.TaskInteractionCanceled || canceled.ResponseDecision != domain.TaskInteractionCancel {
		t.Fatalf("迁移后交互 = %+v，期望已取消", canceled)
	}
	pending, err := service.Store().TaskInteractions(ctx, task.ID, domain.TaskInteractionPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("迁移后待处理交互 = %+v，期望为空", pending)
	}

	failedVersion := failed.Version
	workerVersion := loadedWorker.Version
	interactionUpdatedAt := canceled.UpdatedAt
	migrated, err = service.MigrateLegacyA2ATasks(ctx)
	if err != nil {
		t.Fatalf("重复 MigrateLegacyA2ATasks 返回错误: %v", err)
	}
	if migrated != 0 {
		t.Fatalf("重复迁移数量 = %d，期望 0", migrated)
	}
	afterRepeat, err := service.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterRepeatWorker, err := service.Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterRepeatInteraction, err := service.Store().TaskInteraction(ctx, interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRepeat.Version != failedVersion || afterRepeatWorker.Version != workerVersion || !afterRepeatInteraction.UpdatedAt.Equal(interactionUpdatedAt) {
		t.Fatalf("重复迁移发生额外写入: taskVersion=%d/%d workerVersion=%d/%d interactionUpdatedAt=%s/%s",
			afterRepeat.Version, failedVersion, afterRepeatWorker.Version, workerVersion,
			afterRepeatInteraction.UpdatedAt, interactionUpdatedAt)
	}
}

// TestServiceMigrateLegacyA2ATasksSkipsWaitingInputTaskWithRound 验证已有 A2A round 的等待任务不会被旧协议迁移误伤。
func TestServiceMigrateLegacyA2ATasksSkipsWaitingInputTaskWithRound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 21, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedRunningTask(t, ctx, service)
	waiting := applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, domain.TaskA2ARemoteStatusInputRequired, nil, map[string]any{
		"interactionId": "interaction-with-round",
		"kind":          string(a2aext.InteractionUserInput),
		"title":         "Need input",
		"body":          "Keep this interaction pending",
	})
	if waiting.Status != domain.TaskWaitingInput {
		t.Fatalf("测试前任务状态 = %s，期望 WAITING_INPUT", waiting.Status)
	}
	workerBefore, err := service.Worker(ctx, waiting.WorkerID)
	if err != nil {
		t.Fatal(err)
	}
	roundsBefore, err := service.Store().TaskA2ARounds(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundsBefore) != 1 {
		t.Fatalf("测试前 A2A round = %+v，期望 1 条", roundsBefore)
	}

	migrated, err := service.MigrateLegacyA2ATasks(ctx)
	if err != nil {
		t.Fatalf("MigrateLegacyA2ATasks 返回错误: %v", err)
	}
	if migrated != 0 {
		t.Fatalf("已有 round 的任务迁移数量 = %d，期望 0", migrated)
	}
	after, err := service.Task(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.TaskWaitingInput || after.Version != waiting.Version || after.Result != waiting.Result {
		t.Fatalf("已有 round 的任务被修改: before=%+v after=%+v", waiting, after)
	}
	workerAfter, err := service.Worker(ctx, waiting.WorkerID)
	if err != nil {
		t.Fatal(err)
	}
	if workerAfter.Version != workerBefore.Version || len(workerAfter.CurrentTaskIDs) != 1 || workerAfter.CurrentTaskIDs[0] != waiting.ID {
		t.Fatalf("已有 round 的 Worker 占用被修改: before=%+v after=%+v", workerBefore.CurrentTaskIDs, workerAfter.CurrentTaskIDs)
	}
	interaction, err := service.Store().TaskInteraction(ctx, "interaction-with-round")
	if err != nil {
		t.Fatal(err)
	}
	if interaction.Status != domain.TaskInteractionPending {
		t.Fatalf("已有 round 的待处理交互状态 = %s，期望 PENDING", interaction.Status)
	}
	roundsAfter, err := service.Store().TaskA2ARounds(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundsAfter) != 1 || roundsAfter[0].ID != roundsBefore[0].ID || roundsAfter[0].Version != roundsBefore[0].Version {
		t.Fatalf("迁移后 A2A round 被修改: before=%+v after=%+v", roundsBefore, roundsAfter)
	}
}

func seedLegacyWaitingInputTask(t *testing.T, ctx context.Context, service *Service, now time.Time) (*domain.Task, *domain.Worker, *domain.TaskInteraction) {
	t.Helper()
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "Legacy migration", GitURL: "file:///tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID: "worker-legacy-migration", Name: "Legacy worker", SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir: "/tmp/legacy-worker", BindingMode: domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title: "Legacy waiting task", ProjectID: project.ID, WorkerID: worker.ID,
		AgentType: domain.AgentCodex, BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err = service.Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Start(now); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkRunning("/tmp/legacy-worktree", now); err != nil {
		t.Fatal(err)
	}
	if err := worker.AssignTask(task.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := task.RequestInteraction("interaction-legacy-migration", domain.TaskInteractionUserInput, "Need migration input", now); err != nil {
		t.Fatal(err)
	}
	interaction, err := domain.NewTaskInteraction(domain.TaskInteraction{
		ID: "interaction-legacy-migration", TaskID: task.ID, Kind: domain.TaskInteractionUserInput,
		Status: domain.TaskInteractionPending, Title: "Need migration input", Body: "Pending before upgrade",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 旧控制面已持久化这些事件，测试只保留升级时可见的聚合快照。
	task.PullEvents()
	worker.PullEvents()
	if err := service.Store().SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := service.Store().SaveWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := service.Store().SaveTaskInteraction(ctx, *interaction); err != nil {
		t.Fatal(err)
	}
	return task, worker, interaction
}
