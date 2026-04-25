package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/pkg/store"
)

type Clock func() time.Time

type Service struct {
	store store.Store
	clock Clock
}

type Option func(*Service)

func WithClock(clock Clock) Option {
	return func(service *Service) {
		service.clock = clock
	}
}

func NewService(st store.Store, options ...Option) *Service {
	service := &Service{
		store: st,
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Store() store.Store {
	return s.store
}

type CreateProjectInput struct {
	Name               string   `json:"name"`
	GitURL             string   `json:"gitUrl"`
	DefaultBranch      string   `json:"defaultBranch"`
	WorktreeNamePrefix string   `json:"worktreeNamePrefix"`
	SetupCommands      []string `json:"setupCommands"`
}

func (s *Service) CreateProject(ctx context.Context, input CreateProjectInput) (*domain.Project, error) {
	now := s.clock()
	project, err := domain.NewProject(domain.NewProjectInput{
		ID:                 "project_" + uuid.NewString(),
		Name:               input.Name,
		GitURL:             input.GitURL,
		DefaultBranch:      input.DefaultBranch,
		WorktreeNamePrefix: input.WorktreeNamePrefix,
		SetupCommands:      input.SetupCommands,
		Now:                now,
	})
	if err != nil {
		return nil, err
	}
	events := project.PullEvents()
	if err := s.store.SaveProject(ctx, project); err != nil {
		return nil, err
	}
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *Service) Projects(ctx context.Context) ([]*domain.Project, error) {
	return s.store.Projects(ctx)
}

func (s *Service) ArchiveProject(ctx context.Context, id string) (*domain.Project, error) {
	project, err := s.store.Project(ctx, id)
	if err != nil {
		return nil, err
	}
	project.Archive(s.clock())
	events := project.PullEvents()
	if err := s.store.SaveProject(ctx, project); err != nil {
		return nil, err
	}
	return project, s.store.AppendEvents(ctx, events)
}

type CreateTaskInput struct {
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	ProjectID    string           `json:"projectId"`
	AgentType    domain.AgentType `json:"agentType"`
	BaseBranch   string           `json:"baseBranch"`
	TargetBranch string           `json:"targetBranch"`
	PreCommands  []string         `json:"preCommands"`
	PostCommands []string         `json:"postCommands"`
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (*domain.Task, error) {
	now := s.clock()
	if _, err := s.store.Project(ctx, input.ProjectID); err != nil {
		return nil, err
	}
	task, err := domain.NewTask(domain.NewTaskInput{
		ID:           "task_" + uuid.NewString(),
		Title:        input.Title,
		Description:  input.Description,
		ProjectID:    input.ProjectID,
		AgentType:    input.AgentType,
		BaseBranch:   input.BaseBranch,
		TargetBranch: input.TargetBranch,
		PreCommands:  input.PreCommands,
		PostCommands: input.PostCommands,
		Now:          now,
	})
	if err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) Tasks(ctx context.Context) ([]*domain.Task, error) {
	return s.store.Tasks(ctx)
}

func (s *Service) Task(ctx context.Context, id string) (*domain.Task, error) {
	return s.store.Task(ctx, id)
}

type RegisterWorkerInput struct {
	ID              string                          `json:"id"`
	Name            string                          `json:"name"`
	SupportedAgents []domain.AgentType              `json:"supportedAgents"`
	WorkDir         string                          `json:"workDir"`
	BindingMode     domain.WorkerProjectBindingMode `json:"projectBindingMode"`
	BoundProjectIDs []string                        `json:"boundProjectIds"`
	Capabilities    map[string]string               `json:"capabilities"`
}

func (s *Service) RegisterWorker(ctx context.Context, input RegisterWorkerInput) (*domain.Worker, error) {
	id := input.ID
	if id == "" {
		id = "worker_" + uuid.NewString()
	}
	if existing, err := s.store.Worker(ctx, id); err == nil {
		return existing, nil
	}
	worker, err := domain.NewWorker(domain.NewWorkerInput{
		ID:                 id,
		Name:               input.Name,
		SupportedAgents:    input.SupportedAgents,
		WorkDir:            input.WorkDir,
		ProjectBindingMode: input.BindingMode,
		BoundProjectIDs:    input.BoundProjectIDs,
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
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return nil, err
	}
	return worker, nil
}

func (s *Service) Workers(ctx context.Context) ([]*domain.Worker, error) {
	return s.store.Workers(ctx)
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
	return worker, s.store.AppendEvents(ctx, events)
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
	return worker, s.store.AppendEvents(ctx, events)
}

func (s *Service) AssignWorker(ctx context.Context, taskID, workerID string) (*domain.Task, error) {
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	worker, err := s.store.Worker(ctx, workerID)
	if err != nil {
		return nil, err
	}
	if !worker.CanAcceptTask(task.AgentType, task.ProjectID) && worker.CurrentTaskID != taskID {
		return nil, fmt.Errorf("%w: worker %s cannot accept task %s", domain.ErrConflict, workerID, taskID)
	}
	if err := task.AssignWorker(workerID, now); err != nil {
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
	if err := s.store.AppendEvents(ctx, append(taskEvents, workerEvents...)); err != nil {
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
	settings, err := s.store.Settings(ctx)
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
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return nil, protocol.TaskStartPayload{}, err
	}
	return task, buildStartPayload(task, project, settings), nil
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
	if err := s.store.AppendEvents(ctx, append(taskEvents, workerEvents...)); err != nil {
		return nil, err
	}
	return task, nil
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
	return task, s.store.AppendEvents(ctx, events)
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
	return task, s.store.AppendEvents(ctx, events)
}

func (s *Service) ApplyWorkerConversation(ctx context.Context, messageID, taskID, role, content string) (*domain.Task, error) {
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
	if err := s.store.AppendConversation(ctx, domain.ConversationMessage{ID: "msg_" + uuid.NewString(), TaskID: taskID, Role: role, Content: content, CreatedAt: now}); err != nil {
		return nil, err
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.store.AppendEvents(ctx, events)
}

func (s *Service) ApplyWorkerTaskCompleted(ctx context.Context, messageID, taskID, result string) (*domain.Task, error) {
	ok, err := s.store.MarkMessageProcessed(ctx, messageID)
	if err != nil || !ok {
		return s.store.Task(ctx, taskID)
	}
	now := s.clock()
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := task.Complete(result, now); err != nil {
		return nil, err
	}
	if task.WorkerID != "" {
		if worker, err := s.store.Worker(ctx, task.WorkerID); err == nil {
			worker.ReleaseTask(now)
			workerEvents := worker.PullEvents()
			if err := s.store.SaveWorker(ctx, worker); err != nil {
				return nil, err
			}
			if err := s.store.AppendEvents(ctx, workerEvents); err != nil {
				return nil, err
			}
		}
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.store.AppendEvents(ctx, events)
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
	if task.WorkerID != "" {
		if worker, err := s.store.Worker(ctx, task.WorkerID); err == nil {
			worker.ReleaseTask(now)
			workerEvents := worker.PullEvents()
			if err := s.store.SaveWorker(ctx, worker); err != nil {
				return nil, err
			}
			if err := s.store.AppendEvents(ctx, workerEvents); err != nil {
				return nil, err
			}
		}
	}
	events := task.PullEvents()
	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	return task, s.store.AppendEvents(ctx, events)
}

func (s *Service) Settings(ctx context.Context) (*domain.Settings, error) {
	return s.store.Settings(ctx)
}

func (s *Service) UpdateAgentRuntimeEnvVars(ctx context.Context, vars []domain.AgentRuntimeEnvVar) (*domain.Settings, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	settings.UpdateAgentRuntimeEnvVars(vars, s.clock())
	events := settings.PullEvents()
	if err := s.store.SaveSettings(ctx, settings); err != nil {
		return nil, err
	}
	return settings, s.store.AppendEvents(ctx, events)
}

func buildStartPayload(task *domain.Task, project *domain.Project, settings *domain.Settings) protocol.TaskStartPayload {
	runtime := settings.EnabledRuntimeEnv()
	env := make([]protocol.RuntimeEnvVar, 0, len(runtime))
	for _, item := range settings.AgentRuntimeEnvVars {
		if item.Enabled {
			env = append(env, protocol.RuntimeEnvVar{Key: item.Key, Value: item.Value, Sensitive: item.Sensitive})
		}
	}
	return protocol.TaskStartPayload{
		Task: protocol.TaskPayload{
			ID:           task.ID,
			Title:        task.Title,
			Description:  task.Description,
			AgentType:    task.AgentType,
			BaseBranch:   task.BaseBranch,
			TargetBranch: task.TargetBranch,
			PreCommands:  append([]string(nil), task.PreCommands...),
			PostCommands: append([]string(nil), task.PostCommands...),
		},
		Project: protocol.ProjectPayload{
			ID:                 project.ID,
			GitURL:             project.GitURL,
			DefaultBranch:      project.DefaultBranch,
			WorktreeNamePrefix: project.WorktreeNamePrefix,
			SetupCommands:      append([]string(nil), project.SetupCommands...),
		},
		AgentRuntimeEnv: env,
	}
}
