package domain

import (
	"fmt"
	"slices"
	"strings"
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
	AgentRuntimeEnv    []WorkerAgentRuntimeEnv  `json:"agentRuntimeEnv"`
	CurrentTaskIDs     []string                 `json:"currentTaskIds"`
	LastHeartbeatAt    *time.Time               `json:"lastHeartbeatAt,omitempty"`
	Version            int                      `json:"version"`
	CreatedAt          time.Time                `json:"createdAt"`
	UpdatedAt          time.Time                `json:"updatedAt"`

	pendingEvents []DomainEvent
}

type WorkerAgentRuntimeEnv struct {
	AgentType AgentType            `json:"agentType"`
	Vars      []AgentRuntimeEnvVar `json:"vars"`
}

type NewWorkerInput struct {
	ID                     string
	Name                   string
	SupportedAgents        []AgentType
	WorkDir                string
	StartupCommand         string
	ProjectBindingMode     WorkerProjectBindingMode
	BoundProjectIDs        []string
	AgentRuntimeEnv        []WorkerAgentRuntimeEnv
	ReplaceAgentRuntimeEnv bool
	Capabilities           map[string]string
	Now                    time.Time
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
		AgentRuntimeEnv:    normalizeAgentRuntimeEnv(input.AgentRuntimeEnv, nil, input.SupportedAgents),
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
	if input.ReplaceAgentRuntimeEnv {
		w.AgentRuntimeEnv = normalizeAgentRuntimeEnv(input.AgentRuntimeEnv, w.AgentRuntimeEnv, input.SupportedAgents)
	} else {
		w.AgentRuntimeEnv = normalizeAgentRuntimeEnv(w.AgentRuntimeEnv, nil, input.SupportedAgents)
	}
	w.touch(input.Now)
	w.addEvent("WorkerUpdated", map[string]any{"name": w.Name}, input.Now)
	return nil
}

func (w Worker) MaskedAgentRuntimeEnv() []WorkerAgentRuntimeEnv {
	out := cloneAgentRuntimeEnv(w.AgentRuntimeEnv)
	for groupIndex := range out {
		for varIndex := range out[groupIndex].Vars {
			item := &out[groupIndex].Vars[varIndex]
			if item.Sensitive {
				item.ValueMasked = "********"
			} else {
				item.ValueMasked = item.Value
			}
			item.Value = ""
		}
	}
	return out
}

func (w Worker) EnabledRuntimeEnv(agent AgentType) []AgentRuntimeEnvVar {
	for _, group := range w.AgentRuntimeEnv {
		if group.AgentType != agent {
			continue
		}
		out := make([]AgentRuntimeEnvVar, 0, len(group.Vars))
		for _, item := range group.Vars {
			if item.Enabled {
				out = append(out, item)
			}
		}
		return out
	}
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
	if w.Status != WorkerOnline {
		return false
	}
	if !slices.Contains(w.SupportedAgents, agent) {
		return false
	}
	return w.ProjectBindingMode == WorkerAllProjects || slices.Contains(w.BoundProjectIDs, projectID)
}

func (w *Worker) AssignTask(taskID string, now time.Time) error {
	if err := requireNonBlank("task id", taskID); err != nil {
		return err
	}
	if slices.Contains(w.CurrentTaskIDs, taskID) {
		return nil
	}
	w.CurrentTaskIDs = append(w.CurrentTaskIDs, taskID)
	w.touch(now)
	w.addEvent("WorkerTaskAssigned", map[string]any{"taskId": taskID}, now)
	return nil
}

func (w *Worker) ReleaseTask(taskID string, now time.Time) {
	index := slices.Index(w.CurrentTaskIDs, taskID)
	if index < 0 {
		return
	}
	w.CurrentTaskIDs = slices.Delete(w.CurrentTaskIDs, index, index+1)
	w.touch(now)
	w.addEvent("WorkerTaskReleased", map[string]any{"taskId": taskID}, now)
}

func (w *Worker) Delete(now time.Time) {
	w.touch(now)
	w.addEvent("WorkerDeleted", map[string]any{"name": w.Name}, now)
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

func normalizeAgentRuntimeEnv(input, existing []WorkerAgentRuntimeEnv, supportedAgents []AgentType) []WorkerAgentRuntimeEnv {
	supported := make(map[AgentType]struct{}, len(supportedAgents))
	for _, agent := range supportedAgents {
		if agent.Valid() {
			supported[agent] = struct{}{}
		}
	}
	existingVars := make(map[AgentType]map[string]AgentRuntimeEnvVar)
	for _, group := range existing {
		if _, ok := existingVars[group.AgentType]; !ok {
			existingVars[group.AgentType] = map[string]AgentRuntimeEnvVar{}
		}
		for _, item := range group.Vars {
			existingVars[group.AgentType][item.Key] = item
		}
	}
	inputVars := make(map[AgentType][]AgentRuntimeEnvVar)
	for _, group := range input {
		if !group.AgentType.Valid() {
			continue
		}
		if len(supported) > 0 {
			if _, ok := supported[group.AgentType]; !ok {
				continue
			}
		}
		seen := map[string]int{}
		vars := inputVars[group.AgentType]
		for _, item := range group.Vars {
			if strings.TrimSpace(item.Key) == "" {
				continue
			}
			if item.Value == "" && item.Sensitive {
				if prior, ok := existingVars[group.AgentType][item.Key]; ok && prior.Sensitive {
					item.Value = prior.Value
				}
			}
			if index, ok := seen[item.Key]; ok {
				vars[index] = item
				continue
			}
			seen[item.Key] = len(vars)
			vars = append(vars, item)
		}
		inputVars[group.AgentType] = vars
	}
	out := make([]WorkerAgentRuntimeEnv, 0, len(inputVars))
	for _, agent := range supportedAgents {
		vars := inputVars[agent]
		if len(vars) == 0 {
			continue
		}
		out = append(out, WorkerAgentRuntimeEnv{AgentType: agent, Vars: append([]AgentRuntimeEnvVar(nil), vars...)})
	}
	if len(supportedAgents) == 0 {
		for agent, vars := range inputVars {
			if len(vars) > 0 {
				out = append(out, WorkerAgentRuntimeEnv{AgentType: agent, Vars: append([]AgentRuntimeEnvVar(nil), vars...)})
			}
		}
	}
	return out
}

func cloneAgentRuntimeEnv(in []WorkerAgentRuntimeEnv) []WorkerAgentRuntimeEnv {
	if len(in) == 0 {
		return nil
	}
	out := make([]WorkerAgentRuntimeEnv, 0, len(in))
	for _, group := range in {
		copied := WorkerAgentRuntimeEnv{
			AgentType: group.AgentType,
			Vars:      append([]AgentRuntimeEnvVar(nil), group.Vars...),
		}
		out = append(out, copied)
	}
	return out
}
