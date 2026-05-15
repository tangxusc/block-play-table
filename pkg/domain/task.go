package domain

import (
	"fmt"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskCreated      TaskStatus = "CREATED"
	TaskAssigned     TaskStatus = "ASSIGNED"
	TaskStarting     TaskStatus = "STARTING"
	TaskRunning      TaskStatus = "RUNNING"
	TaskWaitingInput TaskStatus = "WAITING_INPUT"
	TaskInterrupting TaskStatus = "INTERRUPTING"
	TaskInterrupted  TaskStatus = "INTERRUPTED"
	TaskCompleted    TaskStatus = "COMPLETED"
	TaskFailed       TaskStatus = "FAILED"
	TaskArchived     TaskStatus = "ARCHIVED"
)

type TaskDesiredState string

const (
	TaskDesiredRun       TaskDesiredState = "RUN"
	TaskDesiredInterrupt TaskDesiredState = "INTERRUPT"
)

type TaskDirectiveKind string

const (
	TaskDirectiveContinue    TaskDirectiveKind = "CONTINUE"
	TaskDirectiveInteraction TaskDirectiveKind = "INTERACTION_RESPONSE"
)

type TaskDirective struct {
	ID            string                  `json:"id"`
	Kind          TaskDirectiveKind       `json:"kind"`
	Message       string                  `json:"message,omitempty"`
	InteractionID string                  `json:"interactionId,omitempty"`
	Decision      TaskInteractionDecision `json:"decision,omitempty"`
	Payload       string                  `json:"payload,omitempty"`
	IssuedAt      time.Time               `json:"issuedAt"`
}

type AgentWorkMode string

const (
	AgentWorkModePlan      AgentWorkMode = "plan"
	AgentWorkModeImplement AgentWorkMode = "implement"
	AgentWorkModeReview    AgentWorkMode = "review"
)

func (m AgentWorkMode) Valid() bool {
	return m == "" || m == AgentWorkModePlan || m == AgentWorkModeImplement || m == AgentWorkModeReview
}

type CodexReasoningEffort string

const (
	CodexReasoningMinimal CodexReasoningEffort = "minimal"
	CodexReasoningLow     CodexReasoningEffort = "low"
	CodexReasoningMedium  CodexReasoningEffort = "medium"
	CodexReasoningHigh    CodexReasoningEffort = "high"
	CodexReasoningXHigh   CodexReasoningEffort = "xhigh"
)

func (e CodexReasoningEffort) Valid() bool {
	return e == "" || e == CodexReasoningMinimal || e == CodexReasoningLow || e == CodexReasoningMedium || e == CodexReasoningHigh || e == CodexReasoningXHigh
}

type CodexSandboxMode string

const (
	CodexSandboxReadOnly         CodexSandboxMode = "read-only"
	CodexSandboxWorkspaceWrite   CodexSandboxMode = "workspace-write"
	CodexSandboxDangerFullAccess CodexSandboxMode = "danger-full-access"
)

func (m CodexSandboxMode) Valid() bool {
	return m == "" || m == CodexSandboxReadOnly || m == CodexSandboxWorkspaceWrite || m == CodexSandboxDangerFullAccess
}

type CodexApprovalPolicy string

const (
	CodexApprovalUntrusted CodexApprovalPolicy = "untrusted"
	CodexApprovalOnFailure CodexApprovalPolicy = "on-failure"
	CodexApprovalOnRequest CodexApprovalPolicy = "on-request"
	CodexApprovalNever     CodexApprovalPolicy = "never"
)

func (p CodexApprovalPolicy) Valid() bool {
	return p == "" || p == CodexApprovalUntrusted || p == CodexApprovalOnFailure || p == CodexApprovalOnRequest || p == CodexApprovalNever
}

type ClaudeEffort string

const (
	ClaudeEffortLow    ClaudeEffort = "low"
	ClaudeEffortMedium ClaudeEffort = "medium"
	ClaudeEffortHigh   ClaudeEffort = "high"
	ClaudeEffortXHigh  ClaudeEffort = "xhigh"
	ClaudeEffortMax    ClaudeEffort = "max"
)

