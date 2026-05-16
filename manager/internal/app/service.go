package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/pkg/store"
)

type Clock func() time.Time

type Service struct {
	store         store.Store
	clock         Clock
	subscribersMu sync.RWMutex
	subscribers   map[chan domain.DomainEvent]domain.EventFilter
}

type Option func(*Service)

func WithClock(clock Clock) Option {
	return func(service *Service) {
		service.clock = clock
	}
}

func NewService(st store.Store, options ...Option) *Service {
	service := &Service{
		store:       st,
		clock:       func() time.Time { return time.Now().UTC() },
		subscribers: map[chan domain.DomainEvent]domain.EventFilter{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Store() store.Store {
	return s.store
}

func (s *Service) DomainEvents(ctx context.Context, filter domain.EventFilter) ([]domain.DomainEvent, error) {
	return s.store.DomainEvents(ctx, filter)
}

func (s *Service) DomainEventsPage(ctx context.Context, filter domain.EventFilter, page PageInput) ([]domain.DomainEvent, int, error) {
	return s.DomainEventsPageSorted(ctx, filter, DomainEventSort{}, page)
}

func (s *Service) DomainEventsSorted(ctx context.Context, filter domain.EventFilter, sort DomainEventSort) ([]domain.DomainEvent, error) {
	events, err := s.store.DomainEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	sortDomainEvents(events, sort)
	return events, nil
}

func (s *Service) DomainEventsPageSorted(ctx context.Context, filter domain.EventFilter, sort DomainEventSort, page PageInput) ([]domain.DomainEvent, int, error) {
	events, err := s.DomainEventsSorted(ctx, filter, sort)
	if err != nil {
		return nil, 0, err
	}
	pageItems, total := paginateItems(events, page)
	return pageItems, total, nil
}

func (s *Service) OutboxMessages(ctx context.Context, includePublished bool) ([]domain.OutboxMessage, error) {
	return s.store.OutboxMessages(ctx, includePublished)
}

func (s *Service) SubscribeDomainEvents(ctx context.Context, filter domain.EventFilter) (<-chan domain.DomainEvent, func()) {
	events := make(chan domain.DomainEvent, 64)
	s.subscribersMu.Lock()
	s.subscribers[events] = filter
	s.subscribersMu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.subscribersMu.Lock()
			delete(s.subscribers, events)
			close(events)
			s.subscribersMu.Unlock()
		})
	}
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			unsubscribe()
		}()
	}
	return events, unsubscribe
}

type CreateProjectInput struct {
	Name               string `json:"name"`
	GitURL             string `json:"gitUrl"`
	DefaultBranch      string `json:"defaultBranch"`
	WorktreeNamePrefix string `json:"worktreeNamePrefix"`
}

type UpdateProjectInput struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	GitURL             string `json:"gitUrl"`
	DefaultBranch      string `json:"defaultBranch"`
	WorktreeNamePrefix string `json:"worktreeNamePrefix"`
}

type ProjectFilter struct {
	IncludeArchived bool
	Search          string
}

func (s *Service) CreateProject(ctx context.Context, input CreateProjectInput) (*domain.Project, error) {
	now := s.clock()
	project, err := domain.NewProject(domain.NewProjectInput{
		ID:                 "project_" + uuid.NewString(),
		Name:               input.Name,
		GitURL:             input.GitURL,
		DefaultBranch:      input.DefaultBranch,
		WorktreeNamePrefix: input.WorktreeNamePrefix,
		Now:                now,
	})
	if err != nil {
		return nil, err
	}
	events := project.PullEvents()
	if err := s.store.SaveProject(ctx, project); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *Service) Project(ctx context.Context, id string) (*domain.Project, error) {
	return s.store.Project(ctx, id)
}

func (s *Service) Projects(ctx context.Context) ([]*domain.Project, error) {
	return s.store.Projects(ctx)
}

func (s *Service) ProjectsFiltered(ctx context.Context, includeArchived bool) ([]*domain.Project, error) {
	return s.ProjectsFilteredSorted(ctx, ProjectFilter{IncludeArchived: includeArchived}, ProjectSort{})
}

func (s *Service) ProjectsFilteredPage(ctx context.Context, includeArchived bool, page PageInput) ([]*domain.Project, int, error) {
	return s.ProjectsFilteredPageSorted(ctx, ProjectFilter{IncludeArchived: includeArchived}, ProjectSort{}, page)
}

func (s *Service) ProjectsFilteredSorted(ctx context.Context, filter ProjectFilter, sort ProjectSort) ([]*domain.Project, error) {
	projects, err := s.filteredProjects(ctx, filter)
	if err != nil {
		return nil, err
	}
	sortProjects(projects, sort)
	return projects, nil
}

func (s *Service) ProjectsFilteredPageSorted(ctx context.Context, filter ProjectFilter, sort ProjectSort, page PageInput) ([]*domain.Project, int, error) {
	projects, err := s.ProjectsFilteredSorted(ctx, filter, sort)
	if err != nil {
		return nil, 0, err
	}
	pageItems, total := paginateItems(projects, page)
	return pageItems, total, nil
}

func (s *Service) filteredProjects(ctx context.Context, filter ProjectFilter) ([]*domain.Project, error) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Project, 0, len(projects))
	for _, project := range projects {
		if !filter.IncludeArchived && project.Archived {
			continue
		}
		if !matchesSearch(filter.Search, project.ID, project.Name, project.GitURL, project.DefaultBranch, project.WorktreeNamePrefix) {
			continue
		}
		out = append(out, project)
	}
	return out, nil
}

func (s *Service) UpdateProject(ctx context.Context, input UpdateProjectInput) (*domain.Project, error) {
	project, err := s.store.Project(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	if err := project.Update(input.Name, input.GitURL, input.DefaultBranch, input.WorktreeNamePrefix, s.clock()); err != nil {
		return nil, err
	}
	events := project.PullEvents()
	if err := s.store.SaveProject(ctx, project); err != nil {
		return nil, err
	}
	return project, s.appendEvents(ctx, events)
}

func (s *Service) ArchiveProject(ctx context.Context, id string) (*domain.Project, error) {
	project, err := s.store.Project(ctx, id)
	if err != nil {
		return nil, err
	}
	tasks, err := s.store.Tasks(ctx)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if task.ProjectID == id && !isTaskTerminal(task.Status) {
			return nil, fmt.Errorf("%w: project %s has unfinished task %s", domain.ErrConflict, id, task.ID)
		}
	}
	project.Archive(s.clock())
	events := project.PullEvents()
	if err := s.store.SaveProject(ctx, project); err != nil {
		return nil, err
	}
	return project, s.appendEvents(ctx, events)
}

