package domain

import (
	"fmt"
	"slices"
	"time"
)

type WorkerStatus string

const (
	WorkerRegistered WorkerStatus = "REGISTERED"
	WorkerOnline     WorkerStatus = "ONLINE"
	WorkerOffline    WorkerStatus = "OFFLINE"
	WorkerError      WorkerStatus = "ERROR"
	WorkerDisabled   WorkerStatus = "DISABLED"
)

type WorkerProjectBindingMode string

const (
	WorkerAllProjects      WorkerProjectBindingMode = "ALL_PROJECTS"
	WorkerSpecificProjects WorkerProjectBindingMode = "SPECIFIC_PROJECTS"
)

type Worker struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	Status             WorkerStatus             `json:"status"`
	Capabilities       map[string]string        `json:"capabilities,omitempty"`
	SupportedAgents    []AgentType              `json:"supportedAgents"`
	WorkDir            string                   `json:"workDir"`
	StartupCommand     string                   `json:"startupCommand,omitempty"`
	ProjectBindingMode WorkerProjectBindingMode `json:"projectBindingMode"`
	BoundProjectIDs    []string                 `json:"boundProjectIds"`
	CurrentTaskID      string                   `json:"currentTaskId,omitempty"`
	LastHeartbeatAt    *time.Time               `json:"lastHeartbeatAt,omitempty"`
	Version            int                      `json:"version"`
	CreatedAt          time.Time                `json:"createdAt"`
	UpdatedAt          time.Time                `json:"updatedAt"`

	pendingEvents []DomainEvent
}

type NewWorkerInput struct {
	ID                 string
	Name               string
	SupportedAgents    []AgentType
	WorkDir            string
	StartupCommand     string
	ProjectBindingMode WorkerProjectBindingMode
	BoundProjectIDs    []string
	Capabilities       map[string]string
	Now                time.Time
}

func NewWorker(input NewWorkerInput) (*Worker, error) {
	if err := requireNonBlank("worker id", input.ID); err != nil {
		return nil, err
	}
	if err := requireNonBlank("worker name", input.Name); err != nil {
		return nil, err
	}
	if err := requireNonBlank("work dir", input.WorkDir); err != nil {
		return nil, err
	}
	if len(input.SupportedAgents) == 0 {
		return nil, fmt.Errorf("supported agents is required")
	}
	for _, agent := range input.SupportedAgents {
		if !agent.Valid() {
			return nil, fmt.Errorf("unsupported agent type %q", agent)
		}
	}
	if input.ProjectBindingMode == "" {
		input.ProjectBindingMode = WorkerAllProjects
	}
	worker := &Worker{
		ID:                 input.ID,
		Name:               input.Name,
		Status:             WorkerRegistered,
		Capabilities:       cloneMap(input.Capabilities),
		SupportedAgents:    append([]AgentType(nil), input.SupportedAgents...),
		WorkDir:            input.WorkDir,
		StartupCommand:     input.StartupCommand,
		ProjectBindingMode: input.ProjectBindingMode,
		BoundProjectIDs:    append([]string(nil), input.BoundProjectIDs...),
		Version:            1,
		CreatedAt:          input.Now,
		UpdatedAt:          input.Now,
	}
	worker.addEvent("WorkerRegistered", map[string]any{"name": worker.Name}, input.Now)
	return worker, nil
}

func (w *Worker) Connect(now time.Time) {
	w.Status = WorkerOnline
	w.LastHeartbeatAt = &now
	w.touch(now)
	w.addEvent("WorkerConnected", nil, now)
}

func (w *Worker) Heartbeat(now time.Time) {
	w.LastHeartbeatAt = &now
	w.touch(now)
	w.addEvent("WorkerHeartbeatReceived", nil, now)
}

func (w *Worker) MarkOffline(now time.Time) {
	if w.Status != WorkerDisabled {
		w.Status = WorkerOffline
	}
	w.touch(now)
	w.addEvent("WorkerDisconnected", nil, now)
}

