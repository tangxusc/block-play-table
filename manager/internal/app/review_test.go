package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceSaveTaskGitBackupDefaults(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedCompletedTaskWithSession(t, ctx, service)

	if err := service.SaveTaskGitBackup(ctx, domain.TaskGitBackup{TaskID: task.ID, Paths: []string{"README.md"}, PatchPath: "/tmp/backup.patch"}); err != nil {
		t.Fatalf("SaveTaskGitBackup returned error: %v", err)
	}
	backups, err := service.Store().TaskGitBackups(ctx, task.ID)
	if err != nil || len(backups) != 1 {
		t.Fatalf("stored backups = %+v, %v", backups, err)
	}
	if !strings.HasPrefix(backups[0].ID, "git_backup_") || backups[0].CreatedAt != now {
		t.Fatalf("backup defaults = %+v", backups[0])
	}
}

func TestServiceSortFallbackHelpers(t *testing.T) {
	earlier := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Minute)

	if compareOptionalTimes(nil, &earlier) >= 0 {
		t.Fatal("nil optional time should sort before a set time")
	}
	if compareOptionalTimes(&later, &earlier) <= 0 {
		t.Fatal("later optional time should sort after earlier optional time")
	}

	if compareTaskFallback(&domain.Task{ID: "task-a", CreatedAt: earlier}, &domain.Task{ID: "task-b", CreatedAt: later}) <= 0 {
		t.Fatal("task fallback should prefer newer created time")
	}
	if compareTaskFallback(&domain.Task{ID: "task-a", CreatedAt: earlier}, &domain.Task{ID: "task-b", CreatedAt: earlier}) <= 0 {
		t.Fatal("task fallback should prefer descending id when created time ties")
	}
	if compareProjectFallback(&domain.Project{ID: "project-a", CreatedAt: earlier}, &domain.Project{ID: "project-b", CreatedAt: later}) <= 0 {
		t.Fatal("project fallback should prefer newer created time")
	}
	if compareProjectFallback(&domain.Project{ID: "project-a", CreatedAt: earlier}, &domain.Project{ID: "project-b", CreatedAt: earlier}) <= 0 {
		t.Fatal("project fallback should prefer descending id when created time ties")
	}
	if compareWorkerFallback(&domain.Worker{ID: "worker-a", CreatedAt: earlier}, &domain.Worker{ID: "worker-b", CreatedAt: later}) <= 0 {
		t.Fatal("worker fallback should prefer newer created time")
	}
	if compareWorkerFallback(&domain.Worker{ID: "worker-a", CreatedAt: earlier}, &domain.Worker{ID: "worker-b", CreatedAt: earlier}) <= 0 {
		t.Fatal("worker fallback should prefer descending id when created time ties")
	}
	if compareDomainEventFallback(domain.DomainEvent{EventID: "event-a", OccurredAt: earlier}, domain.DomainEvent{EventID: "event-b", OccurredAt: later}) <= 0 {
		t.Fatal("event fallback should prefer newer occurrence time")
	}
	if compareDomainEventFallback(domain.DomainEvent{EventID: "event-a", OccurredAt: earlier}, domain.DomainEvent{EventID: "event-b", OccurredAt: earlier}) <= 0 {
		t.Fatal("event fallback should prefer descending id when occurrence time ties")
	}
}

