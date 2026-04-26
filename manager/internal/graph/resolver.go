package graph

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/graph/model"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

type WorkerSender interface {
	SendTaskStart(workerID, taskID string, payload protocol.TaskStartPayload) error
	SendTaskContinue(workerID, taskID string, payload protocol.TaskContinuePayload) error
	SendTaskInterrupt(workerID, taskID string) error
	SendTaskCancel(workerID, taskID string) error
}

type Resolver struct {
	Service      *app.Service
	WorkerSender WorkerSender
}

func NewResolver(service *app.Service, sender WorkerSender) *Resolver {
	return &Resolver{Service: service, WorkerSender: sender}
}

func toModelTask(task *domain.Task) *model.Task {
	if task == nil {
		return nil
	}
	var agentType *model.AgentType
	if task.AgentType != "" {
		value := model.AgentType(task.AgentType)
		agentType = &value
	}
	return &model.Task{
		ID:             task.ID,
		Title:          task.Title,
		Description:    task.Description,
		Status:         model.TaskStatus(task.Status),
		ProjectID:      task.ProjectID,
		WorkerID:       optionalString(task.WorkerID),
		AgentType:      agentType,
		BaseBranch:     task.BaseBranch,
		WorktreePath:   optionalString(task.WorktreePath),
		AgentSessionID: optionalString(task.AgentSessionID),
		PreCommands:    append([]string(nil), task.PreCommands...),
		PostCommands:   append([]string(nil), task.PostCommands...),
		Result:         optionalString(task.Result),
		Version:        task.Version,
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
	}
}

func toModelTasks(tasks []*domain.Task) []*model.Task {
	out := make([]*model.Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, toModelTask(task))
	}
	return out
}

func toModelProject(project *domain.Project) *model.Project {
	if project == nil {
		return nil
	}
	return &model.Project{
		ID:                 project.ID,
		Name:               project.Name,
		GitURL:             project.GitURL,
		DefaultBranch:      project.DefaultBranch,
		WorktreeNamePrefix: project.WorktreeNamePrefix,
		Archived:           project.Archived,
		Version:            project.Version,
		CreatedAt:          project.CreatedAt,
		UpdatedAt:          project.UpdatedAt,
	}
}

func toModelProjects(projects []*domain.Project) []*model.Project {
	out := make([]*model.Project, 0, len(projects))
	for _, project := range projects {
		out = append(out, toModelProject(project))
	}
	return out
}

func toModelWorker(worker *domain.Worker) *model.Worker {
	if worker == nil {
		return nil
	}
	supportedAgents := make([]model.AgentType, 0, len(worker.SupportedAgents))
	for _, agent := range worker.SupportedAgents {
		supportedAgents = append(supportedAgents, model.AgentType(agent))
	}
	return &model.Worker{
		ID:                 worker.ID,
		Name:               worker.Name,
		Status:             model.WorkerStatus(worker.Status),
		Capabilities:       toKeyValues(worker.Capabilities),
		SupportedAgents:    supportedAgents,
		WorkDir:            worker.WorkDir,
		StartupCommand:     optionalString(worker.StartupCommand),
		ProjectBindingMode: model.WorkerProjectBindingMode(worker.ProjectBindingMode),
		BoundProjectIds:    append([]string(nil), worker.BoundProjectIDs...),
		AgentRuntimeEnv:    toModelWorkerAgentRuntimeEnv(worker.MaskedAgentRuntimeEnv()),
		CurrentTaskID:      optionalString(worker.CurrentTaskID),
		LastHeartbeatAt:    optionalTime(worker.LastHeartbeatAt),
		Version:            worker.Version,
		CreatedAt:          worker.CreatedAt,
		UpdatedAt:          worker.UpdatedAt,
	}
}

func toModelWorkers(workers []*domain.Worker) []*model.Worker {
	out := make([]*model.Worker, 0, len(workers))
	for _, worker := range workers {
		out = append(out, toModelWorker(worker))
	}
	return out
}

func toModelSettings(settings *domain.Settings) *model.Settings {
	if settings == nil {
		return nil
	}
	return &model.Settings{
		ID:                     settings.ID,
		Version:                settings.Version,
		WorkerHeartbeatTimeout: settings.WorkerHeartbeat,
		SecurityPolicy:         settings.SecurityPolicy,
		CreatedAt:              settings.CreatedAt,
		UpdatedAt:              settings.UpdatedAt,
	}
}

