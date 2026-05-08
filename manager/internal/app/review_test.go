package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceReviewPersistenceDefaultsAndValidation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedCompletedTaskWithSession(t, ctx, service)

	for _, tc := range []struct {
		name  string
		input AddTaskReviewCommentInput
	}{
		{name: "missing task", input: AddTaskReviewCommentInput{Path: "README.md", Body: "fix"}},
		{name: "missing path", input: AddTaskReviewCommentInput{TaskID: task.ID, Body: "fix"}},
		{name: "missing body", input: AddTaskReviewCommentInput{TaskID: task.ID, Path: "README.md"}},
		{name: "unknown task", input: AddTaskReviewCommentInput{TaskID: "missing", Path: "README.md", Body: "fix"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.AddTaskReviewComment(ctx, tc.input); err == nil {
				t.Fatal("AddTaskReviewComment should reject invalid input")
			}
		})
	}

	comment, err := service.AddTaskReviewComment(ctx, AddTaskReviewCommentInput{
		TaskID: task.ID,
		Path:   "README.md",
		Line:   12,
		Body:   "Please tighten this branch.",
	})
	if err != nil {
		t.Fatalf("AddTaskReviewComment returned error: %v", err)
	}
	if !strings.HasPrefix(comment.ID, "review_comment_") || comment.CreatedAt != now || comment.UpdatedAt != now {
		t.Fatalf("comment defaults = %+v", comment)
	}
	comments, err := service.Store().TaskReviewComments(ctx, task.ID)
	if err != nil || len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("stored comments = %+v, %v", comments, err)
	}

	run, err := service.SaveTaskReviewRun(ctx, domain.TaskReviewRun{TaskID: task.ID}, []domain.TaskReviewFinding{{
		Path:  "README.md",
		Line:  7,
		Title: "Validation bug",
		Body:  "The changed branch skips validation.",
	}})
	if err != nil {
		t.Fatalf("SaveTaskReviewRun returned error: %v", err)
	}
	if !strings.HasPrefix(run.ID, "review_run_") || run.Status != domain.TaskReviewRunQueued || run.Scope != domain.TaskGitDiffScopeUncommitted || run.CreatedAt != now || run.UpdatedAt != now {
		t.Fatalf("run defaults = %+v", run)
	}
	findings, err := service.Store().TaskReviewFindings(ctx, task.ID, "")
	if err != nil || len(findings) != 1 {
		t.Fatalf("stored findings = %+v, %v", findings, err)
	}
	finding := findings[0]
	if !strings.HasPrefix(finding.ID, "review_finding_") || finding.RunID != run.ID || finding.Status != domain.TaskReviewFindingOpen || finding.Severity != domain.TaskReviewSeverityMedium || finding.CreatedAt != now || finding.UpdatedAt != now {
		t.Fatalf("finding defaults = %+v", finding)
	}

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

	if _, err := service.UpdateTaskReviewFindingStatus(ctx, finding.ID, domain.TaskReviewFindingStatus("BAD")); err == nil {
		t.Fatal("UpdateTaskReviewFindingStatus should reject invalid status")
	}
	updated, err := service.UpdateTaskReviewFindingStatus(ctx, finding.ID, domain.TaskReviewFindingResolved)
	if err != nil {
		t.Fatalf("UpdateTaskReviewFindingStatus returned error: %v", err)
	}
	if updated.Status != domain.TaskReviewFindingResolved || updated.UpdatedAt != now {
		t.Fatalf("updated finding = %+v", updated)
	}
}