func TestServiceSortHelpersCoverRemainingFields(t *testing.T) {
	earlier := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Minute)
	heartbeatEarlier := earlier
	heartbeatLater := later

	tasks := []*domain.Task{
		{ID: "task-a", Title: "same", Status: domain.TaskCreated, CreatedAt: earlier, UpdatedAt: later, StartDate: later, EndDate: earlier},
		{ID: "task-b", Title: "same", Status: domain.TaskCompleted, CreatedAt: later, UpdatedAt: earlier, StartDate: earlier, EndDate: later},
	}
	sortTasks(tasks, TaskSort{Field: TaskSortUpdatedAt, Direction: SortDirectionAsc})
	if tasks[0].ID != "task-b" {
		t.Fatalf("tasks by updatedAt = %+v", tasks)
	}
	sortTasks(tasks, TaskSort{Field: TaskSortStatus, Direction: SortDirectionAsc})
	if tasks[0].ID != "task-b" {
		t.Fatalf("tasks by status = %+v", tasks)
	}
	sortTasks(tasks, TaskSort{Field: TaskSortStartDate, Direction: SortDirectionAsc})
	if tasks[0].ID != "task-b" {
		t.Fatalf("tasks by startDate = %+v", tasks)
	}
	sortTasks(tasks, TaskSort{Field: TaskSortEndDate, Direction: SortDirectionAsc})
	if tasks[0].ID != "task-a" {
		t.Fatalf("tasks by endDate = %+v", tasks)
	}
	sortTasks(tasks, TaskSort{Field: TaskSortTitle, Direction: SortDirectionAsc})
	if tasks[0].ID != "task-b" {
		t.Fatalf("tasks title fallback = %+v", tasks)
	}

	projects := []*domain.Project{
		{ID: "project-a", Name: "same", GitURL: "git://z", DefaultBranch: "z", CreatedAt: earlier, UpdatedAt: later},
		{ID: "project-b", Name: "same", GitURL: "git://a", DefaultBranch: "a", CreatedAt: later, UpdatedAt: earlier},
	}
	sortProjects(projects, ProjectSort{Field: ProjectSortUpdatedAt, Direction: SortDirectionAsc})
	if projects[0].ID != "project-b" {
		t.Fatalf("projects by updatedAt = %+v", projects)
	}
	sortProjects(projects, ProjectSort{Field: ProjectSortGitURL, Direction: SortDirectionAsc})
	if projects[0].ID != "project-b" {
		t.Fatalf("projects by gitURL = %+v", projects)
	}
	sortProjects(projects, ProjectSort{Field: ProjectSortDefaultBranch, Direction: SortDirectionAsc})
	if projects[0].ID != "project-b" {
		t.Fatalf("projects by defaultBranch = %+v", projects)
	}
	sortProjects(projects, ProjectSort{Field: ProjectSortName, Direction: SortDirectionAsc})
	if projects[0].ID != "project-b" {
		t.Fatalf("projects name fallback = %+v", projects)
	}

	workers := []*domain.Worker{
		{ID: "worker-a", Name: "same", Status: domain.WorkerOffline, CreatedAt: earlier, UpdatedAt: later, LastHeartbeatAt: &heartbeatLater},
		{ID: "worker-b", Name: "same", Status: domain.WorkerOnline, CreatedAt: later, UpdatedAt: earlier, LastHeartbeatAt: &heartbeatEarlier},
	}
	sortWorkers(workers, WorkerSort{Field: WorkerSortUpdatedAt, Direction: SortDirectionAsc})
	if workers[0].ID != "worker-b" {
		t.Fatalf("workers by updatedAt = %+v", workers)
	}
	sortWorkers(workers, WorkerSort{Field: WorkerSortStatus, Direction: SortDirectionAsc})
	if workers[0].ID != "worker-a" {
		t.Fatalf("workers by status = %+v", workers)
	}
	sortWorkers(workers, WorkerSort{Field: WorkerSortLastHeartbeatAt, Direction: SortDirectionAsc})
	if workers[0].ID != "worker-b" {
		t.Fatalf("workers by heartbeat = %+v", workers)
	}
	sortWorkers(workers, WorkerSort{Field: WorkerSortName, Direction: SortDirectionAsc})
	if workers[0].ID != "worker-b" {
		t.Fatalf("workers name fallback = %+v", workers)
	}

	events := []domain.DomainEvent{
		{EventID: "event-a", EventType: "same", AggregateType: "Z", AggregateID: "z", OccurredAt: earlier},
		{EventID: "event-b", EventType: "same", AggregateType: "A", AggregateID: "a", OccurredAt: later},
	}
	sortDomainEvents(events, DomainEventSort{Field: DomainEventSortAggregateType, Direction: SortDirectionAsc})
	if events[0].EventID != "event-b" {
		t.Fatalf("events by aggregateType = %+v", events)
	}
	sortDomainEvents(events, DomainEventSort{Field: DomainEventSortAggregateID, Direction: SortDirectionAsc})
	if events[0].EventID != "event-b" {
		t.Fatalf("events by aggregateID = %+v", events)
	}
	sortDomainEvents(events, DomainEventSort{Field: DomainEventSortEventType, Direction: SortDirectionAsc})
	if events[0].EventID != "event-b" {
		t.Fatalf("events type fallback = %+v", events)
	}
}
