package domain

import "time"

type Settings struct {
	ID                  string               `json:"id"`
	Version             int                  `json:"version"`
	AgentRuntimeEnvVars []AgentRuntimeEnvVar `json:"agentRuntimeEnvVars"`
	WorkerHeartbeat     string               `json:"workerHeartbeatTimeout"`
	SecurityPolicy      string               `json:"securityPolicy"`
	CreatedAt           time.Time            `json:"createdAt"`
	UpdatedAt           time.Time            `json:"updatedAt"`

	pendingEvents []DomainEvent
}

type AgentRuntimeEnvVar struct {
	Key         string `json:"key"`
	Value       string `json:"-"`
	ValueMasked string `json:"valueMasked"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Sensitive   bool   `json:"sensitive"`
}

func NewSettings(now time.Time) *Settings {
	return &Settings{
		ID:              "settings",
		Version:         1,
		WorkerHeartbeat: "90s",
		SecurityPolicy:  "TRUSTED",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func (s *Settings) UpdateAgentRuntimeEnvVars(vars []AgentRuntimeEnvVar, now time.Time) {
	existing := make(map[string]AgentRuntimeEnvVar, len(s.AgentRuntimeEnvVars))
	for _, item := range s.AgentRuntimeEnvVars {
		existing[item.Key] = item
	}
	next := make([]AgentRuntimeEnvVar, 0, len(vars))
	for _, item := range vars {
		if item.Value == "" {
			if prior, ok := existing[item.Key]; ok && prior.Sensitive {
				item.Value = prior.Value
			}
		}
		next = append(next, item)
	}
	s.AgentRuntimeEnvVars = next
	s.Version++
	s.UpdatedAt = now
	s.pendingEvents = append(s.pendingEvents, newEvent("AgentRuntimeEnvUpdated", "Settings", s.ID, s.Version, map[string]any{"count": len(vars)}, now))
}

func (s *Settings) UpdateWorkerHeartbeatTimeout(timeout string, now time.Time) {
	if timeout == "" {
		timeout = "90s"
	}
	s.WorkerHeartbeat = timeout
	s.Version++
	s.UpdatedAt = now
	s.pendingEvents = append(s.pendingEvents, newEvent("WorkerHeartbeatTimeoutUpdated", "Settings", s.ID, s.Version, map[string]any{"timeout": timeout}, now))
}

func (s Settings) MaskedEnvVars() []AgentRuntimeEnvVar {
	masked := make([]AgentRuntimeEnvVar, 0, len(s.AgentRuntimeEnvVars))
	for _, item := range s.AgentRuntimeEnvVars {
		out := item
		if item.Sensitive {
			out.ValueMasked = "********"
		} else {
			out.ValueMasked = item.Value
		}
		out.Value = ""
		masked = append(masked, out)
	}
	return masked
}

func (s Settings) EnabledRuntimeEnv() map[string]string {
	env := make(map[string]string)
	for _, item := range s.AgentRuntimeEnvVars {
		if item.Enabled {
			env[item.Key] = item.Value
		}
	}
	return env
}

func (s *Settings) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), s.pendingEvents...)
	s.pendingEvents = nil
	return events
}