func (e ClaudeEffort) Valid() bool {
	return e == "" || e == ClaudeEffortLow || e == ClaudeEffortMedium || e == ClaudeEffortHigh || e == ClaudeEffortXHigh || e == ClaudeEffortMax
}

type ClaudePermissionMode string

const (
	ClaudePermissionAcceptEdits       ClaudePermissionMode = "acceptEdits"
	ClaudePermissionAuto              ClaudePermissionMode = "auto"
	ClaudePermissionBypassPermissions ClaudePermissionMode = "bypassPermissions"
	ClaudePermissionDefault           ClaudePermissionMode = "default"
	ClaudePermissionDontAsk           ClaudePermissionMode = "dontAsk"
	ClaudePermissionPlan              ClaudePermissionMode = "plan"
)

func (m ClaudePermissionMode) Valid() bool {
	return m == "" || m == ClaudePermissionAcceptEdits || m == ClaudePermissionAuto || m == ClaudePermissionBypassPermissions || m == ClaudePermissionDefault || m == ClaudePermissionDontAsk || m == ClaudePermissionPlan
}

type AgentExecutionConfig struct {
	WorkMode AgentWorkMode         `json:"workMode,omitempty"`
	Codex    CodexExecutionConfig  `json:"codex,omitempty"`
	Claude   ClaudeExecutionConfig `json:"claude,omitempty"`
}

type CodexExecutionConfig struct {
	Model                     string               `json:"model,omitempty"`
	ReasoningEffort           CodexReasoningEffort `json:"reasoningEffort,omitempty"`
	SandboxMode               CodexSandboxMode     `json:"sandboxMode,omitempty"`
	ApprovalPolicy            CodexApprovalPolicy  `json:"approvalPolicy,omitempty"`
	FullAuto                  bool                 `json:"fullAuto,omitempty"`
	BypassApprovalsAndSandbox bool                 `json:"bypassApprovalsAndSandbox,omitempty"`
}

type ClaudeExecutionConfig struct {
	Model          string               `json:"model,omitempty"`
	Effort         ClaudeEffort         `json:"effort,omitempty"`
	PermissionMode ClaudePermissionMode `json:"permissionMode,omitempty"`
}

func (c AgentExecutionConfig) Empty() bool {
	return c.WorkMode == "" && c.Codex.Empty() && c.Claude.Empty()
}

func (c AgentExecutionConfig) NormalizedForAgent(agent AgentType) (AgentExecutionConfig, error) {
	normalized := c
	normalized.WorkMode = AgentWorkMode(strings.TrimSpace(string(normalized.WorkMode)))
	normalized.Codex = normalized.Codex.normalized()
	normalized.Claude = normalized.Claude.normalized()
	if err := normalized.ValidateForAgent(agent); err != nil {
		return AgentExecutionConfig{}, err
	}
	if agent != AgentCodex {
		normalized.Codex = CodexExecutionConfig{}
	}
	if agent != AgentClaude {
		normalized.Claude = ClaudeExecutionConfig{}
	}
	return normalized, nil
}

func (c AgentExecutionConfig) ValidateForAgent(agent AgentType) error {
	if !c.WorkMode.Valid() {
		return fmt.Errorf("unsupported agent work mode %q", c.WorkMode)
	}
	if !agent.Valid() {
		return fmt.Errorf("unsupported agent type %q", agent)
	}
	if !c.Codex.Empty() && agent != AgentCodex {
		return fmt.Errorf("%w: codex config cannot be used with agent %s", ErrConflict, agent)
	}
	if !c.Claude.Empty() && agent != AgentClaude {
		return fmt.Errorf("%w: claude config cannot be used with agent %s", ErrConflict, agent)
	}
	if agent == AgentCodex {
		return c.Codex.validate()
	}
	return c.Claude.validate()
}

func (c CodexExecutionConfig) Empty() bool {
	return strings.TrimSpace(c.Model) == "" &&
		c.ReasoningEffort == "" &&
		c.SandboxMode == "" &&
		c.ApprovalPolicy == "" &&
		!c.FullAuto &&
		!c.BypassApprovalsAndSandbox
}

