package graph

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/graph/model"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

type TaskReviewProxy interface {
	ProxyTaskReview(ctx context.Context, taskID, method, path string, input any, out any) error
}

type Resolver struct {
	Service     *app.Service
	ReviewProxy TaskReviewProxy
}

func NewResolver(service *app.Service, reviewProxy TaskReviewProxy) *Resolver {
	return &Resolver{Service: service, ReviewProxy: reviewProxy}
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
	startDate := modelTaskDisplayDate(task.StartDate)
	if startDate.IsZero() {
		startDate = modelTaskDisplayDate(task.CreatedAt)
	}
	endDate := modelTaskDisplayDate(task.EndDate)
	if endDate.IsZero() {
		endDate = startDate
	}
	return &model.Task{
		ID:             task.ID,
		Title:          task.Title,
		Description:    task.Description,
		Status:         model.TaskStatus(task.Status),
		ProjectID:      task.ProjectID,
		WorkerID:       optionalString(task.WorkerID),
		AgentType:      agentType,
		AgentConfig:    toModelAgentExecutionConfig(task.AgentConfig),
		BaseBranch:     task.BaseBranch,
		WorktreePath:   optionalString(task.WorktreePath),
		AgentSessionID: optionalString(task.AgentSessionID),
		PreCommands:    append([]string(nil), task.PreCommands...),
		PostCommands:   append([]string(nil), task.PostCommands...),
		Result:         optionalString(task.Result),
		StartDate:      startDate,
		EndDate:        endDate,
		Version:        task.Version,
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
	}
}

func toModelAgentExecutionConfig(config domain.AgentExecutionConfig) *model.AgentExecutionConfig {
	out := &model.AgentExecutionConfig{}
	if config.WorkMode != "" {
		mode := toModelAgentWorkMode(config.WorkMode)
		out.WorkMode = &mode
	}
	if !config.Codex.Empty() {
		out.Codex = &model.CodexExecutionConfig{
			Model:                     optionalString(config.Codex.Model),
			FullAuto:                  config.Codex.FullAuto,
			BypassApprovalsAndSandbox: config.Codex.BypassApprovalsAndSandbox,
		}
		if config.Codex.ReasoningEffort != "" {
			value := toModelCodexReasoningEffort(config.Codex.ReasoningEffort)
			out.Codex.ReasoningEffort = &value
		}
		if config.Codex.SandboxMode != "" {
			value := toModelCodexSandboxMode(config.Codex.SandboxMode)
			out.Codex.SandboxMode = &value
		}
		if config.Codex.ApprovalPolicy != "" {
			value := toModelCodexApprovalPolicy(config.Codex.ApprovalPolicy)
			out.Codex.ApprovalPolicy = &value
		}
	}
	if !config.Claude.Empty() {
		out.Claude = &model.ClaudeExecutionConfig{
			Model: optionalString(config.Claude.Model),
		}
		if config.Claude.Effort != "" {
			value := toModelClaudeEffort(config.Claude.Effort)
			out.Claude.Effort = &value
		}
		if config.Claude.PermissionMode != "" {
			value := toModelClaudePermissionMode(config.Claude.PermissionMode)
			out.Claude.PermissionMode = &value
		}
	}
	return out
}

func fromAgentExecutionConfigInput(input *model.AgentExecutionConfigInput) *domain.AgentExecutionConfig {
	if input == nil {
		return nil
	}
	config := domain.AgentExecutionConfig{}
	if input.WorkMode != nil {
		config.WorkMode = fromModelAgentWorkMode(*input.WorkMode)
	}
	if input.Codex != nil {
		config.Codex.Model = valueOrEmpty(input.Codex.Model)
		if input.Codex.ReasoningEffort != nil {
			config.Codex.ReasoningEffort = fromModelCodexReasoningEffort(*input.Codex.ReasoningEffort)
		}
		if input.Codex.SandboxMode != nil {
			config.Codex.SandboxMode = fromModelCodexSandboxMode(*input.Codex.SandboxMode)
		}
		if input.Codex.ApprovalPolicy != nil {
			config.Codex.ApprovalPolicy = fromModelCodexApprovalPolicy(*input.Codex.ApprovalPolicy)
		}
		if input.Codex.FullAuto != nil {
			config.Codex.FullAuto = *input.Codex.FullAuto
		}
		if input.Codex.BypassApprovalsAndSandbox != nil {
			config.Codex.BypassApprovalsAndSandbox = *input.Codex.BypassApprovalsAndSandbox
		}
	}
	if input.Claude != nil {
		config.Claude.Model = valueOrEmpty(input.Claude.Model)
		if input.Claude.Effort != nil {
			config.Claude.Effort = fromModelClaudeEffort(*input.Claude.Effort)
		}
		if input.Claude.PermissionMode != nil {
			config.Claude.PermissionMode = fromModelClaudePermissionMode(*input.Claude.PermissionMode)
		}
	}
	return &config
}