type CreateTaskInput struct {
	Title        string                       `json:"title"`
	Description  string                       `json:"description"`
	ProjectID    string                       `json:"projectId"`
	WorkerID     string                       `json:"workerId"`
	AgentType    domain.AgentType             `json:"agentType"`
	AgentConfig  *domain.AgentExecutionConfig `json:"agentConfig"`
	BaseBranch   string                       `json:"baseBranch"`
	PreCommands  []string                     `json:"preCommands"`
	PostCommands []string                     `json:"postCommands"`
	OwnerUserID  string                       `json:"ownerUserId"`
	StartDate    time.Time                    `json:"startDate"`
	EndDate      time.Time                    `json:"endDate"`
}

type UpdateTaskInput struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	ProjectID    string           `json:"projectId"`
	AgentType    domain.AgentType `json:"agentType"`
	BaseBranch   string           `json:"baseBranch"`
	PreCommands  []string         `json:"preCommands"`
	PostCommands []string         `json:"postCommands"`
	StartDate    time.Time        `json:"startDate"`
	EndDate      time.Time        `json:"endDate"`
}

type ContinueTaskInput struct {
	TaskID  string `json:"taskId"`
	Message string `json:"message"`
}

type TaskInteractionRequestInput struct {
	InteractionID  string                     `json:"interactionId"`
	TaskID         string                     `json:"taskId"`
	Kind           domain.TaskInteractionKind `json:"kind"`
	Title          string                     `json:"title"`
	Body           string                     `json:"body"`
	RawPayload     string                     `json:"rawPayload"`
	AgentSessionID string                     `json:"agentSessionId"`
}

type RespondTaskInteractionInput struct {
	InteractionID string                         `json:"interactionId"`
	Decision      domain.TaskInteractionDecision `json:"decision"`
	Message       string                         `json:"message"`
	Payload       string                         `json:"payload"`
}

type TaskInteractionResolvedInput struct {
	InteractionID string                         `json:"interactionId"`
	TaskID        string                         `json:"taskId"`
	Responded     bool                           `json:"responded"`
	Decision      domain.TaskInteractionDecision `json:"decision"`
	Message       string                         `json:"message"`
	Payload       string                         `json:"payload"`
}

type TaskFilter struct {
	Status          domain.TaskStatus
	ProjectID       string
	WorkerID        string
	AgentType       domain.AgentType
	OwnerUserID     string
	IncludeArchived bool
	Search          string
}

type PageInput struct {
	Offset int
	Limit  int
}

type SortDirection string

const (
	SortDirectionAsc  SortDirection = "ASC"
	SortDirectionDesc SortDirection = "DESC"
)

type TaskSortField string

const (
	TaskSortCreatedAt TaskSortField = "CREATED_AT"
	TaskSortUpdatedAt TaskSortField = "UPDATED_AT"
	TaskSortTitle     TaskSortField = "TITLE"
	TaskSortStatus    TaskSortField = "STATUS"
	TaskSortStartDate TaskSortField = "START_DATE"
	TaskSortEndDate   TaskSortField = "END_DATE"
)

type TaskSort struct {
	Field     TaskSortField
	Direction SortDirection
}

type ProjectSortField string

const (
	ProjectSortCreatedAt     ProjectSortField = "CREATED_AT"
	ProjectSortUpdatedAt     ProjectSortField = "UPDATED_AT"
	ProjectSortName          ProjectSortField = "NAME"
	ProjectSortGitURL        ProjectSortField = "GIT_URL"
	ProjectSortDefaultBranch ProjectSortField = "DEFAULT_BRANCH"
)

type ProjectSort struct {
	Field     ProjectSortField
	Direction SortDirection
}

type WorkerSortField string

const (
	WorkerSortCreatedAt       WorkerSortField = "CREATED_AT"
	WorkerSortUpdatedAt       WorkerSortField = "UPDATED_AT"
	WorkerSortName            WorkerSortField = "NAME"
	WorkerSortStatus          WorkerSortField = "STATUS"
	WorkerSortLastHeartbeatAt WorkerSortField = "LAST_HEARTBEAT_AT"
)

type WorkerSort struct {
	Field     WorkerSortField
	Direction SortDirection
}

type DomainEventSortField string

const (
	DomainEventSortOccurredAt    DomainEventSortField = "OCCURRED_AT"
	DomainEventSortEventType     DomainEventSortField = "EVENT_TYPE"
	DomainEventSortAggregateType DomainEventSortField = "AGGREGATE_TYPE"
	DomainEventSortAggregateID   DomainEventSortField = "AGGREGATE_ID"
)

type DomainEventSort struct {
	Field     DomainEventSortField
	Direction SortDirection
}

func paginateItems[T any](items []T, page PageInput) ([]T, int) {
	total := len(items)
	start := page.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := total
	if page.Limit > 0 && start+page.Limit < end {
		end = start + page.Limit
	}
	return items[start:end], total
}

func matchesSearch(search string, fields ...string) bool {
	query := strings.ToLower(strings.TrimSpace(search))
	if query == "" {
		return true
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func compareStrings(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

func compareTimes(a, b time.Time) int {
	return a.Compare(b)
}

func compareOptionalTimes(a, b *time.Time) int {
	return compareTimes(valueTime(a), valueTime(b))
}

func valueTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func applyDirection(cmp int, direction SortDirection) int {
	if cmp == 0 {
		return 0
	}
	if direction == SortDirectionAsc {
		return cmp
	}
	return -cmp
}

func sortTasks(tasks []*domain.Task, sort TaskSort) {
	field := sort.Field
	if field == "" {
		field = TaskSortCreatedAt
	}
	slices.SortFunc(tasks, func(a, b *domain.Task) int {
		var cmp int
		switch field {
		case TaskSortUpdatedAt:
			cmp = compareTimes(a.UpdatedAt, b.UpdatedAt)
		case TaskSortTitle:
			cmp = compareStrings(a.Title, b.Title)
		case TaskSortStatus:
			cmp = compareStrings(string(a.Status), string(b.Status))
		case TaskSortStartDate:
			cmp = compareTimes(a.StartDate, b.StartDate)
		case TaskSortEndDate:
			cmp = compareTimes(a.EndDate, b.EndDate)
		default:
			cmp = compareTimes(a.CreatedAt, b.CreatedAt)
		}
		if cmp != 0 {
			return applyDirection(cmp, sort.Direction)
		}
		return compareTaskFallback(a, b)
	})
}

func compareTaskFallback(a, b *domain.Task) int {
	if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
		return cmp
	}
	return strings.Compare(b.ID, a.ID)
}

func sortProjects(projects []*domain.Project, sort ProjectSort) {
	field := sort.Field
	if field == "" {
		field = ProjectSortCreatedAt
	}
	slices.SortFunc(projects, func(a, b *domain.Project) int {
		var cmp int
		switch field {
		case ProjectSortUpdatedAt:
			cmp = compareTimes(a.UpdatedAt, b.UpdatedAt)
		case ProjectSortName:
			cmp = compareStrings(a.Name, b.Name)
		case ProjectSortGitURL:
			cmp = compareStrings(a.GitURL, b.GitURL)
		case ProjectSortDefaultBranch:
			cmp = compareStrings(a.DefaultBranch, b.DefaultBranch)
		default:
			cmp = compareTimes(a.CreatedAt, b.CreatedAt)
		}
		if cmp != 0 {
			return applyDirection(cmp, sort.Direction)
		}
		return compareProjectFallback(a, b)
	})
}

func compareProjectFallback(a, b *domain.Project) int {
	if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
		return cmp
	}
	return strings.Compare(b.ID, a.ID)
}

func sortWorkers(workers []*domain.Worker, sort WorkerSort) {
	field := sort.Field
	if field == "" {
		field = WorkerSortCreatedAt
	}
	slices.SortFunc(workers, func(a, b *domain.Worker) int {
		var cmp int
		switch field {
		case WorkerSortUpdatedAt:
			cmp = compareTimes(a.UpdatedAt, b.UpdatedAt)
		case WorkerSortName:
			cmp = compareStrings(a.Name, b.Name)
		case WorkerSortStatus:
			cmp = compareStrings(string(a.Status), string(b.Status))
		case WorkerSortLastHeartbeatAt:
			cmp = compareOptionalTimes(a.LastHeartbeatAt, b.LastHeartbeatAt)
		default:
			cmp = compareTimes(a.CreatedAt, b.CreatedAt)
		}
		if cmp != 0 {
			return applyDirection(cmp, sort.Direction)
		}
		return compareWorkerFallback(a, b)
	})
}

func compareWorkerFallback(a, b *domain.Worker) int {
	if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
		return cmp
	}
	return strings.Compare(b.ID, a.ID)
}