func (c CodexExecutionConfig) normalized() CodexExecutionConfig {
	c.Model = strings.TrimSpace(c.Model)
	c.ReasoningEffort = CodexReasoningEffort(strings.TrimSpace(string(c.ReasoningEffort)))
	c.SandboxMode = CodexSandboxMode(strings.TrimSpace(string(c.SandboxMode)))
	c.ApprovalPolicy = CodexApprovalPolicy(strings.TrimSpace(string(c.ApprovalPolicy)))
	return c
}

func (c CodexExecutionConfig) validate() error {
	if !c.ReasoningEffort.Valid() {
		return fmt.Errorf("unsupported codex reasoning effort %q", c.ReasoningEffort)
	}
	if !c.SandboxMode.Valid() {
		return fmt.Errorf("unsupported codex sandbox mode %q", c.SandboxMode)
	}
	if !c.ApprovalPolicy.Valid() {
		return fmt.Errorf("unsupported codex approval policy %q", c.ApprovalPolicy)
	}
	return nil
}

func (c ClaudeExecutionConfig) Empty() bool {
	return strings.TrimSpace(c.Model) == "" && c.Effort == "" && c.PermissionMode == ""
}

func (c ClaudeExecutionConfig) normalized() ClaudeExecutionConfig {
	c.Model = strings.TrimSpace(c.Model)
	c.Effort = ClaudeEffort(strings.TrimSpace(string(c.Effort)))
	c.PermissionMode = ClaudePermissionMode(strings.TrimSpace(string(c.PermissionMode)))
	return c
}

func (c ClaudeExecutionConfig) validate() error {
	if !c.Effort.Valid() {
		return fmt.Errorf("unsupported claude effort %q", c.Effort)
	}
	if !c.PermissionMode.Valid() {
		return fmt.Errorf("unsupported claude permission mode %q", c.PermissionMode)
	}
	return nil
}

type Task struct {
	ID                string               `json:"id"`
	Title             string               `json:"title"`
	Description       string               `json:"description"`
	Status            TaskStatus           `json:"status"`
	DesiredState      TaskDesiredState     `json:"desiredState"`
	PendingDirective  *TaskDirective       `json:"pendingDirective,omitempty"`
	ProjectID         string               `json:"projectId"`
	WorkerID          string               `json:"workerId,omitempty"`
	AgentType         AgentType            `json:"agentType"`
	AgentConfig       AgentExecutionConfig `json:"agentConfig"`
	BaseBranch        string               `json:"baseBranch"`
	WorktreePath      string               `json:"worktreePath,omitempty"`
	AgentSessionID    string               `json:"agentSessionId,omitempty"`
	PreCommands       []string             `json:"preCommands"`
	PostCommands      []string             `json:"postCommands"`
	Result            string               `json:"result,omitempty"`
	StartDate         time.Time            `json:"startDate"`
	EndDate           time.Time            `json:"endDate"`
	Version           int                  `json:"version"`
	CreatedAt         time.Time            `json:"createdAt"`
	UpdatedAt         time.Time            `json:"updatedAt"`

	pendingEvents []DomainEvent
}

type NewTaskInput struct {
	ID           string
	Title        string
	Description  string
	ProjectID    string
	AgentType    AgentType
	BaseBranch   string
	PreCommands  []string
	PostCommands []string
	StartDate    time.Time
	EndDate      time.Time
	Now          time.Time
}

