package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceDesignCRUDFilteringAndSettings(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))

	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded, err := service.Project(ctx, project.ID); err != nil || loaded.ID != project.ID {
		t.Fatalf("Project = %+v, %v", loaded, err)
	}
	project, err = service.UpdateProject(ctx, UpdateProjectInput{ID: project.ID, Name: "P2", GitURL: "git://repo2", DefaultBranch: "develop", WorktreeNamePrefix: "p2"})
	if err != nil {
		t.Fatalf("UpdateProject returned error: %v", err)
	}
	if project.DefaultBranch != "develop" || project.WorktreeNamePrefix != "p2" {
		t.Fatalf("updated project = %+v", project)
	}

	archivable, err := service.CreateProject(ctx, CreateProjectInput{Name: "Archive", GitURL: "git://archive"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ArchiveProject(ctx, archivable.ID); err != nil {
		t.Fatalf("ArchiveProject returned error: %v", err)
	}
	if projects, err := service.ProjectsFiltered(ctx, false); err != nil || len(projects) != 1 {
		t.Fatalf("ProjectsFiltered active = %d, %v", len(projects), err)
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{Title: "Archived Project", ProjectID: archivable.ID, AgentType: domain.AgentCodex}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("CreateTask archived project err = %v, want conflict", err)
	}

	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-design", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", BindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Worker(ctx, worker.ID); err != nil || got.ID != worker.ID {
		t.Fatalf("Worker = %+v, %v", got, err)
	}
	if workers, err := service.WorkersFiltered(ctx, WorkerFilter{ProjectID: project.ID, AgentType: domain.AgentCodex}); err != nil || len(workers) != 1 {
		t.Fatalf("WorkersFiltered = %d, %v", len(workers), err)
	}

	task, err := service.CreateTask(ctx, CreateTaskInput{
		Title:     "T",
		ProjectID: project.ID,
		AgentType: domain.AgentCodex,
		StartDate: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !task.StartDate.Equal(time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)) || !task.EndDate.Equal(time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("created task display dates = %s %s", task.StartDate, task.EndDate)
	}
	updated, err := service.UpdateTask(ctx, UpdateTaskInput{ID: task.ID, Title: "T2", ProjectID: project.ID, AgentType: domain.AgentCodex, BaseBranch: "develop", PreCommands: []string{"make pre"}})
	if err != nil {
		t.Fatalf("UpdateTask returned error: %v", err)
	}
	if updated.Title != "T2" || len(updated.PreCommands) != 1 || !updated.StartDate.Equal(task.StartDate) || !updated.EndDate.Equal(task.EndDate) {
		t.Fatalf("updated task = %+v", updated)
	}
	if _, err := service.UpdateTask(ctx, UpdateTaskInput{
		ID:        task.ID,
		Title:     "Bad Dates",
		ProjectID: project.ID,
		AgentType: domain.AgentCodex,
		StartDate: time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC),
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("UpdateTask invalid dates err = %v, want conflict", err)
	}
	if tasks, total, err := service.TasksFiltered(ctx, TaskFilter{ProjectID: project.ID, AgentType: domain.AgentCodex}, PageInput{Limit: 1}); err != nil || total != 1 || len(tasks) != 1 {
		t.Fatalf("TasksFiltered = len %d total %d err %v", len(tasks), total, err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateWorker(ctx, RegisterWorkerInput{ID: worker.ID, Name: "W2", SupportedAgents: []domain.AgentType{domain.AgentClaude}, WorkDir: "/tmp"}); err != nil {
		t.Fatalf("UpdateWorker should allow management edits: %v", err)
	}
	if _, err := service.UpdateTask(ctx, UpdateTaskInput{ID: task.ID, Title: "Bad", ProjectID: project.ID, AgentType: domain.AgentCodex}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("UpdateTask incompatible worker err = %v, want conflict", err)
	}
	if _, err := service.UpdateWorker(ctx, RegisterWorkerInput{ID: worker.ID, Name: "W3", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", BindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{project.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskAccepted(ctx, "accepted-1", task.ID); err != nil {
		t.Fatalf("ApplyWorkerTaskAccepted returned error: %v", err)
	}
	if _, err := service.ApplyWorkerTaskStarted(ctx, "started-design", task.ID, "/tmp/worktree"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerWaitingInput(ctx, "waiting-design", task.ID, "need input"); err != nil {
		t.Fatalf("ApplyWorkerWaitingInput returned error: %v", err)
	}
	if _, err := service.ApplyWorkerTaskFailed(ctx, "failed-design", task.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	if retried, err := service.RetryTask(ctx, task.ID); err != nil || retried.Status != domain.TaskCreated {
		t.Fatalf("RetryTask = %+v, %v", retried, err)
	}
	if archivedTask, err := service.ArchiveTask(ctx, task.ID); err != nil || archivedTask.Status != domain.TaskArchived {
		t.Fatalf("ArchiveTask = %+v, %v", archivedTask, err)
	}

	worker, err = service.UpdateWorkerProjectBindings(ctx, worker.ID, domain.WorkerAllProjects, nil)
	if err != nil || worker.ProjectBindingMode != domain.WorkerAllProjects {
		t.Fatalf("UpdateWorkerProjectBindings all = %+v, %v", worker, err)
	}
	if worker, err = service.DisableWorker(ctx, worker.ID); err != nil || worker.Status != domain.WorkerDisabled {
		t.Fatalf("DisableWorker = %+v, %v", worker, err)
	}
	if worker, err = service.EnableWorker(ctx, worker.ID); err != nil || worker.Status != domain.WorkerRegistered {
		t.Fatalf("EnableWorker = %+v, %v", worker, err)
	}
	if err := service.DeleteWorker(ctx, worker.ID); err != nil {
		t.Fatalf("DeleteWorker returned error: %v", err)
	}
	if err := service.DeleteWorker(ctx, worker.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteWorker missing err = %v, want not found", err)
	}

	if _, err := service.UpdateWorkerHeartbeatTimeout(ctx, "bad"); err == nil {
		t.Fatal("UpdateWorkerHeartbeatTimeout should reject invalid duration")
	}
	worker, err = service.RegisterWorker(ctx, RegisterWorkerInput{
		ID:                     "worker-env-preserve",
		Name:                   "Env",
		SupportedAgents:        []domain.AgentType{domain.AgentCodex},
		WorkDir:                "/tmp",
		ReplaceAgentRuntimeEnv: true,
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{
			AgentType: domain.AgentCodex,
			Vars:      []domain.AgentRuntimeEnvVar{{Key: "TOKEN", Value: "secret", Enabled: true, Sensitive: true}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err = service.UpdateWorker(ctx, RegisterWorkerInput{
		ID:                     worker.ID,
		Name:                   "Env",
		SupportedAgents:        []domain.AgentType{domain.AgentCodex},
		WorkDir:                "/tmp",
		ReplaceAgentRuntimeEnv: true,
		AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{
			AgentType: domain.AgentCodex,
			Vars:      []domain.AgentRuntimeEnvVar{{Key: "TOKEN", Enabled: true, Sensitive: true}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime := worker.EnabledRuntimeEnv(domain.AgentCodex); runtime[0].Value != "secret" {
		t.Fatalf("sensitive value was not preserved: %+v", worker.AgentRuntimeEnv)
	}
	settings, err := service.UpdateWorkerHeartbeatTimeout(ctx, "30s")
	if err != nil || settings.WorkerHeartbeat != "30s" {
		t.Fatalf("UpdateWorkerHeartbeatTimeout = %+v, %v", settings, err)
	}
}

func TestServiceDesignEdgeBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))

	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Other", GitURL: "git://other"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-edge", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", BindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if disabled, err := service.DisableWorker(ctx, worker.ID); err != nil || disabled.Status != domain.WorkerDisabled {
		t.Fatalf("DisableWorker = %+v, %v", disabled, err)
	}
	if workers, err := service.WorkersFiltered(ctx, WorkerFilter{IncludeDisabled: true, Status: domain.WorkerDisabled}); err != nil || len(workers) != 1 {
		t.Fatalf("disabled worker filter = %d, %v", len(workers), err)
	}
	if _, err := service.EnableWorker(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if workers, err := service.WorkersFiltered(ctx, WorkerFilter{ProjectID: otherProject.ID}); err != nil || len(workers) != 0 {
		t.Fatalf("wrong project worker filter = %d, %v", len(workers), err)
	}
	if _, err := service.UpdateWorkerProjectBindings(ctx, worker.ID, domain.WorkerSpecificProjects, []string{otherProject.ID}); err != nil {
		t.Fatal(err)
	}
	if workers, err := service.WorkersFiltered(ctx, WorkerFilter{ProjectID: otherProject.ID, AgentType: domain.AgentClaude}); err != nil || len(workers) != 0 {
		t.Fatalf("unsupported agent worker filter = %d, %v", len(workers), err)
	}
	if _, err := service.UpdateWorkerProjectBindings(ctx, worker.ID, domain.WorkerSpecificProjects, []string{"missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing binding project err = %v, want not found", err)
	}
	if _, err := service.UpdateWorkerProjectBindings(ctx, worker.ID, domain.WorkerSpecificProjects, []string{project.ID}); err != nil {
		t.Fatal(err)
	}

	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	blocker, err := service.CreateTask(ctx, CreateTaskInput{Title: "Blocker", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, blocker.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, blocker.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteWorker(ctx, worker.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("DeleteWorker occupied err = %v, want conflict", err)
	}
	if _, err := service.ArchiveProject(ctx, project.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ArchiveProject with unfinished task err = %v, want conflict", err)
	}
	if tasks, total, err := service.TasksFiltered(ctx, TaskFilter{Status: domain.TaskAssigned, WorkerID: worker.ID}, PageInput{Offset: 99}); err != nil || total != 1 || len(tasks) != 0 {
		t.Fatalf("TasksFiltered offset = len %d total %d err %v", len(tasks), total, err)
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskStarted(ctx, "started-edge", task.ID, "/tmp/worktree"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerConversationWithMetadata(ctx, "conv-edge", task.ID, "assistant", "hello", map[string]string{"tool": "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskResult(ctx, "result-edge", task.ID, "partial"); err != nil {
		t.Fatal(err)
	}
	if interrupted, err := service.ApplyWorkerTaskInterrupted(ctx, "interrupted-edge", task.ID, "stopped"); err != nil || interrupted.Status != domain.TaskInterrupted {
		t.Fatalf("ApplyWorkerTaskInterrupted = %+v, %v", interrupted, err)
	}
	if tasks, total, err := service.TasksFiltered(ctx, TaskFilter{IncludeArchived: true}, PageInput{}); err != nil || total == 0 || len(tasks) == 0 {
		t.Fatalf("TasksFiltered include archived = len %d total %d err %v", len(tasks), total, err)
	}
	if _, err := service.WorkerDisconnected(ctx, worker.ID); err != nil {
		t.Fatalf("WorkerDisconnected returned error: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := service.MarkStaleWorkersOffline(ctx, time.Minute); err != nil {
		t.Fatalf("MarkStaleWorkersOffline returned error: %v", err)
	}
	service.MonitorWorkerHeartbeats(ctx, 0, 0)
}

func TestServiceMonitorHeartbeatsRunsUntilCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))

	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-monitor", Name: "Monitor", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.MonitorWorkerHeartbeats(ctx, 500*time.Millisecond, time.Millisecond)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		loaded, err := service.Worker(ctx, worker.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == domain.WorkerOffline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker status = %s, want OFFLINE", loaded.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("MonitorWorkerHeartbeats did not stop after context cancellation")
	}

	canceled, stop := context.WithCancel(context.Background())
	stop()
	service.MonitorWorkerHeartbeats(canceled, time.Nanosecond, 0)
}

func TestServiceFilteringPaginationAndEventBranches(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Other", GitURL: "git://other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []CreateTaskInput{
		{Title: "A", ProjectID: project.ID, AgentType: domain.AgentCodex},
		{Title: "B", ProjectID: project.ID, AgentType: domain.AgentCodex},
		{Title: "C", ProjectID: otherProject.ID, AgentType: domain.AgentClaude},
	} {
		if _, err := service.CreateTask(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := service.TasksFiltered(ctx, TaskFilter{Status: domain.TaskCreated, ProjectID: project.ID, AgentType: domain.AgentCodex}, PageInput{Offset: -10, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(page) != 1 {
		t.Fatalf("paged tasks = len %d total %d, want len 1 total 2", len(page), total)
	}
	if _, total, err = service.TasksFiltered(ctx, TaskFilter{ProjectID: "missing"}, PageInput{}); err != nil || total != 0 {
		t.Fatalf("missing project filter total = %d, err = %v", total, err)
	}

	event := domain.DomainEvent{EventID: "evt", EventType: "TaskCreated", AggregateType: "Task", AggregateID: "task-1"}
	for _, filter := range []domain.EventFilter{
		{AggregateID: "task-2"},
		{AggregateType: "Worker"},
		{EventType: "WorkerConnected"},
	} {
		if eventMatchesFilter(event, filter) {
			t.Fatalf("eventMatchesFilter(%+v) = true, want false", filter)
		}
	}
	if !eventMatchesFilter(event, domain.EventFilter{AggregateID: "task-1", AggregateType: "Task", EventType: "TaskCreated"}) {
		t.Fatal("eventMatchesFilter should match exact filter")
	}
	if err := service.appendEvents(ctx, nil); err != nil {
		t.Fatalf("appendEvents(nil) returned error: %v", err)
	}

	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{})
	defer unsubscribe()
	_ = events
	burst := make([]domain.DomainEvent, 65)
	for i := range burst {
		burst[i] = domain.DomainEvent{EventID: "evt-burst", EventType: "TaskUpdated", AggregateType: "Task", AggregateID: "task-burst"}
	}
	service.publishEvents(burst)
}

func TestServicePaginatesProjectsWorkersAndEventsNewestFirst(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		current := now
		now = now.Add(time.Minute)
		return current
	}))

	firstProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "First", GitURL: "git://first"})
	if err != nil {
		t.Fatal(err)
	}
	secondProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Second", GitURL: "git://second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ArchiveProject(ctx, firstProject.ID); err != nil {
		t.Fatal(err)
	}

	projects, total, err := service.ProjectsFilteredPage(ctx, true, PageInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(projects) != 1 || projects[0].ID != secondProject.ID {
		t.Fatalf("paged projects = len %d total %d first %+v", len(projects), total, projects)
	}
	projects, total, err = service.ProjectsFilteredPage(ctx, false, PageInput{Offset: -10, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(projects) != 1 || projects[0].ID != secondProject.ID {
		t.Fatalf("active projects page = len %d total %d projects %+v", len(projects), total, projects)
	}
	projects, total, err = service.ProjectsFilteredPage(ctx, true, PageInput{Offset: 99, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(projects) != 0 {
		t.Fatalf("out-of-range projects page = len %d total %d", len(projects), total)
	}

	firstWorker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-first", Name: "First", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/first"})
	if err != nil {
		t.Fatal(err)
	}
	secondWorker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-second", Name: "Second", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DisableWorker(ctx, firstWorker.ID); err != nil {
		t.Fatal(err)
	}

	workers, workerTotal, err := service.WorkersFilteredPage(ctx, WorkerFilter{IncludeDisabled: true}, PageInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if workerTotal != 2 || len(workers) != 1 || workers[0].ID != secondWorker.ID {
		t.Fatalf("paged workers = len %d total %d workers %+v", len(workers), workerTotal, workers)
	}
	workers, workerTotal, err = service.WorkersFilteredPage(ctx, WorkerFilter{}, PageInput{Offset: 99, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if workerTotal != 1 || len(workers) != 0 {
		t.Fatalf("out-of-range workers page = len %d total %d", len(workers), workerTotal)
	}

	events, eventTotal, err := service.DomainEventsPage(ctx, domain.EventFilter{AggregateType: "Project"}, PageInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if eventTotal != 3 || len(events) != 1 || events[0].AggregateID != firstProject.ID || events[0].EventType != "ProjectArchived" {
		t.Fatalf("paged events = len %d total %d events %+v", len(events), eventTotal, events)
	}
	events, eventTotal, err = service.DomainEventsPage(ctx, domain.EventFilter{AggregateType: "Project"}, PageInput{Offset: 99, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if eventTotal != 3 || len(events) != 0 {
		t.Fatalf("out-of-range events page = len %d total %d", len(events), eventTotal)
	}
}

func TestServiceSearchesSortsAndPaginatesResources(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		current := now
		now = now.Add(time.Minute)
		return current
	}))

	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "Search Base", GitURL: "git://search-base"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []CreateTaskInput{
		{Title: "Alpha target", Description: "first target", ProjectID: project.ID, AgentType: domain.AgentCodex},
		{Title: "Beta target", Description: "second target", ProjectID: project.ID, AgentType: domain.AgentCodex},
		{Title: "Gamma target", Description: "third target", ProjectID: project.ID, AgentType: domain.AgentCodex},
	} {
		if _, err := service.CreateTask(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	tasks, taskTotal, err := service.TasksFilteredSorted(ctx, TaskFilter{Search: "target", IncludeArchived: true}, TaskSort{Field: TaskSortTitle, Direction: SortDirectionAsc}, PageInput{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if taskTotal != 3 || len(tasks) != 1 || tasks[0].Title != "Beta target" {
		t.Fatalf("searched task page = len %d total %d tasks %+v", len(tasks), taskTotal, tasks)
	}

	alphaProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Alpha Search Project", GitURL: "git://alpha"})
	if err != nil {
		t.Fatal(err)
	}
	gammaProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Gamma Search Project", GitURL: "git://gamma"})
	if err != nil {
		t.Fatal(err)
	}
	projects, projectTotal, err := service.ProjectsFilteredPageSorted(ctx, ProjectFilter{IncludeArchived: true, Search: "search project"}, ProjectSort{Field: ProjectSortName, Direction: SortDirectionDesc}, PageInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if projectTotal != 2 || len(projects) != 1 || projects[0].ID != gammaProject.ID {
		t.Fatalf("searched project page = len %d total %d projects %+v", len(projects), projectTotal, projects)
	}
	_ = alphaProject

	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-alpha-search", Name: "Alpha Runner", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/alpha-runner"}); err != nil {
		t.Fatal(err)
	}
	gammaWorker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-gamma-search", Name: "Gamma Runner", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp/gamma-runner"})
	if err != nil {
		t.Fatal(err)
	}
	workers, workerTotal, err := service.WorkersFilteredPageSorted(ctx, WorkerFilter{IncludeDisabled: true, Search: "runner"}, WorkerSort{Field: WorkerSortName, Direction: SortDirectionDesc}, PageInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if workerTotal != 2 || len(workers) != 1 || workers[0].ID != gammaWorker.ID {
		t.Fatalf("searched worker page = len %d total %d workers %+v", len(workers), workerTotal, workers)
	}

	firstEvent := domain.DomainEvent{
		EventID:          "evt-search-first",
		EventType:        "AlphaEvent",
		AggregateType:    "Task",
		AggregateID:      "task-alpha",
		AggregateVersion: 1,
		Payload:          []byte(`{"message":"search payload first"}`),
		OccurredAt:       now,
	}
	secondEvent := domain.DomainEvent{
		EventID:          "evt-search-second",
		EventType:        "GammaEvent",
		AggregateType:    "Task",
		AggregateID:      "task-gamma",
		AggregateVersion: 1,
		Payload:          []byte(`{"message":"search payload second"}`),
		OccurredAt:       now.Add(time.Minute),
	}
	if err := service.appendEvents(ctx, []domain.DomainEvent{firstEvent, secondEvent}); err != nil {
		t.Fatal(err)
	}
	events, eventTotal, err := service.DomainEventsPageSorted(ctx, domain.EventFilter{Search: "search payload"}, DomainEventSort{Field: DomainEventSortEventType, Direction: SortDirectionDesc}, PageInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if eventTotal != 2 || len(events) != 1 || events[0].EventID != secondEvent.EventID {
		t.Fatalf("searched event page = len %d total %d events %+v", len(events), eventTotal, events)
	}
}

func TestServiceSchedulingHelpers(t *testing.T) {
	worker := &domain.Worker{
		ID:                 "worker-helper",
		Status:             domain.WorkerOffline,
		SupportedAgents:    []domain.AgentType{domain.AgentCodex},
		ProjectBindingMode: domain.WorkerSpecificProjects,
		BoundProjectIDs:    []string{"project-1"},
	}
	if workerCanRunTask(worker, domain.AgentCodex, "project-1", "task-1") {
		t.Fatal("offline worker should not be able to run task")
	}
	worker.Status = domain.WorkerOnline
	if !workerCanRunTask(worker, domain.AgentCodex, "project-1", "task-1") {
		t.Fatal("online compatible worker should be able to run another task")
	}
	if workerAllowsProject(worker, "project-missing") {
		t.Fatal("specific-project worker should reject unbound project")
	}
	if !isTaskTerminal(domain.TaskArchived) {
		t.Fatal("archived task should be terminal")
	}
	if isTaskTerminal(domain.TaskRunning) {
		t.Fatal("running task should not be terminal")
	}
}