func sortDomainEvents(events []domain.DomainEvent, sort DomainEventSort) {
	field := sort.Field
	if field == "" {
		field = DomainEventSortOccurredAt
	}
	slices.SortFunc(events, func(a, b domain.DomainEvent) int {
		var cmp int
		switch field {
		case DomainEventSortEventType:
			cmp = compareStrings(a.EventType, b.EventType)
		case DomainEventSortAggregateType:
			cmp = compareStrings(a.AggregateType, b.AggregateType)
		case DomainEventSortAggregateID:
			cmp = compareStrings(a.AggregateID, b.AggregateID)
		default:
			cmp = compareTimes(a.OccurredAt, b.OccurredAt)
		}
		if cmp != 0 {
			return applyDirection(cmp, sort.Direction)
		}
		return compareDomainEventFallback(a, b)
	})
}

func compareDomainEventFallback(a, b domain.DomainEvent) int {
	if cmp := b.OccurredAt.Compare(a.OccurredAt); cmp != 0 {
		return cmp
	}
	return strings.Compare(b.EventID, a.EventID)
}

func domainAgentStrings(agents []domain.AgentType) []string {
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		out = append(out, string(agent))
	}
	return out
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (*domain.Task, error) {
	now := s.clock()
	project, err := s.store.Project(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	if project.Archived {
		return nil, fmt.Errorf("%w: project %s is archived", domain.ErrConflict, project.ID)
	}
	if input.WorkerID != "" && !input.AgentType.Valid() {
		return nil, fmt.Errorf("%w: agent type is required when creating task with worker %s", domain.ErrConflict, input.WorkerID)
	}
	if input.WorkerID == "" && input.AgentConfig != nil && !input.AgentConfig.Empty() {
		return nil, fmt.Errorf("%w: agent config requires worker assignment", domain.ErrConflict)
	}
	task, err := domain.NewTask(domain.NewTaskInput{
		ID:           "task_" + uuid.NewString(),
		Title:        input.Title,
		Description:  input.Description,
		ProjectID:    input.ProjectID,
		AgentType:    input.AgentType,
		BaseBranch:   input.BaseBranch,
		PreCommands:  input.PreCommands,
		PostCommands: input.PostCommands,
		OwnerUserID:  input.OwnerUserID,
		StartDate:    input.StartDate,
		EndDate:      input.EndDate,
		Now:          now,
	})
	if err != nil {
		return nil, err
	}
	if input.WorkerID != "" {
		worker, err := s.store.Worker(ctx, input.WorkerID)
		if err != nil {
			return nil, err
		}
		if !workerCanRunTask(worker, input.AgentType, input.ProjectID, task.ID) {
			return nil, fmt.Errorf("%w: worker %s cannot accept task %s", domain.ErrConflict, input.WorkerID, task.ID)
		}
		if err := task.AssignWorkerWithAgentConfig(input.WorkerID, input.AgentType, input.AgentConfig, now); err != nil {
			return nil, err
		}
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) Tasks(ctx context.Context) ([]*domain.Task, error) {
	return s.store.Tasks(ctx)
}

func (s *Service) TasksFiltered(ctx context.Context, filter TaskFilter, page PageInput) ([]*domain.Task, int, error) {
	return s.TasksFilteredSorted(ctx, filter, TaskSort{}, page)
}

func (s *Service) TasksFilteredSorted(ctx context.Context, filter TaskFilter, sort TaskSort, page PageInput) ([]*domain.Task, int, error) {
	tasks, err := s.store.Tasks(ctx)
	if err != nil {
		return nil, 0, err
	}
	filtered := make([]*domain.Task, 0, len(tasks))
	for _, task := range tasks {
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		if filter.ProjectID != "" && task.ProjectID != filter.ProjectID {
			continue
		}
		if filter.WorkerID != "" && task.WorkerID != filter.WorkerID {
			continue
		}
		if filter.AgentType != "" && task.AgentType != filter.AgentType {
			continue
		}
		if filter.OwnerUserID != "" && task.OwnerUserID != filter.OwnerUserID {
			continue
		}
		if !filter.IncludeArchived && task.Status == domain.TaskArchived {
			continue
		}
		if !matchesSearch(
			filter.Search,
			task.ID,
			task.Title,
			task.Description,
			string(task.Status),
			task.ProjectID,
			task.WorkerID,
			string(task.AgentType),
			task.BaseBranch,
			task.WorktreePath,
			task.AgentSessionID,
			task.Result,
		) {
			continue
		}
		filtered = append(filtered, task)
	}
	sortTasks(filtered, sort)
	pageItems, total := paginateItems(filtered, page)
	return pageItems, total, nil
}

func (s *Service) Task(ctx context.Context, id string) (*domain.Task, error) {
	return s.store.Task(ctx, id)
}

func (s *Service) UpdateTask(ctx context.Context, input UpdateTaskInput) (*domain.Task, error) {
	now := s.clock()
	task, err := s.store.Task(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	project, err := s.store.Project(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	if project.Archived {
		return nil, fmt.Errorf("%w: project %s is archived", domain.ErrConflict, project.ID)
	}
	if task.WorkerID != "" {
		worker, err := s.store.Worker(ctx, task.WorkerID)
		if err != nil {
			return nil, err
		}
		if !workerCanRunTask(worker, input.AgentType, input.ProjectID, task.ID) {
			return nil, fmt.Errorf("%w: worker %s cannot run updated task %s", domain.ErrConflict, worker.ID, task.ID)
		}
	}
	if err := task.Update(domain.NewTaskInput{
		ID:           task.ID,
		Title:        input.Title,
		Description:  input.Description,
		ProjectID:    input.ProjectID,
		AgentType:    input.AgentType,
		BaseBranch:   input.BaseBranch,
		PreCommands:  input.PreCommands,
		PostCommands: input.PostCommands,
		StartDate:    input.StartDate,
		EndDate:      input.EndDate,
		Now:          now,
	}); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ArchiveTask(ctx context.Context, taskID string) (*domain.Task, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.Archive(s.clock()); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) DeleteTask(ctx context.Context, taskID string) error {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status != domain.TaskArchived {
		return fmt.Errorf("%w: task %s is not archived", domain.ErrConflict, taskID)
	}
	if err := task.Delete(s.clock()); err != nil {
		return err
	}
	events := task.PullEvents()
	if err := s.store.DeleteTask(ctx, taskID); err != nil {
		return err
	}
	return s.appendEvents(ctx, events)
}

func (s *Service) RetryTask(ctx context.Context, taskID string) (*domain.Task, error) {
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	previousWorkerID := task.WorkerID
	if err := task.Retry(now); err != nil {
		return nil, err
	}
	if previousWorkerID != "" {
		if err := s.releaseWorkerIDFromTask(ctx, previousWorkerID, taskID, now); err != nil {
			return nil, err
		}
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

type RegisterWorkerInput struct {
	ID                     string                          `json:"id"`
	Name                   string                          `json:"name"`
	SupportedAgents        []domain.AgentType              `json:"supportedAgents"`
	WorkDir                string                          `json:"workDir"`
	StartupCommand         string                          `json:"startupCommand"`
	BindingMode            domain.WorkerProjectBindingMode `json:"projectBindingMode"`
	BoundProjectIDs        []string                        `json:"boundProjectIds"`
	AgentRuntimeEnv        []domain.WorkerAgentRuntimeEnv  `json:"agentRuntimeEnv"`
	ReplaceAgentRuntimeEnv bool                            `json:"-"`
	Capabilities           map[string]string               `json:"capabilities"`
}

func (s *Service) RegisterWorker(ctx context.Context, input RegisterWorkerInput) (*domain.Worker, error) {
	id := input.ID
	if id == "" {
		id = "worker_" + uuid.NewString()
	}
	if existing, err := s.store.Worker(ctx, id); err == nil {
		if input.Name != "" && input.WorkDir != "" && len(input.SupportedAgents) > 0 {
			if err := s.ensureWorkerNameUnique(ctx, input.Name, existing.ID); err != nil {
				return nil, err
			}
			capabilities := input.Capabilities
			if capabilities == nil {
				capabilities = existing.Capabilities
			}
			if err := existing.Update(domain.NewWorkerInput{
				ID:                     existing.ID,
				Name:                   input.Name,
				SupportedAgents:        input.SupportedAgents,
				WorkDir:                input.WorkDir,
				StartupCommand:         input.StartupCommand,
				ProjectBindingMode:     input.BindingMode,
				BoundProjectIDs:        input.BoundProjectIDs,
				AgentRuntimeEnv:        input.AgentRuntimeEnv,
				ReplaceAgentRuntimeEnv: input.ReplaceAgentRuntimeEnv,
				Capabilities:           capabilities,
				Now:                    s.clock(),
			}); err != nil {
				return nil, err
			}
			events := existing.PullEvents()
			if err := s.store.SaveWorker(ctx, existing); err != nil {
				return nil, err
			}
			if err := s.appendEvents(ctx, events); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	if err := s.ensureWorkerNameUnique(ctx, input.Name, id); err != nil {
		return nil, err
	}
	worker, err := domain.NewWorker(domain.NewWorkerInput{
		ID:                 id,
		Name:               input.Name,
		SupportedAgents:    input.SupportedAgents,
		WorkDir:            input.WorkDir,
		StartupCommand:     input.StartupCommand,
		ProjectBindingMode: input.BindingMode,
		BoundProjectIDs:    input.BoundProjectIDs,
		AgentRuntimeEnv:    input.AgentRuntimeEnv,
		Capabilities:       input.Capabilities,
		Now:                s.clock(),
	})
	if err != nil {
		return nil, err
	}
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, err
	}
	return worker, nil
}

func (s *Service) Workers(ctx context.Context) ([]*domain.Worker, error) {
	return s.store.Workers(ctx)
}

type WorkerFilter struct {
	Status          domain.WorkerStatus
	ProjectID       string
	AgentType       domain.AgentType
	IncludeDisabled bool
	Search          string
}

func (s *Service) Worker(ctx context.Context, id string) (*domain.Worker, error) {
	return s.store.Worker(ctx, id)
}

func (s *Service) WorkersFiltered(ctx context.Context, filter WorkerFilter) ([]*domain.Worker, error) {
	return s.WorkersFilteredSorted(ctx, filter, WorkerSort{})
}

func (s *Service) WorkersFilteredPage(ctx context.Context, filter WorkerFilter, page PageInput) ([]*domain.Worker, int, error) {
	return s.WorkersFilteredPageSorted(ctx, filter, WorkerSort{}, page)
}

func (s *Service) WorkersFilteredSorted(ctx context.Context, filter WorkerFilter, sort WorkerSort) ([]*domain.Worker, error) {
	workers, err := s.filteredWorkers(ctx, filter)
	if err != nil {
		return nil, err
	}
	sortWorkers(workers, sort)
	return workers, nil
}

func (s *Service) WorkersFilteredPageSorted(ctx context.Context, filter WorkerFilter, sort WorkerSort, page PageInput) ([]*domain.Worker, int, error) {
	workers, err := s.WorkersFilteredSorted(ctx, filter, sort)
	if err != nil {
		return nil, 0, err
	}
	pageItems, total := paginateItems(workers, page)
	return pageItems, total, nil
}

func (s *Service) filteredWorkers(ctx context.Context, filter WorkerFilter) ([]*domain.Worker, error) {
	workers, err := s.store.Workers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Worker, 0, len(workers))
	for _, worker := range workers {
		if filter.Status != "" && worker.Status != filter.Status {
			continue
		}
		if !filter.IncludeDisabled && worker.Status == domain.WorkerDisabled {
			continue
		}
		if filter.AgentType != "" && !workerSupportsAgent(worker, filter.AgentType) {
			continue
		}
		if filter.ProjectID != "" && !workerAllowsProject(worker, filter.ProjectID) {
			continue
		}
		workerFields := []string{
			worker.ID,
			worker.Name,
			string(worker.Status),
			worker.WorkDir,
			worker.StartupCommand,
			string(worker.ProjectBindingMode),
		}
		workerFields = append(workerFields, domainAgentStrings(worker.SupportedAgents)...)
		workerFields = append(workerFields, worker.BoundProjectIDs...)
		workerFields = append(workerFields, worker.CurrentTaskIDs...)
		if !matchesSearch(filter.Search, workerFields...) {
			continue
		}
		out = append(out, worker)
	}
	return out, nil
}

func (s *Service) UpdateWorker(ctx context.Context, input RegisterWorkerInput) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureWorkerNameUnique(ctx, input.Name, worker.ID); err != nil {
		return nil, err
	}
	capabilities := input.Capabilities
	if capabilities == nil {
		capabilities = worker.Capabilities
	}
	if err := worker.Update(domain.NewWorkerInput{
		ID:                     worker.ID,
		Name:                   input.Name,
		SupportedAgents:        input.SupportedAgents,
		WorkDir:                input.WorkDir,
		StartupCommand:         input.StartupCommand,
		ProjectBindingMode:     input.BindingMode,
		BoundProjectIDs:        input.BoundProjectIDs,
		AgentRuntimeEnv:        input.AgentRuntimeEnv,
		ReplaceAgentRuntimeEnv: input.ReplaceAgentRuntimeEnv,
		Capabilities:           capabilities,
		Now:                    s.clock(),
	}); err != nil {
		return nil, err
	}
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	return worker, s.appendEvents(ctx, events)
}

func (s *Service) ensureWorkerNameUnique(ctx context.Context, name, exceptWorkerID string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	workers, err := s.store.Workers(ctx)
	if err != nil {
		return err
	}
	for _, worker := range workers {
		if worker.ID != exceptWorkerID && worker.Name == name {
			return fmt.Errorf("%w: worker name %q already exists", domain.ErrConflict, name)
		}
	}
	return nil
}

func (s *Service) UpdateWorkerProjectBindings(ctx context.Context, workerID string, mode domain.WorkerProjectBindingMode, projectIDs []string) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	if mode == domain.WorkerAllProjects || mode == "" {
		worker.ShareAcrossAllProjects(now)
	} else {
		for _, projectID := range projectIDs {
			if _, err := s.store.Project(ctx, projectID); err != nil {
				return nil, err
			}
		}
		worker.BindProjects(projectIDs, now)
	}
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	return worker, s.appendEvents(ctx, events)
}

func (s *Service) EnableWorker(ctx context.Context, workerID string) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	worker.Enable(s.clock())
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	return worker, s.appendEvents(ctx, events)
}

func (s *Service) DisableWorker(ctx context.Context, workerID string) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	worker.Disable(s.clock())
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	return worker, s.appendEvents(ctx, events)
}

func (s *Service) DeleteWorker(ctx context.Context, workerID string) error {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return err
	}
	if len(worker.CurrentTaskIDs) > 0 {
		return fmt.Errorf("%w: worker %s has current tasks %v", domain.ErrConflict, workerID, worker.CurrentTaskIDs)
	}
	worker.Delete(s.clock())
	events := worker.PullEvents()
	if err := s.store.DeleteWorker(ctx, workerID); err != nil {
		return err
	}
	return s.appendEvents(ctx, events)
}

func (s *Service) WorkerConnected(ctx context.Context, workerID string) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	worker.Connect(s.clock())
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	return worker, s.appendEvents(ctx, events)
}

func (s *Service) WorkerHeartbeat(ctx context.Context, workerID string) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	worker.Heartbeat(s.clock())
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	return worker, s.appendEvents(ctx, events)
}

