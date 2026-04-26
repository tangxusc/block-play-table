package app

import (
	"context"
	"fmt"
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
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Project, 0, len(projects))
	for _, project := range projects {
		if !includeArchived && project.Archived {
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
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	ProjectID    string           `json:"projectId"`
	WorkerID     string           `json:"workerId"`
	AgentType    domain.AgentType `json:"agentType"`
	BaseBranch   string           `json:"baseBranch"`
	PreCommands  []string         `json:"preCommands"`
	PostCommands []string         `json:"postCommands"`
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
}

type ContinueTaskInput struct {
	TaskID  string `json:"taskId"`
	Message string `json:"message"`
}

type TaskFilter struct {
	Status          domain.TaskStatus
	ProjectID       string
	WorkerID        string
	AgentType       domain.AgentType
	IncludeArchived bool
}

type PageInput struct {
	Offset int
	Limit  int
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
	task, err := domain.NewTask(domain.NewTaskInput{
		ID:           "task_" + uuid.NewString(),
		Title:        input.Title,
		Description:  input.Description,
		ProjectID:    input.ProjectID,
		AgentType:    input.AgentType,
		BaseBranch:   input.BaseBranch,
		PreCommands:  input.PreCommands,
		PostCommands: input.PostCommands,
		Now:          now,
	})
	if err != nil {
		return nil, err
	}
	var worker *domain.Worker
	if input.WorkerID != "" {
		worker, err = s.store.Worker(ctx, input.WorkerID)
		if err != nil {
			return nil, err
		}
		if !workerCanRunTask(worker, input.AgentType, input.ProjectID, task.ID) {
			return nil, fmt.Errorf("%w: worker %s cannot accept task %s", domain.ErrConflict, input.WorkerID, task.ID)
		}
		if err := task.AssignWorkerWithAgent(input.WorkerID, input.AgentType, now); err != nil {
			return nil, err
		}
		if err := worker.AssignTask(task.ID, now); err != nil {
			return nil, err
		}
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if worker != nil {
		workerEvents := worker.PullEvents()
		if err := s.store.SaveWorker(ctx, worker); err != nil {
			return nil, err
		}
		events = append(events, workerEvents...)
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
		if !filter.IncludeArchived && task.Status == domain.TaskArchived {
			continue
		}
		filtered = append(filtered, task)
	}
	total := len(filtered)
	start := page.Offset
	if start < 0 {
		start = 0
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	end := len(filtered)
	if page.Limit > 0 && start+page.Limit < end {
		end = start + page.Limit
	}
	return filtered[start:end], total, nil
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
				Capabilities:           input.Capabilities,
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
}

func (s *Service) Worker(ctx context.Context, id string) (*domain.Worker, error) {
	return s.store.Worker(ctx, id)
}

func (s *Service) WorkersFiltered(ctx context.Context, filter WorkerFilter) ([]*domain.Worker, error) {
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
		out = append(out, worker)
	}
	return out, nil
}

func (s *Service) UpdateWorker(ctx context.Context, input RegisterWorkerInput) (*domain.Worker, error) {
	worker, err := s.store.Worker(ctx, input.ID)
	if err != nil {
		return nil, err
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
		Capabilities:           input.Capabilities,
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
	if worker.CurrentTaskID != "" {
		return fmt.Errorf("%w: worker %s has current task %s", domain.ErrConflict, workerID, worker.CurrentTaskID)
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
	if timeout <= 0 {
		return nil
	}
	workers, err := s.store.Workers(ctx)
	if err != nil {
		return err
	}
	cutoff := s.clock().Add(-timeout)
	for _, worker := range workers {
		if worker.Status != domain.WorkerOnline {
			continue
		}
		if worker.LastHeartbeatAt != nil && worker.LastHeartbeatAt.After(cutoff) {
			continue
		}
		worker.MarkOffline(s.clock())
		events := worker.PullEvents()
		if err := s.store.SaveWorker(ctx, worker); err != nil {
			return err
		}
		if err := s.appendEvents(ctx, events); err != nil {
			return err
		}
	}
	return nil
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
			_ = s.MarkStaleWorkersOffline(ctx, timeout)
		}
	}
}

func (s *Service) AssignWorker(ctx context.Context, taskID, workerID string, agentTypes ...domain.AgentType) (*domain.Task, error) {
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
	if err := task.AssignWorkerWithAgent(workerID, agentType, now); err != nil {
		return nil, err
	}
	if err := worker.AssignTask(taskID, now); err != nil {
		return nil, err
	}
	taskEvents := task.PullEvents()
	workerEvents := worker.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.store.SaveWorker(ctx, worker); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, append(taskEvents, workerEvents...)); err != nil {
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
	if worker.Status != domain.WorkerOnline || worker.CurrentTaskID != task.ID {
		return nil, protocol.TaskStartPayload{}, fmt.Errorf("%w: worker %s is not ready for task", domain.ErrConflict, worker.ID)
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	if err := task.Start(now); err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
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
	if err := selected.AssignTask(task.ID, now); err != nil {
		return nil, err
	}
	taskEvents := task.PullEvents()
	workerEvents := selected.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.store.SaveWorker(ctx, selected); err != nil {
		return nil, err
	}
	if err := s.appendEvents(ctx, append(taskEvents, workerEvents...)); err != nil {
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
	if worker.CurrentTaskID != "" && worker.CurrentTaskID != taskID {
		return nil
	}
	worker.ReleaseTask(now)
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

func workerCanRunTask(worker *domain.Worker, agent domain.AgentType, projectID, taskID string) bool {
	if worker.Status != domain.WorkerOnline || (worker.CurrentTaskID != "" && worker.CurrentTaskID != taskID) {
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
