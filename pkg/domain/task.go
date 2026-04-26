package domain

import (
	"fmt"
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

type Task struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Status         TaskStatus `json:"status"`
	ProjectID      string     `json:"projectId"`
	WorkerID       string     `json:"workerId,omitempty"`
	AgentType      AgentType  `json:"agentType"`
	BaseBranch     string     `json:"baseBranch"`
	WorktreePath   string     `json:"worktreePath,omitempty"`
	AgentSessionID string     `json:"agentSessionId,omitempty"`
	PreCommands    []string   `json:"preCommands"`
	PostCommands   []string   `json:"postCommands"`
	Result         string     `json:"result,omitempty"`
	StartDate      time.Time  `json:"startDate"`
	EndDate        time.Time  `json:"endDate"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`

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
	t.WorkerID = workerID
	payload := map[string]any{"workerId": workerID}
	if t.AgentType != "" {
		payload["agentType"] = t.AgentType
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

func (t *Task) Resume(now time.Time) error {
	if t.Status != TaskWaitingInput {
		return fmt.Errorf("%w: resume from %s", ErrInvalidTransition, t.Status)
	}
	t.transition(TaskRunning, now, "TaskStarted", nil)
	return nil
}

func (t *Task) RequestInterrupt(now time.Time) error {
	if t.Status != TaskRunning && t.Status != TaskWaitingInput && t.Status != TaskStarting {
		return fmt.Errorf("%w: interrupt from %s", ErrInvalidTransition, t.Status)
	}
	t.transition(TaskInterrupting, now, "TaskInterruptRequested", nil)
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