func (s *Service) WorkerDisconnected(ctx context.Context, workerID string) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	worker.MarkOffline(s.clock())
	events := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	return worker, s.appendEvents(ctx, events)
}

func (s *Service) MarkStaleWorkersOffline(ctx context.Context, timeout time.Duration) error {
	return s.markWorkersLost(ctx, timeout)
}

func (s *Service) markWorkersLost(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	workers, err := s.store.Workers(ctx)
	if err != nil {
		return err
	}
	cutoff := s.clock().Add(-timeout)
	for _, worker := range workers {
		online := worker.Status == domain.WorkerOnline
		hasTasks := len(worker.CurrentTaskIDs) > 0
		stale := worker.LastHeartbeatAt == nil || !worker.LastHeartbeatAt.After(cutoff)
		if online && !stale {
			continue
		}
		if !online && !hasTasks {
			continue
		}
		if online && !hasTasks && stale {
			worker.MarkOffline(s.clock())
			events := worker.PullEvents()
			if err := s.store.SaveWorker(ctx, worker); err != nil {
				return err
			}
			if err := s.appendEvents(ctx, events); err != nil {
				return err
			}
			continue
		}
		if _, err := s.MarkWorkerLost(ctx, worker.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) MarkWorkerLost(ctx context.Context, workerID string) (*domain.Worker, error) {
	now := s.clock()
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	if worker.Status == domain.WorkerOnline {
		worker.MarkOffline(now)
	}
	taskIDs := append([]string(nil), worker.CurrentTaskIDs...)
	events := worker.PullEvents()
	for _, taskID := range taskIDs {
		task, err := s.store.Task(ctx, taskID)
		if err != nil {
			continue
		}
		if isTaskTerminal(task.Status) || task.Status == domain.TaskCreated || task.Status == domain.TaskAssigned {
			if task.Status == domain.TaskAssigned {
				worker.ReleaseTask(taskID, now)
				events = append(events, worker.PullEvents()...)
			}
			continue
		}
		if err := task.Fail("worker_lost: "+workerID, now); err != nil {
			continue
		}
		if err := s.store.CancelPendingTaskInteractions(ctx, task.ID, now); err != nil {
			return nil, err
		}
		worker.ReleaseTask(taskID, now)
		taskEvents := task.PullEvents()
		workerEvents := worker.PullEvents()
		if err := s.store.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		events = append(events, taskEvents...)
		events = append(events, workerEvents...)
	}
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, err
	}
	return worker, nil
}

func (s *Service) MonitorWorkerHeartbeats(ctx context.Context, timeout, interval time.Duration) {
	if timeout <= 0 {
		return
	}
	if interval <= 0 {
		interval = timeout / 3
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.markWorkersLost(ctx, timeout)
		}
	}
}

