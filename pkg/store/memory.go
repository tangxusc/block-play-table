package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

type Store interface {
	Ping(context.Context) error
	SaveTask(context.Context, *domain.Task) error
	Task(context.Context, string) (*domain.Task, error)
	Tasks(context.Context) ([]*domain.Task, error)
	SaveWorker(context.Context, *domain.Worker) error
	Worker(context.Context, string) (*domain.Worker, error)
	Workers(context.Context) ([]*domain.Worker, error)
	DeleteWorker(context.Context, string) error
	SaveProject(context.Context, *domain.Project) error
	Project(context.Context, string) (*domain.Project, error)
	Projects(context.Context) ([]*domain.Project, error)
	SaveSettings(context.Context, *domain.Settings) error
	Settings(context.Context) (*domain.Settings, error)
	AppendTaskLog(context.Context, domain.TaskLog) error
	TaskLogs(context.Context, string) ([]domain.TaskLog, error)
	AppendConversation(context.Context, domain.ConversationMessage) error
	TaskConversations(context.Context, string) ([]domain.ConversationMessage, error)
	SaveTaskInteraction(context.Context, domain.TaskInteraction) error
	TaskInteraction(context.Context, string) (*domain.TaskInteraction, error)
	TaskInteractions(context.Context, string, domain.TaskInteractionStatus) ([]domain.TaskInteraction, error)
	CancelPendingTaskInteractions(context.Context, string, time.Time) error
	AppendEvents(context.Context, []domain.DomainEvent) error
	DomainEvents(context.Context, domain.EventFilter) ([]domain.DomainEvent, error)
	OutboxMessages(context.Context, bool) ([]domain.OutboxMessage, error)
	MarkOutboxPublished(context.Context, []string, time.Time) error
	MarkMessageProcessed(context.Context, string) (bool, error)
}

type MemoryStore struct {
	mu                sync.RWMutex
	tasks             map[string]*domain.Task
	workers           map[string]*domain.Worker
	projects          map[string]*domain.Project
	settings          *domain.Settings
	logs              []domain.TaskLog
	conversations     []domain.ConversationMessage
	interactions      map[string]domain.TaskInteraction
	events            []domain.DomainEvent
	outbox            []domain.OutboxMessage
	processedMessages map[string]struct{}
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:             map[string]*domain.Task{},
		workers:           map[string]*domain.Worker{},
		projects:          map[string]*domain.Project{},
		interactions:      map[string]domain.TaskInteraction{},
		settings:          domain.NewSettings(time.Now().UTC()),
		processedMessages: map[string]struct{}{},
	}
}

func (s *MemoryStore) Ping(ctx context.Context) error {
	return nil
}

func (s *MemoryStore) SaveTask(ctx context.Context, task *domain.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = cloneTask(task)
	return nil
}

func (s *MemoryStore) Task(ctx context.Context, id string) (*domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
	}
	return cloneTask(task), nil
}

func (s *MemoryStore) Tasks(ctx context.Context) ([]*domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		out = append(out, cloneTask(task))
	}
	slices.SortFunc(out, func(a, b *domain.Task) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (s *MemoryStore) SaveWorker(ctx context.Context, worker *domain.Worker) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[worker.ID] = cloneWorker(worker)
	return nil
}

func (s *MemoryStore) Worker(ctx context.Context, id string) (*domain.Worker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	worker, ok := s.workers[id]
	if !ok {
		return nil, fmt.Errorf("%w: worker %s", domain.ErrNotFound, id)
	}
	return cloneWorker(worker), nil
}

func (s *MemoryStore) Workers(ctx context.Context) ([]*domain.Worker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Worker, 0, len(s.workers))
	for _, worker := range s.workers {
		out = append(out, cloneWorker(worker))
	}
	return out, nil
}

func (s *MemoryStore) DeleteWorker(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workers[id]; !ok {
		return fmt.Errorf("%w: worker %s", domain.ErrNotFound, id)
	}
	delete(s.workers, id)
	return nil
}

func (s *MemoryStore) SaveProject(ctx context.Context, project *domain.Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[project.ID] = cloneProject(project)
	return nil
}

func (s *MemoryStore) Project(ctx context.Context, id string) (*domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	project, ok := s.projects[id]
	if !ok {
		return nil, fmt.Errorf("%w: project %s", domain.ErrNotFound, id)
	}
	return cloneProject(project), nil
}

func (s *MemoryStore) Projects(ctx context.Context) ([]*domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Project, 0, len(s.projects))
	for _, project := range s.projects {
		out = append(out, cloneProject(project))
	}
	return out, nil
}

func (s *MemoryStore) SaveSettings(ctx context.Context, settings *domain.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *settings
	s.settings = &copy
	return nil
}

func (s *MemoryStore) Settings(ctx context.Context) (*domain.Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := *s.settings
	return &copy, nil
}

func (s *MemoryStore) AppendTaskLog(ctx context.Context, log domain.TaskLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, log)
	return nil
}

func (s *MemoryStore) TaskLogs(ctx context.Context, taskID string) ([]domain.TaskLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.TaskLog
	for _, log := range s.logs {
		if log.TaskID == taskID {
			out = append(out, log)
		}
	}
	return out, nil
}

func (s *MemoryStore) AppendConversation(ctx context.Context, message domain.ConversationMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversations = append(s.conversations, message)
	return nil
}

func (s *MemoryStore) TaskConversations(ctx context.Context, taskID string) ([]domain.ConversationMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.ConversationMessage
	for _, message := range s.conversations {
		if message.TaskID == taskID {
			out = append(out, message)
		}
	}
	return out, nil
}