func toModelWorkerAgentRuntimeEnv(env []domain.WorkerAgentRuntimeEnv) []*model.WorkerAgentRuntimeEnv {
	out := make([]*model.WorkerAgentRuntimeEnv, 0, len(env))
	for _, group := range env {
		vars := make([]*model.AgentRuntimeEnvVar, 0, len(group.Vars))
		for _, item := range group.Vars {
			vars = append(vars, &model.AgentRuntimeEnvVar{
				Key:         item.Key,
				ValueMasked: item.ValueMasked,
				Description: optionalString(item.Description),
				Enabled:     item.Enabled,
				Sensitive:   item.Sensitive,
			})
		}
		out = append(out, &model.WorkerAgentRuntimeEnv{
			AgentType: model.AgentType(group.AgentType),
			Vars:      vars,
		})
	}
	return out
}

func toModelTaskLog(log domain.TaskLog) *model.TaskLog {
	return &model.TaskLog{ID: log.ID, TaskID: log.TaskID, Stream: log.Stream, Content: log.Content, CreatedAt: log.CreatedAt}
}

func toModelConversation(message domain.ConversationMessage) *model.ConversationMessage {
	return &model.ConversationMessage{
		ID:        message.ID,
		TaskID:    message.TaskID,
		Role:      message.Role,
		Content:   message.Content,
		Metadata:  toKeyValues(message.Metadata),
		CreatedAt: message.CreatedAt,
	}
}

func toModelEvent(event domain.DomainEvent) *model.DomainEvent {
	return &model.DomainEvent{
		EventID:          event.EventID,
		EventType:        event.EventType,
		AggregateType:    event.AggregateType,
		AggregateID:      event.AggregateID,
		AggregateVersion: event.AggregateVersion,
		Payload:          string(event.Payload),
		OccurredAt:       event.OccurredAt,
		CorrelationID:    optionalString(event.CorrelationID),
		CausationID:      optionalString(event.CausationID),
	}
}

func toModelEvents(events []domain.DomainEvent) []*model.DomainEvent {
	out := make([]*model.DomainEvent, 0, len(events))
	for _, event := range events {
		out = append(out, toModelEvent(event))
	}
	return out
}

func toModelOutbox(message domain.OutboxMessage) *model.OutboxMessage {
	return &model.OutboxMessage{
		ID:          message.ID,
		Event:       toModelEvent(message.Event),
		Status:      string(message.Status),
		CreatedAt:   message.CreatedAt,
		PublishedAt: optionalTime(message.PublishedAt),
	}
}

func toKeyValues(values map[string]string) []*model.KeyValue {
	if len(values) == 0 {
		return []*model.KeyValue{}
	}
	out := make([]*model.KeyValue, 0, len(values))
	for key, value := range values {
		out = append(out, &model.KeyValue{Key: key, Value: value})
	}
	return out
}

func fromKeyValueInputs(values []*model.KeyValueInput) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for _, item := range values {
		if item != nil {
			out[item.Key] = item.Value
		}
	}
	return out
}

func fromWorkerAgentRuntimeEnvInputs(values []*model.WorkerAgentRuntimeEnvInput) []domain.WorkerAgentRuntimeEnv {
	if len(values) == 0 {
		return nil
	}
	out := make([]domain.WorkerAgentRuntimeEnv, 0, len(values))
	for _, group := range values {
		if group == nil {
			continue
		}
		vars := make([]domain.AgentRuntimeEnvVar, 0, len(group.Vars))
		for _, item := range group.Vars {
			if item == nil {
				continue
			}
			vars = append(vars, domain.AgentRuntimeEnvVar{
				Key:         item.Key,
				Value:       valueOrEmpty(item.Value),
				Description: valueOrEmpty(item.Description),
				Enabled:     item.Enabled,
				Sensitive:   item.Sensitive,
			})
		}
		out = append(out, domain.WorkerAgentRuntimeEnv{
			AgentType: domain.AgentType(group.AgentType),
			Vars:      vars,
		})
	}
	return out
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	out := value
	return &out
}

func optionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func eventPayloadString(event domain.DomainEvent, key string) string {
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return ""
	}
	if value, ok := payload[key].(string); ok {
		return value
	}
	return ""
}

func (r *Resolver) registerWorkerFromInput(ctx context.Context, id *string, name string, agents []model.AgentType, workDir string, startupCommand *string, bindingMode *model.WorkerProjectBindingMode, boundProjectIDs []string, agentRuntimeEnv []*model.WorkerAgentRuntimeEnvInput, capabilities []*model.KeyValueInput, connect bool) (*model.Worker, error) {
	mode := domain.WorkerAllProjects
	if bindingMode != nil {
		mode = domain.WorkerProjectBindingMode(*bindingMode)
	}
	worker, err := r.Service.RegisterWorker(ctx, app.RegisterWorkerInput{
		ID:                     valueOrEmpty(id),
		Name:                   name,
		SupportedAgents:        domainAgents(agents),
		WorkDir:                workDir,
		StartupCommand:         valueOrEmpty(startupCommand),
		BindingMode:            mode,
		BoundProjectIDs:        append([]string(nil), boundProjectIDs...),
		AgentRuntimeEnv:        fromWorkerAgentRuntimeEnvInputs(agentRuntimeEnv),
		ReplaceAgentRuntimeEnv: true,
		Capabilities:           fromKeyValueInputs(capabilities),
	})
	if err != nil {
		return nil, err
	}
	if connect {
		worker, err = r.Service.WorkerConnected(ctx, worker.ID)
		if err != nil {
			return nil, err
		}
	}
	return toModelWorker(worker), nil
}