func NewTask(input NewTaskInput) (*Task, error) {
	if err := requireNonBlank("task id", input.ID); err != nil {
		return nil, err
	}
	if err := requireNonBlank("task title", input.Title); err != nil {
		return nil, err
	}
	if err := requireNonBlank("project id", input.ProjectID); err != nil {
		return nil, err
	}
	if input.AgentType != "" && !input.AgentType.Valid() {
		return nil, fmt.Errorf("unsupported agent type %q", input.AgentType)
	}
	if input.BaseBranch == "" {
		input.BaseBranch = "main"
	}
	startDate := defaultTaskDisplayDate(input.StartDate, input.Now)
	endDate := defaultTaskDisplayDate(input.EndDate, input.Now)
	if err := validateTaskDisplayDates(startDate, endDate); err != nil {
		return nil, err
	}
	task := &Task{
		ID:           input.ID,
		Title:        input.Title,
		Description:  input.Description,
		Status:       TaskCreated,
		DesiredState: TaskDesiredRun,
		ProjectID:    input.ProjectID,
		AgentType:    input.AgentType,
		BaseBranch:   input.BaseBranch,
		PreCommands:  append([]string(nil), input.PreCommands...),
		PostCommands: append([]string(nil), input.PostCommands...),
		StartDate:    startDate,
		EndDate:      endDate,
		Version:      1,
		CreatedAt:    input.Now,
		UpdatedAt:    input.Now,
	}
	task.addEvent("TaskCreated", map[string]any{"title": task.Title, "projectId": task.ProjectID, "startDate": task.StartDate, "endDate": task.EndDate}, input.Now)
	return task, nil
}

func (t *Task) AssignWorker(workerID string, now time.Time) error {
	return t.AssignWorkerWithAgent(workerID, "", now)
}

func (t *Task) AssignWorkerWithAgent(workerID string, agent AgentType, now time.Time) error {
	return t.AssignWorkerWithAgentConfig(workerID, agent, nil, now)
}

func (t *Task) AssignWorkerWithAgentConfig(workerID string, agent AgentType, config *AgentExecutionConfig, now time.Time) error {
	if t.Status != TaskCreated && t.Status != TaskAssigned {
		return fmt.Errorf("%w: assign worker from %s", ErrInvalidTransition, t.Status)
	}
	if err := requireNonBlank("worker id", workerID); err != nil {
		return err
	}
	if t.AgentType == "" {
		if !agent.Valid() {
			return fmt.Errorf("agent type is required for assignment")
		}
		t.AgentType = agent
	} else if agent != "" && agent != t.AgentType {
		return fmt.Errorf("%w: task agent %s does not match assignment agent %s", ErrConflict, t.AgentType, agent)
	}
	if config != nil {
		normalized, err := config.NormalizedForAgent(t.AgentType)
		if err != nil {
			return err
		}
		t.AgentConfig = normalized
	} else if err := t.AgentConfig.ValidateForAgent(t.AgentType); err != nil {
		return err
	}
	t.WorkerID = workerID
	payload := map[string]any{"workerId": workerID}
	if t.AgentType != "" {
		payload["agentType"] = t.AgentType
	}
	if config != nil && !t.AgentConfig.Empty() {
		payload["agentConfig"] = t.AgentConfig
	}
	t.transition(TaskAssigned, now, "TaskAssigned", payload)
	return nil
}

func (t *Task) Update(input NewTaskInput) error {
	if t.Status != TaskCreated && t.Status != TaskAssigned {
		return fmt.Errorf("%w: update task from %s", ErrInvalidTransition, t.Status)
	}
	if err := requireNonBlank("task title", input.Title); err != nil {
		return err
	}
	if err := requireNonBlank("project id", input.ProjectID); err != nil {
		return err
	}
	if input.AgentType != "" && !input.AgentType.Valid() {
		return fmt.Errorf("unsupported agent type %q", input.AgentType)
	}
	if input.BaseBranch == "" {
		input.BaseBranch = "main"
	}
	startDate := taskDisplayDate(input.StartDate)
	if startDate.IsZero() {
		startDate = defaultTaskDisplayDate(t.StartDate, input.Now)
	}
	endDate := taskDisplayDate(input.EndDate)
	if endDate.IsZero() {
		endDate = defaultTaskDisplayDate(t.EndDate, input.Now)
	}
	if err := validateTaskDisplayDates(startDate, endDate); err != nil {
		return err
	}
	if !t.AgentConfig.Empty() {
		if err := t.AgentConfig.ValidateForAgent(input.AgentType); err != nil {
			return err
		}
	}
	t.Title = input.Title
	t.Description = input.Description
	t.ProjectID = input.ProjectID
	t.AgentType = input.AgentType
	t.BaseBranch = input.BaseBranch
	t.PreCommands = append([]string(nil), input.PreCommands...)
	t.PostCommands = append([]string(nil), input.PostCommands...)
	t.StartDate = startDate
	t.EndDate = endDate
	t.touch(input.Now)
	t.addEvent("TaskUpdated", map[string]any{"title": t.Title, "projectId": t.ProjectID, "startDate": t.StartDate, "endDate": t.EndDate}, input.Now)
	return nil
}