func (s *Service) AssignWorker(ctx context.Context, taskID, workerID string, agentTypes ...domain.AgentType) (*domain.Task, error) {
	return s.AssignWorkerWithConfig(ctx, taskID, workerID, nil, agentTypes...)
}

func (s *Service) AssignWorkerWithConfig(ctx context.Context, taskID, workerID string, config *domain.AgentExecutionConfig, agentTypes ...domain.AgentType) (*domain.Task, error) {
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	agentType := task.AgentType
	if len(agentTypes) > 0 {
		agentType = agentTypes[0]
	}
	if task.AgentType != "" && len(agentTypes) > 0 && agentTypes[0] != "" && agentTypes[0] != task.AgentType {
		return nil, fmt.Errorf("%w: task %s already uses agent %s", domain.ErrConflict, taskID, task.AgentType)
	}
	if task.AgentType == "" && !agentType.Valid() {
		return nil, fmt.Errorf("%w: agent type is required to assign task %s", domain.ErrConflict, taskID)
	}
	if !workerCanRunTask(worker, agentType, task.ProjectID, taskID) {
		return nil, fmt.Errorf("%w: worker %s cannot accept task %s", domain.ErrConflict, workerID, taskID)
	}
	if err := task.AssignWorkerWithAgentConfig(workerID, agentType, config, now); err != nil {
		return nil, err
	}
	taskEvents := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, taskEvents); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) StartTask(ctx context.Context, taskID string) (*domain.Task, protocol.TaskStartPayload, error) {
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	if task.AgentType == "" {
		return nil, protocol.TaskStartPayload{}, fmt.Errorf("%w: task %s requires agent before start", domain.ErrConflict, task.ID)
	}
	if task.WorkerID == "" && task.Status == domain.TaskCreated {
		task, err = s.assignFirstAvailableWorker(ctx, task, now)
		if err != nil {
			return nil, protocol.TaskStartPayload{}, err
		}
	}
	worker, err := s.store.Worker(ctx, task.WorkerID)
	if err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	if !workerCanRunTask(worker, task.AgentType, task.ProjectID, task.ID) {
		return nil, protocol.TaskStartPayload{}, fmt.Errorf("%w: worker %s is not ready for task", domain.ErrConflict, worker.ID)
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	if err := task.Start(now); err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	if err := worker.AssignTask(task.ID, now); err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	events := task.PullEvents()
	events = append(events, worker.PullEvents()...)
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	return task, buildStartPayload(task, project, worker), nil
}

func (s *Service) ContinueTask(ctx context.Context, input ContinueTaskInput) (*domain.Task, protocol.TaskContinuePayload, error) {
	now := s.clock()
	if strings.TrimSpace(input.Message) == "" {
		return nil, protocol.TaskContinuePayload{}, fmt.Errorf("message is required")
	}
	task, err := s.store.Task(ctx, input.TaskID)
	if err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if err := validateTaskCanContinue(task); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	worker, err := s.store.Worker(ctx, task.WorkerID)
	if err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if !workerCanRunTask(worker, task.AgentType, task.ProjectID, task.ID) {
		return nil, protocol.TaskContinuePayload{}, fmt.Errorf("%w: worker %s is not ready to continue task %s", domain.ErrConflict, worker.ID, task.ID)
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if err := task.AppendUserConversation(input.Message, now); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if err := s.store.AppendConversation(ctx, domain.ConversationMessage{ID: "msg_" + uuid.NewString(), TaskID: task.ID, Role: "user", Content: input.Message, CreatedAt: now}); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	events := task.PullEvents()
	if err := task.Continue(now); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if err := task.QueueContinueDirective("dir_"+uuid.NewString(), input.Message, now); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if err := worker.AssignTask(task.ID, now); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	events = append(events, task.PullEvents()...)
	events = append(events, worker.PullEvents()...)
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	return task, buildContinuePayload(task, project, worker, input.Message), nil
}

func (s *Service) InterruptTask(ctx context.Context, taskID string) (*domain.Task, string, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, "", err
	}
	if err := task.RequestInterrupt(s.clock()); err != nil {
		return nil, "", err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, "", err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, "", err
	}
	return task, task.WorkerID, nil
}

func (s *Service) assignFirstAvailableWorker(ctx context.Context, task *domain.Task, now time.Time) (*domain.Task, error) {
	workers, err := s.store.Workers(ctx)
	if err != nil {
		return nil, err
	}
	var selected *domain.Worker
	for _, worker := range workers {
		if worker.CanAcceptTask(task.AgentType, task.ProjectID) {
			selected = worker
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("%w: no available worker for task %s", domain.ErrConflict, task.ID)
	}
	if err := task.AssignWorker(selected.ID, now); err != nil {
		return nil, err
	}
	taskEvents := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, taskEvents); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) ApplyWorkerTaskAccepted(ctx context.Context, messageID, taskID string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	return s.store.Task(ctx, taskID)
}

func (s *Service) ApplyWorkerTaskStarted(ctx context.Context, messageID, taskID, worktreePath string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.MarkRunning(worktreePath, s.clock()); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerTaskLog(ctx context.Context, messageID, taskID, stream, content string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.AppendLog(stream, content, now); err != nil {
		return nil, err
	}
	if err := s.store.AppendTaskLog(ctx, domain.TaskLog{ID: "log_" + uuid.NewString(), TaskID: taskID, Stream: stream, Content: content, CreatedAt: now}); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerConversation(ctx context.Context, messageID, taskID, role, content string) (*domain.Task, error) {
	return s.ApplyWorkerConversationWithMetadata(ctx, messageID, taskID, role, content, nil)
}

func (s *Service) ApplyWorkerConversationWithMetadata(ctx context.Context, messageID, taskID, role, content string, metadata map[string]string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.AppendConversation(role, content, now); err != nil {
		return nil, err
	}
	task.RememberAgentSession(metadata["agentSessionId"], now)
	if err := s.store.AppendConversation(ctx, domain.ConversationMessage{ID: "msg_" + uuid.NewString(), TaskID: taskID, Role: role, Content: content, Metadata: cloneStringMap(metadata), CreatedAt: now}); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerWaitingInput(ctx context.Context, messageID, taskID, content string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.WaitForInput(now); err != nil {
		return nil, err
	}
	if content != "" {
		if err := s.store.AppendTaskLog(ctx, domain.TaskLog{ID: "log_" + uuid.NewString(), TaskID: taskID, Stream: "system", Content: content, CreatedAt: now}); err != nil {
			return nil, err
		}
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerTaskInteractionRequest(ctx context.Context, messageID string, input TaskInteractionRequestInput) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, input.TaskID)
	}
	now := s.clock()
	if strings.TrimSpace(input.InteractionID) == "" {
		input.InteractionID = messageID
	}
	if strings.TrimSpace(input.TaskID) == "" {
		return nil, fmt.Errorf("task id is required")
	}
	if !input.Kind.Valid() {
		return nil, fmt.Errorf("unsupported task interaction kind %q", input.Kind)
	}
	if existing, err := s.store.TaskInteraction(ctx, input.InteractionID); err == nil {
		return s.store.Task(ctx, existing.TaskID)
	}
	task, err := s.store.Task(ctx, input.TaskID)
	if err != nil {
		return nil, err
	}
	interaction, err := domain.NewTaskInteraction(domain.TaskInteraction{
		ID:             input.InteractionID,
		TaskID:         task.ID,
		Kind:           input.Kind,
		Status:         domain.TaskInteractionPending,
		Title:          input.Title,
		Body:           input.Body,
		RawPayload:     input.RawPayload,
		AgentSessionID: firstString(input.AgentSessionID, task.AgentSessionID),
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		return nil, err
	}
	if err := task.RequestInteraction(interaction.ID, interaction.Kind, interaction.Title, now, interaction.AgentSessionID); err != nil {
		return nil, err
	}
	if err := s.store.SaveTaskInteraction(ctx, *interaction); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) RespondTaskInteraction(ctx context.Context, input RespondTaskInteractionInput) (*domain.TaskInteraction, error) {
	if strings.TrimSpace(input.InteractionID) == "" {
		return nil, fmt.Errorf("interaction id is required")
	}
	if !input.Decision.Valid() {
		return nil, fmt.Errorf("unsupported task interaction decision %q", input.Decision)
	}
	now := s.clock()
	interaction, err := s.store.TaskInteraction(ctx, input.InteractionID)
	if err != nil {
		return nil, err
	}
	if interaction.Status == domain.TaskInteractionAnswered {
		if interaction.ResponseDecision == input.Decision &&
			interaction.ResponseMessage == input.Message &&
			interaction.ResponsePayload == input.Payload {
			return interaction, nil
		}
		return nil, fmt.Errorf("%w: interaction %s is already answered", domain.ErrConflict, interaction.ID)
	}
	if interaction.Status != domain.TaskInteractionPending {
		return nil, fmt.Errorf("%w: interaction %s is %s", domain.ErrConflict, interaction.ID, interaction.Status)
	}
	task, err := s.store.Task(ctx, interaction.TaskID)
	if err != nil {
		return nil, err
	}
	if task.WorkerID == "" {
		return nil, fmt.Errorf("%w: interaction %s has no worker", domain.ErrConflict, interaction.ID)
	}
	worker, err := s.store.Worker(ctx, task.WorkerID)
	if err != nil {
		return nil, err
	}
	if worker.Status != domain.WorkerOnline {
		return nil, fmt.Errorf("%w: worker %s is not online", domain.ErrConflict, worker.ID)
	}
	interaction.Status = domain.TaskInteractionAnswered
	interaction.ResponseDecision = input.Decision
	interaction.ResponseMessage = input.Message
	interaction.ResponsePayload = input.Payload
	interaction.UpdatedAt = now
	if err := s.store.SaveTaskInteraction(ctx, *interaction); err != nil {
		return nil, err
	}
	if err := task.RecordInteractionAnswered(interaction.ID, interaction.ResponseDecision, now); err != nil {
		return nil, err
	}
	if err := task.QueueInteractionDirective("dir_"+uuid.NewString(), interaction.ID, input.Decision, input.Message, input.Payload, now); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, err
	}
	return interaction, nil
}

func (s *Service) AssignedTasks(ctx context.Context, workerID string) ([]*domain.Task, error) {
	all, err := s.store.Tasks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Task, 0, 4)
	for _, task := range all {
		if task.WorkerID != workerID {
			continue
		}
		if isTaskTerminal(task.Status) {
			continue
		}
		if task.Status == domain.TaskCreated || task.Status == domain.TaskAssigned {
			continue
		}
		out = append(out, task)
	}
	return out, nil
}

func (s *Service) AssignedTaskBundle(ctx context.Context, taskID string) (*domain.Task, *domain.Project, *domain.Worker, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, nil, nil, err
	}
	if task.WorkerID == "" {
		return nil, nil, nil, fmt.Errorf("%w: task %s has no worker", domain.ErrConflict, taskID)
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return nil, nil, nil, err
	}
	worker, err := s.store.Worker(ctx, task.WorkerID)
	if err != nil {
		return nil, nil, nil, err
	}
	return task, project, worker, nil
}

func (s *Service) BuildStartPayload(task *domain.Task, project *domain.Project, worker *domain.Worker) protocol.TaskStartPayload {
	return buildStartPayload(task, project, worker)
}

func (s *Service) BuildContinuePayload(task *domain.Task, project *domain.Project, worker *domain.Worker, message string) protocol.TaskContinuePayload {
	return buildContinuePayload(task, project, worker, message)
}

func (s *Service) AckTaskDirective(ctx context.Context, taskID, directiveID string) (*domain.Task, error) {
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.AckDirective(directiveID, now); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if len(events) == 0 {
		return task, nil
	}
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, events); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) ApplyWorkerTaskInteractionResolved(ctx context.Context, messageID string, input TaskInteractionResolvedInput) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, input.TaskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, input.TaskID)
	if err != nil {
		return nil, err
	}
	if input.Responded {
		interaction, err := s.store.TaskInteraction(ctx, input.InteractionID)
		if err != nil {
			return nil, err
		}
		if interaction.Status == domain.TaskInteractionPending {
			interaction.Status = domain.TaskInteractionAnswered
			interaction.ResponseDecision = input.Decision
			interaction.ResponseMessage = input.Message
			interaction.ResponsePayload = input.Payload
			interaction.UpdatedAt = now
			if err := s.store.SaveTaskInteraction(ctx, *interaction); err != nil {
				return nil, err
			}
			if err := task.RecordInteractionAnswered(interaction.ID, interaction.ResponseDecision, now); err != nil {
				return nil, err
			}
		}
	}
	if task.Status == domain.TaskWaitingInput {
		if err := task.Resume(now); err != nil {
			return nil, err
		}
	} else if task.Status != domain.TaskRunning {
		return nil, fmt.Errorf("%w: resolve interaction from %s", domain.ErrInvalidTransition, task.Status)
	} else {
		if err := task.RecordInteractionResolved(input.InteractionID, now); err != nil {
			return nil, err
		}
	}
	if task.PendingDirective != nil && task.PendingDirective.Kind == domain.TaskDirectiveInteraction && task.PendingDirective.InteractionID == input.InteractionID {
		if err := task.AckDirective(task.PendingDirective.ID, now); err != nil {
			return nil, err
		}
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerTaskResult(ctx context.Context, messageID, taskID, result string, agentSessionIDs ...string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.RecordResult(result, s.clock(), firstString(agentSessionIDs...)); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerTaskCompleted(ctx context.Context, messageID, taskID, result string, agentSessionIDs ...string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.Complete(result, now, firstString(agentSessionIDs...)); err != nil {
		return nil, err
	}
	if err := s.releaseWorkerFromTask(ctx, task, now); err != nil {
		return nil, err
	}
	if err := s.store.CancelPendingTaskInteractions(ctx, task.ID, now); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerTaskFailed(ctx context.Context, messageID, taskID, reason string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.Fail(reason, now); err != nil {
		return nil, err
	}
	if err := s.releaseWorkerFromTask(ctx, task, now); err != nil {
		return nil, err
	}
	if err := s.store.CancelPendingTaskInteractions(ctx, task.ID, now); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) ApplyWorkerTaskInterrupted(ctx context.Context, messageID, taskID, result string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if result != "" {
		if err := task.RecordResult(result, now); err != nil {
			return nil, err
		}
	}
	if err := task.MarkInterrupted(now); err != nil {
		return nil, err
	}
	if err := s.releaseWorkerFromTask(ctx, task, now); err != nil {
		return nil, err
	}
	if err := s.store.CancelPendingTaskInteractions(ctx, task.ID, now); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.appendEvents(ctx, events)
}

func (s *Service) releaseWorkerFromTask(ctx context.Context, task *domain.Task, now time.Time) error {
	if task.WorkerID == "" {
		return nil
	}
	return s.releaseWorkerIDFromTask(ctx, task.WorkerID, task.ID, now)
}

func (s *Service) releaseWorkerIDFromTask(ctx context.Context, workerID, taskID string, now time.Time) error {
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil
	}
	before := len(worker.CurrentTaskIDs)
	worker.ReleaseTask(taskID, now)
	if len(worker.CurrentTaskIDs) == before {
		return nil
	}
	workerEvents := worker.PullEvents()
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return err
	}
	return s.appendEvents(ctx, workerEvents)
}

func (s *Service) Settings(ctx context.Context) (*domain.Settings, error) {
	return s.store.Settings(ctx)
}

func (s *Service) UpdateWorkerHeartbeatTimeout(ctx context.Context, timeout string) (*domain.Settings, error) {
	if timeout != "" {
		parsed, err := time.ParseDuration(timeout)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("invalid worker heartbeat timeout %q", timeout)
		}
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	settings.UpdateWorkerHeartbeatTimeout(timeout, s.clock())
	events := settings.PullEvents()
	if err := s.store.SaveSettings(ctx, settings); err != nil {
		return nil, err
	}
	return settings, s.appendEvents(ctx, events)
}

func (s *Service) appendEvents(ctx context.Context, events []domain.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return err
	}
	s.publishEvents(events)
	outboxIDs := make([]string, 0, len(events))
	for _, event := range events {
		outboxIDs = append(outboxIDs, "out_"+event.EventID)
	}
	return s.store.MarkOutboxPublished(ctx, outboxIDs, s.clock())
}