func firstID(primary, secondary *string) string {
	if primary != nil {
		return *primary
	}
	if secondary != nil {
		return *secondary
	}
	return ""
}

func domainAgents(agents []model.AgentType) []domain.AgentType {
	out := make([]domain.AgentType, 0, len(agents))
	for _, agent := range agents {
		out = append(out, domain.AgentType(agent))
	}
	return out
}

func taskFilter(filter *model.TaskFilter) app.TaskFilter {
	if filter == nil {
		return app.TaskFilter{}
	}
	out := app.TaskFilter{}
	if filter.Status != nil {
		out.Status = domain.TaskStatus(*filter.Status)
	}
	if filter.ProjectID != nil {
		out.ProjectID = *filter.ProjectID
	}
	if filter.WorkerID != nil {
		out.WorkerID = *filter.WorkerID
	}
	if filter.AgentType != nil {
		out.AgentType = domain.AgentType(*filter.AgentType)
	}
	if filter.IncludeArchived != nil {
		out.IncludeArchived = *filter.IncludeArchived
	}
	return out
}

func workerFilter(filter *model.WorkerFilter) app.WorkerFilter {
	if filter == nil {
		return app.WorkerFilter{}
	}
	out := app.WorkerFilter{}
	if filter.Status != nil {
		out.Status = domain.WorkerStatus(*filter.Status)
	}
	if filter.ProjectID != nil {
		out.ProjectID = *filter.ProjectID
	}
	if filter.AgentType != nil {
		out.AgentType = domain.AgentType(*filter.AgentType)
	}
	if filter.IncludeDisabled != nil {
		out.IncludeDisabled = *filter.IncludeDisabled
	}
	return out
}

func pageInput(page *model.PageInput) app.PageInput {
	if page == nil {
		return app.PageInput{}
	}
	out := app.PageInput{}
	if page.Offset != nil {
		out.Offset = *page.Offset
	}
	if page.Limit != nil {
		out.Limit = *page.Limit
	}
	return out
}

func eventFilter(filter *model.DomainEventFilter) domain.EventFilter {
	if filter == nil {
		return domain.EventFilter{}
	}
	out := domain.EventFilter{}
	if filter.AggregateID != nil {
		out.AggregateID = *filter.AggregateID
	}
	if filter.AggregateType != nil {
		out.AggregateType = *filter.AggregateType
	}
	if filter.EventType != nil {
		out.EventType = *filter.EventType
	}
	return out
}

func boardColumns(tasks []*domain.Task) []*model.BoardColumn {
	columns := []struct {
		status domain.TaskStatus
		title  string
	}{
		{domain.TaskCreated, "Pending"},
		{domain.TaskAssigned, "Assigned"},
		{domain.TaskStarting, "Starting"},
		{domain.TaskRunning, "Running"},
		{domain.TaskWaitingInput, "Waiting"},
		{domain.TaskInterrupting, "Interrupting"},
		{domain.TaskInterrupted, "Interrupted"},
		{domain.TaskFailed, "Failed"},
		{domain.TaskCompleted, "Completed"},
		{domain.TaskArchived, "Archived"},
	}
	out := make([]*model.BoardColumn, 0, len(columns))
	for _, column := range columns {
		var columnTasks []*model.Task
		for _, task := range tasks {
			if task.Status == column.status {
				columnTasks = append(columnTasks, toModelTask(task))
			}
		}
		out = append(out, &model.BoardColumn{ID: string(column.status), Title: column.title, Status: model.TaskStatus(column.status), Tasks: columnTasks})
	}
	return out
}

func subscribeRawEvents(ctx context.Context, service *app.Service, filter domain.EventFilter) <-chan domain.DomainEvent {
	events, unsubscribe := service.SubscribeDomainEvents(ctx, filter)
	out := make(chan domain.DomainEvent, 64)
	go func() {
		defer close(out)
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func subscribeEvents(ctx context.Context, service *app.Service, filter domain.EventFilter) <-chan *model.DomainEvent {
	events := subscribeRawEvents(ctx, service, filter)
	out := make(chan *model.DomainEvent, 64)
	go func() {
		defer close(out)
		for event := range events {
			select {
			case out <- toModelEvent(event):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