func (t *Task) Start(now time.Time) error {
	if t.Status != TaskAssigned {
		return fmt.Errorf("%w: start from %s", ErrInvalidTransition, t.Status)
	}
	t.transition(TaskStarting, now, "TaskStartRequested", map[string]any{"workerId": t.WorkerID})
	return nil
}

func (t *Task) Retry(now time.Time) error {
	switch t.Status {
	case TaskCompleted, TaskFailed, TaskInterrupted:
		t.Status = TaskCreated
		t.WorkerID = ""
		t.AgentConfig = AgentExecutionConfig{}
		t.WorktreePath = ""
		t.AgentSessionID = ""
		t.Result = ""
		t.touch(now)
		t.addEvent("TaskRetried", nil, now)
		return nil
	default:
		return fmt.Errorf("%w: retry from %s", ErrInvalidTransition, t.Status)
	}
}

func (t *Task) Continue(now time.Time) error {
	if t.Status != TaskCompleted {
		return fmt.Errorf("%w: continue from %s", ErrInvalidTransition, t.Status)
	}
	if t.WorkerID == "" {
		return fmt.Errorf("%w: continue task %s without worker", ErrConflict, t.ID)
	}
	if t.WorktreePath == "" {
		return fmt.Errorf("%w: continue task %s without worktree", ErrConflict, t.ID)
	}
	if t.AgentSessionID == "" {
		return fmt.Errorf("%w: continue task %s without agent session", ErrConflict, t.ID)
	}
	t.Result = ""
	t.DesiredState = TaskDesiredRun
	t.transition(TaskStarting, now, "TaskContinueRequested", map[string]any{"workerId": t.WorkerID, "agentSessionId": t.AgentSessionID})
	return nil
}

func (t *Task) MarkRunning(worktreePath string, now time.Time) error {
	if t.Status != TaskStarting {
		return fmt.Errorf("%w: mark running from %s", ErrInvalidTransition, t.Status)
	}
	t.WorktreePath = worktreePath
	t.transition(TaskRunning, now, "TaskStarted", map[string]any{"worktreePath": worktreePath})
	return nil
}

func (t *Task) AppendLog(stream, content string, now time.Time) error {
	if !t.canReceiveRuntimeEvent() {
		return fmt.Errorf("%w: append log from %s", ErrInvalidTransition, t.Status)
	}
	t.touch(now)
	t.addEvent("TaskLogAppended", map[string]any{"stream": stream, "content": content}, now)
	return nil
}

func (t *Task) AppendConversation(role, content string, now time.Time) error {
	if !t.canReceiveRuntimeEvent() {
		return fmt.Errorf("%w: append conversation from %s", ErrInvalidTransition, t.Status)
	}
	t.touch(now)
	t.addEvent("TaskConversationAppended", map[string]any{"role": role, "content": content}, now)
	return nil
}

func (t *Task) AppendUserConversation(content string, now time.Time) error {
	if t.Status != TaskCompleted {
		return fmt.Errorf("%w: append user conversation from %s", ErrInvalidTransition, t.Status)
	}
	t.touch(now)
	t.addEvent("TaskConversationAppended", map[string]any{"role": "user", "content": content}, now)
	return nil
}

func (t *Task) WaitForInput(now time.Time) error {
	if t.Status != TaskRunning {
		return fmt.Errorf("%w: wait input from %s", ErrInvalidTransition, t.Status)
	}
	t.transition(TaskWaitingInput, now, "TaskWaitingInput", nil)
	return nil
}