func TestServiceContinueTaskWithReviewFeedbackBuildsMessage(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedCompletedTaskWithSession(t, ctx, service)

	if _, _, err := service.ContinueTaskWithReviewFeedback(ctx, ContinueTaskWithReviewFeedbackInput{TaskID: task.ID}); err == nil {
		t.Fatal("ContinueTaskWithReviewFeedback should require message or selections")
	}

	run, err := service.SaveTaskReviewRun(ctx, domain.TaskReviewRun{TaskID: task.ID, Status: domain.TaskReviewRunCompleted}, []domain.TaskReviewFinding{{
		ID:         "finding-review-feedback",
		Path:       "README.md",
		Line:       3,
		Severity:   domain.TaskReviewSeverityHigh,
		Status:     domain.TaskReviewFindingOpen,
		Title:      "Validation bug",
		Body:       "The changed branch skips validation.",
		Suggestion: "Use the existing validator.",
	}})
	if err != nil {
		t.Fatalf("SaveTaskReviewRun returned error: %v", err)
	}
	if run.ID == "" {
		t.Fatal("SaveTaskReviewRun should assign an id")
	}
	comment, err := service.AddTaskReviewComment(ctx, AddTaskReviewCommentInput{
		TaskID: task.ID,
		Path:   "README.md",
		Line:   4,
		Body:   "Please tighten this.",
	})
	if err != nil {
		t.Fatalf("AddTaskReviewComment returned error: %v", err)
	}

	continued, payload, err := service.ContinueTaskWithReviewFeedback(ctx, ContinueTaskWithReviewFeedbackInput{
		TaskID:     task.ID,
		FindingIDs: []string{"finding-review-feedback"},
		CommentIDs: []string{comment.ID},
		Message:    "  Please address these review items.  ",
	})
	if err != nil {
		t.Fatalf("ContinueTaskWithReviewFeedback returned error: %v", err)
	}
	if continued.Status != domain.TaskStarting || payload.AgentSessionID != "session-1" || payload.WorktreePath != "/tmp/worktree" {
		t.Fatalf("continue result = task %+v payload %+v", continued, payload)
	}
	for _, want := range []string{
		"Please address these review items.",
		"Selected review findings:",
		"README.md:3 [HIGH] Validation bug - The changed branch skips validation. Suggestion: Use the existing validator.",
		"Selected inline comments:",
		"README.md:4 - Please tighten this.",
	} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("feedback message missing %q: %q", want, payload.Message)
		}
	}
	conversations, err := service.Store().TaskConversations(ctx, task.ID)
	if err != nil || len(conversations) == 0 || conversations[len(conversations)-1].Content != payload.Message {
		t.Fatalf("stored conversations = %+v, %v", conversations, err)
	}
}

func TestServiceReviewFeedbackRejectsMissingAndCrossTaskSelections(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	task := seedCompletedTaskWithSession(t, ctx, service)
	otherTask, err := service.CreateTask(ctx, CreateTaskInput{Title: "Other", ProjectID: task.ProjectID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	otherRun, err := service.SaveTaskReviewRun(ctx, domain.TaskReviewRun{ID: "run-other", TaskID: otherTask.ID}, []domain.TaskReviewFinding{{
		ID:     "finding-other",
		TaskID: otherTask.ID,
		Path:   "other.go",
		Title:  "Other finding",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if otherRun.ID != "run-other" {
		t.Fatalf("other run = %+v", otherRun)
	}
	otherComment, err := service.AddTaskReviewComment(ctx, AddTaskReviewCommentInput{TaskID: otherTask.ID, Path: "other.go", Body: "other comment"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.reviewFeedbackMessage(ctx, ContinueTaskWithReviewFeedbackInput{TaskID: task.ID, FindingIDs: []string{"missing-finding"}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing finding err = %v, want ErrNotFound", err)
	}
	if _, err := service.reviewFeedbackMessage(ctx, ContinueTaskWithReviewFeedbackInput{TaskID: task.ID, CommentIDs: []string{"missing-comment"}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing comment err = %v, want ErrNotFound", err)
	}
	if _, err := service.reviewFeedbackMessage(ctx, ContinueTaskWithReviewFeedbackInput{TaskID: task.ID, FindingIDs: []string{"finding-other"}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cross-task finding err = %v, want ErrConflict", err)
	}
	if _, err := service.reviewFeedbackMessage(ctx, ContinueTaskWithReviewFeedbackInput{TaskID: task.ID, CommentIDs: []string{otherComment.ID}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cross-task comment err = %v, want ErrConflict", err)
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
