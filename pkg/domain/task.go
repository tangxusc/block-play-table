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
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Status       TaskStatus `json:"status"`
	ProjectID    string     `json:"projectId"`
	WorkerID     string     `json:"workerId,omitempty"`
	AgentType    AgentType  `json:"agentType"`
	BaseBranch   string     `json:"baseBranch"`
	TargetBranch string     `json:"targetBranch"`
	WorktreePath string     `json:"worktreePath,omitempty"`
	PreCommands  []string   `json:"preCommands"`
	PostCommands []string   `json:"postCommands"`
	Result       string     `json:"result,omitempty"`
	Version      int        `json:"version"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`

	pendingEvents []DomainEvent
}

type NewTaskInput struct {
	ID           string
	Title        string
	Description  string
	ProjectID    string
	AgentType    AgentType
	BaseBranch   string
	TargetBranch string
	PreCommands  []string
	PostCommands []string
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
	if !input.AgentType.Valid() {
		return nil, fmt.Errorf("unsupported agent type %q", input.AgentType)
	}
	if input.BaseBranch == "" {
		input.BaseBranch = "main"
	}
	if input.TargetBranch == "" {
		input.TargetBranch = "task/" + input.ID
	}
	task := &Task{
		ID:           input.ID,
		Title:        input.Title,
		Description:  input.Description,
		Status:       TaskCreated,
		ProjectID:    input.ProjectID,
		AgentType:    input.AgentType,
		BaseBranch:   input.BaseBranch,
		TargetBranch: input.TargetBranch,
		PreCommands:  append([]string(nil), input.PreCommands...),
		PostCommands: append([]string(nil), input.PostCommands...),
		Version:      1,
		CreatedAt:    input.Now,
		UpdatedAt:    input.Now,
	}
	task.addEvent("TaskCreated", map[string]any{"title": task.Title, "projectId": task.ProjectID}, input.Now)
	return task, nil
}

func (t *Task) AssignWorker(workerID string, now time.Time) error {
	if t.Status != TaskCreated && t.Status != TaskAssigned {
		return fmt.Errorf("%w: assign worker from %s", ErrInvalidTransition, t.Status)
	}
	if err := requireNonBlank("worker id", workerID); err != nil {
		return err
	}
	t.WorkerID = workerID
	t.transition(TaskAssigned, now, "TaskAssigned", map[string]any{"workerId": workerID})
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
	if !input.AgentType.Valid() {
		return fmt.Errorf("unsupported agent type %q", input.AgentType)
	}
	if input.BaseBranch == "" {
		input.BaseBranch = "main"
	}
	if input.TargetBranch == "" {
		input.TargetBranch = "task/" + t.ID
	}
	t.Title = input.Title
	t.Description = input.Description
	t.ProjectID = input.ProjectID
	t.AgentType = input.AgentType
	t.BaseBranch = input.BaseBranch
	t.TargetBranch = input.TargetBranch
	t.PreCommands = append([]string(nil), input.PreCommands...)
	t.PostCommands = append([]string(nil), input.PostCommands...)
	t.touch(input.Now)
	t.addEvent("TaskUpdated", map[string]any{"title": t.Title, "projectId": t.ProjectID}, input.Now)
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
		t.Result = ""
		t.touch(now)
		t.addEvent("TaskRetried", nil, now)
		return nil
	default:
		return fmt.Errorf("%w: retry from %s", ErrInvalidTransition, t.Status)
	}
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

func (t *Task) Complete(result string, now time.Time) error {
	if t.Status != TaskRunning && t.Status != TaskStarting && t.Status != TaskWaitingInput {
		return fmt.Errorf("%w: complete from %s", ErrInvalidTransition, t.Status)
	}
	if result != "" {
		t.Result = result
	}
	t.transition(TaskCompleted, now, "TaskCompleted", map[string]any{"result": t.Result})
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

func (t *Task) RecordResult(result string, now time.Time) error {
	if !t.canReceiveRuntimeEvent() {
		return fmt.Errorf("%w: record result from %s", ErrInvalidTransition, t.Status)
	}
	t.Result = result
	t.touch(now)
	t.addEvent("TaskResultReported", map[string]any{"result": result}, now)
	return nil
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