func (t *Task) RequestInteraction(interactionID string, kind TaskInteractionKind, title string, now time.Time, agentSessionIDs ...string) error {
	if t.Status != TaskRunning && t.Status != TaskWaitingInput {
		return fmt.Errorf("%w: request interaction from %s", ErrInvalidTransition, t.Status)
	}
	t.setAgentSessionID(agentSessionIDs...)
	payload := map[string]any{
		"interactionId":  interactionID,
		"kind":           kind,
		"title":          title,
		"agentSessionId": t.AgentSessionID,
	}
	if t.Status == TaskRunning {
		t.Status = TaskWaitingInput
	}
	t.touch(now)
	t.addEvent("TaskInteractionRequested", payload, now)
	return nil
}

func (t *Task) RecordInteractionAnswered(interactionID string, decision TaskInteractionDecision, now time.Time) error {
	if !t.canReceiveRuntimeEvent() {
		return fmt.Errorf("%w: answer interaction from %s", ErrInvalidTransition, t.Status)
	}
	t.touch(now)
	t.addEvent("TaskInteractionAnswered", map[string]any{"interactionId": interactionID, "decision": decision}, now)
	return nil
}

func (t *Task) RecordInteractionResolved(interactionID string, now time.Time) error {
	if !t.canReceiveRuntimeEvent() {
		return fmt.Errorf("%w: resolve interaction from %s", ErrInvalidTransition, t.Status)
	}
	t.touch(now)
	t.addEvent("TaskInteractionResolved", map[string]any{"interactionId": interactionID}, now)
	return nil
}

func (t *Task) Resume(now time.Time) error {
	if t.Status != TaskWaitingInput {
		return fmt.Errorf("%w: resume from %s", ErrInvalidTransition, t.Status)
	}
	t.transition(TaskRunning, now, "TaskResumed", nil)
	return nil
}

func (t *Task) RequestInterrupt(now time.Time) error {
	if t.Status != TaskRunning && t.Status != TaskWaitingInput && t.Status != TaskStarting {
		return fmt.Errorf("%w: interrupt from %s", ErrInvalidTransition, t.Status)
	}
	t.DesiredState = TaskDesiredInterrupt
	t.transition(TaskInterrupting, now, "TaskInterruptRequested", nil)
	return nil
}

func (t *Task) QueueContinueDirective(directiveID, message string, now time.Time) error {
	if t.Status != TaskStarting {
		return fmt.Errorf("%w: queue continue directive from %s", ErrInvalidTransition, t.Status)
	}
	if err := requireNonBlank("directive id", directiveID); err != nil {
		return err
	}
	t.PendingDirective = &TaskDirective{
		ID:       directiveID,
		Kind:     TaskDirectiveContinue,
		Message:  message,
		IssuedAt: now,
	}
	t.touch(now)
	t.addEvent("TaskDirectiveQueued", map[string]any{
		"directiveId": directiveID,
		"kind":        TaskDirectiveContinue,
	}, now)
	return nil
}

func (t *Task) QueueInteractionDirective(directiveID, interactionID string, decision TaskInteractionDecision, message, payload string, now time.Time) error {
	if !t.canReceiveRuntimeEvent() {
		return fmt.Errorf("%w: queue interaction directive from %s", ErrInvalidTransition, t.Status)
	}
	if err := requireNonBlank("directive id", directiveID); err != nil {
		return err
	}
	if err := requireNonBlank("interaction id", interactionID); err != nil {
		return err
	}
	t.PendingDirective = &TaskDirective{
		ID:            directiveID,
		Kind:          TaskDirectiveInteraction,
		InteractionID: interactionID,
		Decision:      decision,
		Message:       message,
		Payload:       payload,
		IssuedAt:      now,
	}
	t.touch(now)
	t.addEvent("TaskDirectiveQueued", map[string]any{
		"directiveId":   directiveID,
		"kind":          TaskDirectiveInteraction,
		"interactionId": interactionID,
		"decision":      decision,
	}, now)
	return nil
}

func (t *Task) AckDirective(directiveID string, now time.Time) error {
	if t.PendingDirective == nil || t.PendingDirective.ID != directiveID {
		return nil
	}
	kind := t.PendingDirective.Kind
	t.PendingDirective = nil
	t.touch(now)
	t.addEvent("TaskDirectiveAcked", map[string]any{
		"directiveId": directiveID,
		"kind":        kind,
	}, now)
	return nil
}