func (s *MemoryStore) SaveTaskInteraction(ctx context.Context, interaction domain.TaskInteraction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interactions[interaction.ID] = interaction
	return nil
}

func (s *MemoryStore) TaskInteraction(ctx context.Context, id string) (*domain.TaskInteraction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	interaction, ok := s.interactions[id]
	if !ok {
		return nil, fmt.Errorf("%w: task interaction %s", domain.ErrNotFound, id)
	}
	copy := interaction
	return &copy, nil
}

func (s *MemoryStore) TaskInteractions(ctx context.Context, taskID string, status domain.TaskInteractionStatus) ([]domain.TaskInteraction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.TaskInteraction, 0)
	for _, interaction := range s.interactions {
		if interaction.TaskID != taskID {
			continue
		}
		if status != "" && interaction.Status != status {
			continue
		}
		out = append(out, interaction)
	}
	slices.SortFunc(out, func(a, b domain.TaskInteraction) int {
		if cmp := a.CreatedAt.Compare(b.CreatedAt); cmp != 0 {
			return cmp
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out, nil
}

func (s *MemoryStore) CancelPendingTaskInteractions(ctx context.Context, taskID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, interaction := range s.interactions {
		if interaction.TaskID == taskID && interaction.Status == domain.TaskInteractionPending {
			interaction.Status = domain.TaskInteractionCanceled
			interaction.ResponseDecision = domain.TaskInteractionCancel
			interaction.UpdatedAt = now
			s.interactions[id] = interaction
		}
	}
	return nil
}

func (s *MemoryStore) AppendEvents(ctx context.Context, events []domain.DomainEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	for _, event := range events {
		s.outbox = append(s.outbox, domain.OutboxMessage{
			ID:        "out_" + event.EventID,
			Event:     event,
			Status:    domain.OutboxPending,
			CreatedAt: event.OccurredAt,
		})
	}
	return nil
}

func (s *MemoryStore) DomainEvents(ctx context.Context, filter domain.EventFilter) ([]domain.DomainEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.DomainEvent
	for _, event := range s.events {
		if filter.AggregateID != "" && event.AggregateID != filter.AggregateID {
			continue
		}
		if filter.AggregateType != "" && event.AggregateType != filter.AggregateType {
			continue
		}
		if filter.EventType != "" && event.EventType != filter.EventType {
			continue
		}
		if !eventMatchesSearch(event, filter.Search) {
			continue
		}
		out = append(out, event)
	}
	return out, nil
}

func eventMatchesSearch(event domain.DomainEvent, search string) bool {
	query := strings.ToLower(strings.TrimSpace(search))
	if query == "" {
		return true
	}
	fields := []string{
		event.EventID,
		event.EventType,
		event.AggregateType,
		event.AggregateID,
		string(event.Payload),
		event.CorrelationID,
		event.CausationID,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func (s *MemoryStore) OutboxMessages(ctx context.Context, includePublished bool) ([]domain.OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.OutboxMessage, 0, len(s.outbox))
	for _, message := range s.outbox {
		if !includePublished && message.Status != domain.OutboxPending {
			continue
		}
		out = append(out, cloneOutboxMessage(message))
	}
	return out, nil
}

func (s *MemoryStore) MarkOutboxPublished(ctx context.Context, ids []string, publishedAt time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		selected[id] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.outbox {
		if _, ok := selected[s.outbox[index].ID]; ok {
			s.outbox[index].Status = domain.OutboxPublished
			s.outbox[index].PublishedAt = &publishedAt
		}
	}
	return nil
}

func (s *MemoryStore) MarkMessageProcessed(ctx context.Context, messageID string) (bool, error) {
	if messageID == "" {
		return true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.processedMessages[messageID]; ok {
		return false, nil
	}
	s.processedMessages[messageID] = struct{}{}
	return true, nil
}

func cloneTask(task *domain.Task) *domain.Task {
	copy := *task
	copy.PreCommands = append([]string(nil), task.PreCommands...)
	copy.PostCommands = append([]string(nil), task.PostCommands...)
	return &copy
}

func cloneWorker(worker *domain.Worker) *domain.Worker {
	copy := *worker
	copy.SupportedAgents = append([]domain.AgentType(nil), worker.SupportedAgents...)
	copy.BoundProjectIDs = append([]string(nil), worker.BoundProjectIDs...)
	copy.CurrentTaskIDs = append([]string(nil), worker.CurrentTaskIDs...)
	copy.AgentRuntimeEnv = cloneWorkerAgentRuntimeEnv(worker.AgentRuntimeEnv)
	if worker.Capabilities != nil {
		copy.Capabilities = map[string]string{}
		for k, v := range worker.Capabilities {
			copy.Capabilities[k] = v
		}
	}
	return &copy
}

func cloneWorkerAgentRuntimeEnv(in []domain.WorkerAgentRuntimeEnv) []domain.WorkerAgentRuntimeEnv {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.WorkerAgentRuntimeEnv, 0, len(in))
	for _, group := range in {
		out = append(out, domain.WorkerAgentRuntimeEnv{
			AgentType: group.AgentType,
			Vars:      append([]domain.AgentRuntimeEnvVar(nil), group.Vars...),
		})
	}
	return out
}

func cloneProject(project *domain.Project) *domain.Project {
	copy := *project
	return &copy
}

func cloneOutboxMessage(message domain.OutboxMessage) domain.OutboxMessage {
	copy := message
	if message.PublishedAt != nil {
		publishedAt := *message.PublishedAt
		copy.PublishedAt = &publishedAt
	}
	return copy
}