func modelTaskDisplayDate(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	utc := value.UTC()
	year, month, day := utc.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
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
		CurrentTaskIds:     append([]string(nil), worker.CurrentTaskIDs...),
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

func toModelTaskInteraction(interaction *domain.TaskInteraction) *model.TaskInteraction {
	if interaction == nil {
		return nil
	}
	var responseDecision *model.TaskInteractionDecision
	if interaction.ResponseDecision != "" {
		value := model.TaskInteractionDecision(interaction.ResponseDecision)
		responseDecision = &value
	}
	return &model.TaskInteraction{
		ID:               interaction.ID,
		TaskID:           interaction.TaskID,
		Kind:             model.TaskInteractionKind(interaction.Kind),
		Status:           model.TaskInteractionStatus(interaction.Status),
		Title:            interaction.Title,
		Body:             interaction.Body,
		RawPayload:       interaction.RawPayload,
		AgentSessionID:   optionalString(interaction.AgentSessionID),
		ResponseDecision: responseDecision,
		ResponseMessage:  optionalString(interaction.ResponseMessage),
		ResponsePayload:  interaction.ResponsePayload,
		CreatedAt:        interaction.CreatedAt,
		UpdatedAt:        interaction.UpdatedAt,
	}
}

func toModelTaskInteractions(interactions []domain.TaskInteraction) []*model.TaskInteraction {
	out := make([]*model.TaskInteraction, 0, len(interactions))
	for index := range interactions {
		out = append(out, toModelTaskInteraction(&interactions[index]))
	}
	return out
}

func toModelTaskGitDiff(diff *TaskGitDiffResponse) *model.TaskGitDiff {
	if diff == nil {
		return nil
	}
	files := make([]*model.TaskGitDiffFile, 0, len(diff.Files))
	for _, file := range diff.Files {
		files = append(files, &model.TaskGitDiffFile{
			Path:      file.Path,
			OldPath:   optionalString(file.OldPath),
			Status:    file.Status,
			Staged:    file.Staged,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Patch:     file.Patch,
			Truncated: file.Truncated,
		})
	}
	return &model.TaskGitDiff{
		TaskID:      diff.TaskID,
		Scope:       model.TaskGitDiffScope(diff.Scope),
		BaseRef:     optionalString(diff.BaseRef),
		HeadRef:     optionalString(diff.HeadRef),
		Files:       files,
		Truncated:   diff.Truncated,
		GeneratedAt: diff.GeneratedAt,
	}
}

func toModelTaskGitChangeResult(result *TaskGitChangeResponse) *model.TaskGitChangeResult {
	if result == nil {
		return nil
	}
	return &model.TaskGitChangeResult{
		Ok:     result.OK,
		Backup: toModelTaskGitBackup(result.Backup),
		Diff:   toModelTaskGitDiff(result.Diff),
	}
}

func toModelTaskGitCommandResult(result *TaskGitCommandResponse) *model.TaskGitCommandResult {
	if result == nil {
		return nil
	}
	return &model.TaskGitCommandResult{
		Ok:      result.OK,
		Command: model.TaskGitCommand(result.Command),
		Output:  result.Output,
		HeadRef: optionalString(result.HeadRef),
		BaseRef: optionalString(result.BaseRef),
		Diff:    toModelTaskGitDiff(result.Diff),
	}
}

func toModelTaskGitStatus(status *TaskGitStatusResponse) *model.TaskGitStatus {
	if status == nil {
		return nil
	}
	return &model.TaskGitStatus{
		TaskID:             status.TaskID,
		Remote:             status.Remote,
		Branch:             status.Branch,
		CurrentBranch:      status.CurrentBranch,
		HeadRef:            optionalString(status.HeadRef),
		TargetRef:          optionalString(status.TargetRef),
		Ahead:              status.Ahead,
		Behind:             status.Behind,
		HasStagedChanges:   status.HasStagedChanges,
		HasUnstagedChanges: status.HasUnstagedChanges,
		HasUntrackedFiles:  status.HasUntrackedFiles,
		GeneratedAt:        status.GeneratedAt,
	}
}

func toModelTaskGitBackup(backup *domain.TaskGitBackup) *model.TaskGitBackup {
	if backup == nil {
		return nil
	}
	return &model.TaskGitBackup{
		ID:        backup.ID,
		TaskID:    backup.TaskID,
		Paths:     append([]string(nil), backup.Paths...),
		PatchPath: backup.PatchPath,
		CreatedAt: backup.CreatedAt,
	}
}

func toModelTaskGitBackups(backups []domain.TaskGitBackup) []*model.TaskGitBackup {
	out := make([]*model.TaskGitBackup, 0, len(backups))
	for index := range backups {
		out = append(out, toModelTaskGitBackup(&backups[index]))
	}
	return out
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

func toModelAgentWorkMode(value domain.AgentWorkMode) model.AgentWorkMode {
	switch value {
	case domain.AgentWorkModePlan:
		return model.AgentWorkModePlan
	case domain.AgentWorkModeReview:
		return model.AgentWorkModeReview
	default:
		return model.AgentWorkModeImplement
	}
}

func fromModelAgentWorkMode(value model.AgentWorkMode) domain.AgentWorkMode {
	switch value {
	case model.AgentWorkModePlan:
		return domain.AgentWorkModePlan
	case model.AgentWorkModeReview:
		return domain.AgentWorkModeReview
	default:
		return domain.AgentWorkModeImplement
	}
}

func toModelCodexReasoningEffort(value domain.CodexReasoningEffort) model.CodexReasoningEffort {
	switch value {
	case domain.CodexReasoningMinimal:
		return model.CodexReasoningEffortMinimal
	case domain.CodexReasoningLow:
		return model.CodexReasoningEffortLow
	case domain.CodexReasoningHigh:
		return model.CodexReasoningEffortHigh
	case domain.CodexReasoningXHigh:
		return model.CodexReasoningEffortXhigh
	default:
		return model.CodexReasoningEffortMedium
	}
}

func fromModelCodexReasoningEffort(value model.CodexReasoningEffort) domain.CodexReasoningEffort {
	switch value {
	case model.CodexReasoningEffortMinimal:
		return domain.CodexReasoningMinimal
	case model.CodexReasoningEffortLow:
		return domain.CodexReasoningLow
	case model.CodexReasoningEffortHigh:
		return domain.CodexReasoningHigh
	case model.CodexReasoningEffortXhigh:
		return domain.CodexReasoningXHigh
	default:
		return domain.CodexReasoningMedium
	}
}

func toModelCodexSandboxMode(value domain.CodexSandboxMode) model.CodexSandboxMode {
	switch value {
	case domain.CodexSandboxReadOnly:
		return model.CodexSandboxModeReadOnly
	case domain.CodexSandboxDangerFullAccess:
		return model.CodexSandboxModeDangerFullAccess
	default:
		return model.CodexSandboxModeWorkspaceWrite
	}
}

func fromModelCodexSandboxMode(value model.CodexSandboxMode) domain.CodexSandboxMode {
	switch value {
	case model.CodexSandboxModeReadOnly:
		return domain.CodexSandboxReadOnly
	case model.CodexSandboxModeDangerFullAccess:
		return domain.CodexSandboxDangerFullAccess
	default:
		return domain.CodexSandboxWorkspaceWrite
	}
}

func toModelCodexApprovalPolicy(value domain.CodexApprovalPolicy) model.CodexApprovalPolicy {
	switch value {
	case domain.CodexApprovalUntrusted:
		return model.CodexApprovalPolicyUntrusted
	case domain.CodexApprovalOnFailure:
		return model.CodexApprovalPolicyOnFailure
	case domain.CodexApprovalNever:
		return model.CodexApprovalPolicyNever
	default:
		return model.CodexApprovalPolicyOnRequest
	}
}

func fromModelCodexApprovalPolicy(value model.CodexApprovalPolicy) domain.CodexApprovalPolicy {
	switch value {
	case model.CodexApprovalPolicyUntrusted:
		return domain.CodexApprovalUntrusted
	case model.CodexApprovalPolicyOnFailure:
		return domain.CodexApprovalOnFailure
	case model.CodexApprovalPolicyNever:
		return domain.CodexApprovalNever
	default:
		return domain.CodexApprovalOnRequest
	}
}

func toModelClaudeEffort(value domain.ClaudeEffort) model.ClaudeEffort {
	switch value {
	case domain.ClaudeEffortLow:
		return model.ClaudeEffortLow
	case domain.ClaudeEffortHigh:
		return model.ClaudeEffortHigh
	case domain.ClaudeEffortXHigh:
		return model.ClaudeEffortXhigh
	case domain.ClaudeEffortMax:
		return model.ClaudeEffortMax
	default:
		return model.ClaudeEffortMedium
	}
}

func fromModelClaudeEffort(value model.ClaudeEffort) domain.ClaudeEffort {
	switch value {
	case model.ClaudeEffortLow:
		return domain.ClaudeEffortLow
	case model.ClaudeEffortHigh:
		return domain.ClaudeEffortHigh
	case model.ClaudeEffortXhigh:
		return domain.ClaudeEffortXHigh
	case model.ClaudeEffortMax:
		return domain.ClaudeEffortMax
	default:
		return domain.ClaudeEffortMedium
	}
}

func toModelClaudePermissionMode(value domain.ClaudePermissionMode) model.ClaudePermissionMode {
	switch value {
	case domain.ClaudePermissionAcceptEdits:
		return model.ClaudePermissionModeAcceptEdits
	case domain.ClaudePermissionAuto:
		return model.ClaudePermissionModeAuto
	case domain.ClaudePermissionBypassPermissions:
		return model.ClaudePermissionModeBypassPermissions
	case domain.ClaudePermissionDontAsk:
		return model.ClaudePermissionModeDontAsk
	case domain.ClaudePermissionPlan:
		return model.ClaudePermissionModePlan
	default:
		return model.ClaudePermissionModeDefault
	}
}

func fromModelClaudePermissionMode(value model.ClaudePermissionMode) domain.ClaudePermissionMode {
	switch value {
	case model.ClaudePermissionModeAcceptEdits:
		return domain.ClaudePermissionAcceptEdits
	case model.ClaudePermissionModeAuto:
		return domain.ClaudePermissionAuto
	case model.ClaudePermissionModeBypassPermissions:
		return domain.ClaudePermissionBypassPermissions
	case model.ClaudePermissionModeDontAsk:
		return domain.ClaudePermissionDontAsk
	case model.ClaudePermissionModePlan:
		return domain.ClaudePermissionPlan
	default:
		return domain.ClaudePermissionDefault
	}
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
	if filter.Search != nil {
		out.Search = *filter.Search
	}
	return out
}

func taskSort(sort *model.TaskSortInput) app.TaskSort {
	if sort == nil {
		return app.TaskSort{}
	}
	out := app.TaskSort{}
	if sort.Field != nil {
		out.Field = app.TaskSortField(*sort.Field)
	}
	if sort.Direction != nil {
		out.Direction = app.SortDirection(*sort.Direction)
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
	if filter.Search != nil {
		out.Search = *filter.Search
	}
	return out
}

func workerSort(sort *model.WorkerSortInput) app.WorkerSort {
	if sort == nil {
		return app.WorkerSort{}
	}
	out := app.WorkerSort{}
	if sort.Field != nil {
		out.Field = app.WorkerSortField(*sort.Field)
	}
	if sort.Direction != nil {
		out.Direction = app.SortDirection(*sort.Direction)
	}
	return out
}

func projectFilter(filter *model.ProjectFilter) app.ProjectFilter {
	if filter == nil {
		return app.ProjectFilter{}
	}
	out := app.ProjectFilter{}
	if filter.IncludeArchived != nil {
		out.IncludeArchived = *filter.IncludeArchived
	}
	if filter.Search != nil {
		out.Search = *filter.Search
	}
	return out
}

func projectSort(sort *model.ProjectSortInput) app.ProjectSort {
	if sort == nil {
		return app.ProjectSort{}
	}
	out := app.ProjectSort{}
	if sort.Field != nil {
		out.Field = app.ProjectSortField(*sort.Field)
	}
	if sort.Direction != nil {
		out.Direction = app.SortDirection(*sort.Direction)
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
	if filter.Search != nil {
		out.Search = *filter.Search
	}
	return out
}

func domainEventSort(sort *model.DomainEventSortInput) app.DomainEventSort {
	if sort == nil {
		return app.DomainEventSort{}
	}
	out := app.DomainEventSort{}
	if sort.Field != nil {
		out.Field = app.DomainEventSortField(*sort.Field)
	}
	if sort.Direction != nil {
		out.Direction = app.SortDirection(*sort.Direction)
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

func taskPageNewestFirst(tasks []*domain.Task, page app.PageInput) ([]*domain.Task, int) {
	sorted := append([]*domain.Task(nil), tasks...)
	slices.SortFunc(sorted, func(a, b *domain.Task) int {
		if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(b.ID, a.ID)
	})
	pageItems, total := paginateResolverItems(sorted, page)
	return pageItems, total
}

func paginateResolverItems[T any](items []T, page app.PageInput) ([]T, int) {
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