func (w *Worker) Disable(now time.Time) {
	w.Status = WorkerDisabled
	w.touch(now)
	w.addEvent("WorkerDisabled", nil, now)
}

func (w *Worker) Enable(now time.Time) {
	w.Status = WorkerRegistered
	w.touch(now)
	w.addEvent("WorkerEnabled", nil, now)
}

func (w *Worker) Update(input NewWorkerInput) error {
	if err := requireNonBlank("worker name", input.Name); err != nil {
		return err
	}
	if err := requireNonBlank("work dir", input.WorkDir); err != nil {
		return err
	}
	if len(input.SupportedAgents) == 0 {
		return fmt.Errorf("supported agents is required")
	}
	for _, agent := range input.SupportedAgents {
		if !agent.Valid() {
			return fmt.Errorf("unsupported agent type %q", agent)
		}
	}
	if input.ProjectBindingMode == "" {
		input.ProjectBindingMode = WorkerAllProjects
	}
	w.Name = input.Name
	w.Capabilities = cloneMap(input.Capabilities)
	w.SupportedAgents = append([]AgentType(nil), input.SupportedAgents...)
	w.WorkDir = input.WorkDir
	w.StartupCommand = input.StartupCommand
	w.ProjectBindingMode = input.ProjectBindingMode
	if w.ProjectBindingMode == WorkerAllProjects {
		w.BoundProjectIDs = nil
	} else {
		w.BoundProjectIDs = append([]string(nil), input.BoundProjectIDs...)
	}
	w.touch(input.Now)
	w.addEvent("WorkerUpdated", map[string]any{"name": w.Name}, input.Now)
	return nil
}

func (w *Worker) BindProjects(projectIDs []string, now time.Time) {
	w.ProjectBindingMode = WorkerSpecificProjects
	w.BoundProjectIDs = append([]string(nil), projectIDs...)
	w.touch(now)
	w.addEvent("WorkerProjectBindingUpdated", map[string]any{"projectIds": projectIDs}, now)
}

func (w *Worker) ShareAcrossAllProjects(now time.Time) {
	w.ProjectBindingMode = WorkerAllProjects
	w.BoundProjectIDs = nil
	w.touch(now)
	w.addEvent("WorkerProjectBindingUpdated", map[string]any{"mode": WorkerAllProjects}, now)
}

func (w *Worker) CanAcceptTask(agent AgentType, projectID string) bool {
	if w.Status != WorkerOnline || w.CurrentTaskID != "" {
		return false
	}
	if !slices.Contains(w.SupportedAgents, agent) {
		return false
	}
	return w.ProjectBindingMode == WorkerAllProjects || slices.Contains(w.BoundProjectIDs, projectID)
}

func (w *Worker) AssignTask(taskID string, now time.Time) error {
	if w.CurrentTaskID != "" && w.CurrentTaskID != taskID {
		return fmt.Errorf("%w: worker already has task %s", ErrConflict, w.CurrentTaskID)
	}
	w.CurrentTaskID = taskID
	w.touch(now)
	w.addEvent("WorkerTaskAssigned", map[string]any{"taskId": taskID}, now)
	return nil
}

func (w *Worker) ReleaseTask(now time.Time) {
	released := w.CurrentTaskID
	w.CurrentTaskID = ""
	w.touch(now)
	w.addEvent("WorkerTaskReleased", map[string]any{"taskId": released}, now)
}

func (w *Worker) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), w.pendingEvents...)
	w.pendingEvents = nil
	return events
}

func (w *Worker) RestoreEvents(events []DomainEvent) {
	w.pendingEvents = append([]DomainEvent(nil), events...)
}

func (w *Worker) touch(now time.Time) {
	w.Version++
	w.UpdatedAt = now
}

func (w *Worker) addEvent(eventType string, payload any, now time.Time) {
	w.pendingEvents = append(w.pendingEvents, newEvent(eventType, "Worker", w.ID, w.Version, payload, now))
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
