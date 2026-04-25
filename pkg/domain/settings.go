package domain

import "time"

type Settings struct {
	ID                  string               `json:"id"`
	Version             int                  `json:"version"`
	AgentRuntimeEnvVars []AgentRuntimeEnvVar `json:"agentRuntimeEnvVars"`
	WorkerHeartbeat     string               `json:"workerHeartbeatTimeout"`
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
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func (s *Settings) UpdateAgentRuntimeEnvVars(vars []AgentRuntimeEnvVar, now time.Time) {
	s.AgentRuntimeEnvVars = append([]AgentRuntimeEnvVar(nil), vars...)
	s.Version++
	s.UpdatedAt = now
	s.pendingEvents = append(s.pendingEvents, newEvent("AgentRuntimeEnvUpdated", "Settings", s.ID, s.Version, map[string]any{"count": len(vars)}, now))
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