func (t *Task) MarkInterrupted(now time.Time) error {
	if t.Status != TaskInterrupting && t.Status != TaskRunning && t.Status != TaskStarting {
		return fmt.Errorf("%w: mark interrupted from %s", ErrInvalidTransition, t.Status)
	}
	t.transition(TaskInterrupted, now, "TaskInterrupted", nil)
	return nil
}

func (t *Task) Complete(result string, now time.Time, agentSessionIDs ...string) error {
	if t.Status != TaskRunning && t.Status != TaskStarting && t.Status != TaskWaitingInput {
		return fmt.Errorf("%w: complete from %s", ErrInvalidTransition, t.Status)
	}
	t.setAgentSessionID(agentSessionIDs...)
	if result != "" {
		t.Result = result
	}
	t.transition(TaskCompleted, now, "TaskCompleted", map[string]any{"result": t.Result, "agentSessionId": t.AgentSessionID})
	return nil
}

func (t *Task) Fail(reason string, now time.Time) error {
	if t.Status != TaskRunning && t.Status != TaskStarting && t.Status != TaskWaitingInput && t.Status != TaskInterrupting {
		return fmt.Errorf("%w: fail from %s", ErrInvalidTransition, t.Status)
	}
	t.Result = reason
	t.transition(TaskFailed, now, "TaskFailed", map[string]any{"reason": reason})
	return nil
}

func (t *Task) RecordResult(result string, now time.Time, agentSessionIDs ...string) error {
	if !t.canReceiveRuntimeEvent() {
		return fmt.Errorf("%w: record result from %s", ErrInvalidTransition, t.Status)
	}
	t.setAgentSessionID(agentSessionIDs...)
	t.Result = result
	t.touch(now)
	t.addEvent("TaskResultReported", map[string]any{"result": result, "agentSessionId": t.AgentSessionID}, now)
	return nil
}

func (t *Task) RememberAgentSession(agentSessionID string, now time.Time) {
	if agentSessionID == "" || t.AgentSessionID == agentSessionID {
		return
	}
	t.AgentSessionID = agentSessionID
	t.touch(now)
}

func (t *Task) Archive(now time.Time) error {
	switch t.Status {
	case TaskCreated, TaskCompleted, TaskFailed, TaskInterrupted:
		t.transition(TaskArchived, now, "TaskArchived", nil)
		return nil
	default:
		return fmt.Errorf("%w: archive from %s", ErrInvalidTransition, t.Status)
	}
}

func (t *Task) Delete(now time.Time) error {
	if t.Status != TaskArchived {
		return fmt.Errorf("%w: delete from %s", ErrInvalidTransition, t.Status)
	}
	t.touch(now)
	t.addEvent("TaskDeleted", map[string]any{"title": t.Title}, now)
	return nil
}

func (t *Task) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), t.pendingEvents...)
	t.pendingEvents = nil
	return events
}

func (t *Task) RestoreEvents(events []DomainEvent) {
	t.pendingEvents = append([]DomainEvent(nil), events...)
}

func (t *Task) canReceiveRuntimeEvent() bool {
	return t.Status == TaskStarting || t.Status == TaskRunning || t.Status == TaskWaitingInput || t.Status == TaskInterrupting
}

func (t *Task) setAgentSessionID(agentSessionIDs ...string) {
	for _, agentSessionID := range agentSessionIDs {
		if agentSessionID != "" {
			t.AgentSessionID = agentSessionID
			return
		}
	}
}

func (t *Task) transition(status TaskStatus, now time.Time, eventType string, payload any) {
	t.Status = status
	t.touch(now)
	t.addEvent(eventType, payload, now)
}

func (t *Task) touch(now time.Time) {
	t.Version++
	t.UpdatedAt = now
}

func (t *Task) addEvent(eventType string, payload any, now time.Time) {
	t.pendingEvents = append(t.pendingEvents, newEvent(eventType, "Task", t.ID, t.Version, payload, now))
}