func (s *Service) publishEvents(events []domain.DomainEvent) {
	s.subscribersMu.RLock()
	defer s.subscribersMu.RUnlock()
	for _, event := range events {
		for subscriber, filter := range s.subscribers {
			if !eventMatchesFilter(event, filter) {
				continue
			}
			select {
			case subscriber <- event:
			default:
			}
		}
	}
}

func eventMatchesFilter(event domain.DomainEvent, filter domain.EventFilter) bool {
	if filter.AggregateID != "" && event.AggregateID != filter.AggregateID {
		return false
	}
	if filter.AggregateType != "" && event.AggregateType != filter.AggregateType {
		return false
	}
	if filter.EventType != "" && event.EventType != filter.EventType {
		return false
	}
	return true
}

func buildStartPayload(task *domain.Task, project *domain.Project, worker *domain.Worker) protocol.TaskStartPayload {
	runtime := worker.EnabledRuntimeEnv(task.AgentType)
	env := make([]protocol.RuntimeEnvVar, 0, len(runtime))
	for _, item := range runtime {
		env = append(env, protocol.RuntimeEnvVar{Key: item.Key, Value: item.Value, Sensitive: item.Sensitive})
	}
	return protocol.TaskStartPayload{
		Task: protocol.TaskPayload{
			ID:           task.ID,
			Title:        task.Title,
			Description:  task.Description,
			AgentType:    task.AgentType,
			AgentConfig:  task.AgentConfig,
			BaseBranch:   task.BaseBranch,
			PreCommands:  append([]string(nil), task.PreCommands...),
			PostCommands: append([]string(nil), task.PostCommands...),
		},
		Project: protocol.ProjectPayload{
			ID:                 project.ID,
			GitURL:             project.GitURL,
			DefaultBranch:      project.DefaultBranch,
			WorktreeNamePrefix: project.WorktreeNamePrefix,
		},
		AgentRuntimeEnv: env,
	}
}