func defaultTaskDisplayDate(value, fallback time.Time) time.Time {
	date := taskDisplayDate(value)
	if !date.IsZero() {
		return date
	}
	return taskDisplayDate(fallback)
}

func taskDisplayDate(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	utc := value.UTC()
	year, month, day := utc.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func validateTaskDisplayDates(startDate, endDate time.Time) error {
	if startDate.IsZero() || endDate.IsZero() {
		return nil
	}
	if endDate.Before(startDate) {
		return fmt.Errorf("%w: task endDate before startDate", ErrConflict)
	}
	return nil
}

type TaskLog struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

type ConversationMessage struct {
	ID        string            `json:"id"`
	TaskID    string            `json:"taskId"`
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

type TaskInteractionKind string

const (
	TaskInteractionUserInput          TaskInteractionKind = "USER_INPUT"
	TaskInteractionCommandApproval    TaskInteractionKind = "COMMAND_APPROVAL"
	TaskInteractionFileApproval       TaskInteractionKind = "FILE_APPROVAL"
	TaskInteractionPermissionApproval TaskInteractionKind = "PERMISSION_APPROVAL"
)

func (k TaskInteractionKind) Valid() bool {
	return k == TaskInteractionUserInput ||
		k == TaskInteractionCommandApproval ||
		k == TaskInteractionFileApproval ||
		k == TaskInteractionPermissionApproval
}

type TaskInteractionStatus string

const (
	TaskInteractionPending  TaskInteractionStatus = "PENDING"
	TaskInteractionAnswered TaskInteractionStatus = "ANSWERED"
	TaskInteractionCanceled TaskInteractionStatus = "CANCELED"
)

func (s TaskInteractionStatus) Valid() bool {
	return s == "" || s == TaskInteractionPending || s == TaskInteractionAnswered || s == TaskInteractionCanceled
}

type TaskInteractionDecision string

const (
	TaskInteractionApprove           TaskInteractionDecision = "APPROVE"
	TaskInteractionApproveForSession TaskInteractionDecision = "APPROVE_FOR_SESSION"
	TaskInteractionDeny              TaskInteractionDecision = "DENY"
	TaskInteractionCancel            TaskInteractionDecision = "CANCEL"
)

func (d TaskInteractionDecision) Valid() bool {
	return d == "" ||
		d == TaskInteractionApprove ||
		d == TaskInteractionApproveForSession ||
		d == TaskInteractionDeny ||
		d == TaskInteractionCancel
}

type TaskInteraction struct {
	ID               string                  `json:"id"`
	TaskID           string                  `json:"taskId"`
	Kind             TaskInteractionKind     `json:"kind"`
	Status           TaskInteractionStatus   `json:"status"`
	Title            string                  `json:"title"`
	Body             string                  `json:"body"`
	RawPayload       string                  `json:"rawPayload"`
	AgentSessionID   string                  `json:"agentSessionId,omitempty"`
	ResponseDecision TaskInteractionDecision `json:"responseDecision,omitempty"`
	ResponseMessage  string                  `json:"responseMessage,omitempty"`
	ResponsePayload  string                  `json:"responsePayload,omitempty"`
	CreatedAt        time.Time               `json:"createdAt"`
	UpdatedAt        time.Time               `json:"updatedAt"`
}

func NewTaskInteraction(input TaskInteraction) (*TaskInteraction, error) {
	if err := requireNonBlank("interaction id", input.ID); err != nil {
		return nil, err
	}
	if err := requireNonBlank("task id", input.TaskID); err != nil {
		return nil, err
	}
	if !input.Kind.Valid() {
		return nil, fmt.Errorf("unsupported task interaction kind %q", input.Kind)
	}
	if input.Status == "" {
		input.Status = TaskInteractionPending
	}
	if !input.Status.Valid() {
		return nil, fmt.Errorf("unsupported task interaction status %q", input.Status)
	}
	if !input.ResponseDecision.Valid() {
		return nil, fmt.Errorf("unsupported task interaction decision %q", input.ResponseDecision)
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = input.UpdatedAt
	}
	if input.UpdatedAt.IsZero() {
		input.UpdatedAt = input.CreatedAt
	}
	return &input, nil
}