func buildContinuePayload(task *domain.Task, project *domain.Project, worker *domain.Worker, message string) protocol.TaskContinuePayload {
	start := buildStartPayload(task, project, worker)
	return protocol.TaskContinuePayload{
		Task:            start.Task,
		Project:         start.Project,
		Message:         message,
		AgentSessionID:  task.AgentSessionID,
		WorktreePath:    task.WorktreePath,
		AgentRuntimeEnv: start.AgentRuntimeEnv,
		Settings:        start.Settings,
	}
}

func isTaskTerminal(status domain.TaskStatus) bool {
	switch status {
	case domain.TaskCompleted, domain.TaskFailed, domain.TaskInterrupted, domain.TaskArchived:
		return true
	default:
		return false
	}
}

func workerCanRunTask(worker *domain.Worker, agent domain.AgentType, projectID, _ string) bool {
	if worker.Status != domain.WorkerOnline {
		return false
	}
	return workerSupportsAgent(worker, agent) && workerAllowsProject(worker, projectID)
}

func workerSupportsAgent(worker *domain.Worker, agent domain.AgentType) bool {
	for _, supported := range worker.SupportedAgents {
		if supported == agent {
			return true
		}
	}
	return false
}

func workerAllowsProject(worker *domain.Worker, projectID string) bool {
	if worker.ProjectBindingMode == domain.WorkerAllProjects {
		return true
	}
	for _, bound := range worker.BoundProjectIDs {
		if bound == projectID {
			return true
		}
	}
	return false
}

func validateTaskCanContinue(task *domain.Task) error {
	if task.Status != domain.TaskCompleted {
		return fmt.Errorf("%w: continue from %s", domain.ErrInvalidTransition, task.Status)
	}
	if task.WorkerID == "" {
		return fmt.Errorf("%w: continue task %s without worker", domain.ErrConflict, task.ID)
	}
	if task.WorktreePath == "" {
		return fmt.Errorf("%w: continue task %s without worktree", domain.ErrConflict, task.ID)
	}
	if task.AgentSessionID == "" {
		return fmt.Errorf("%w: continue task %s without agent session", domain.ErrConflict, task.ID)
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
