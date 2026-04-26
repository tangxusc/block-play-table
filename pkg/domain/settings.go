package domain

import "time"

type Settings struct {
	ID              string    `json:"id"`
	Version         int       `json:"version"`
	WorkerHeartbeat string    `json:"workerHeartbeatTimeout"`
	SecurityPolicy  string    `json:"securityPolicy"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`

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

func (s *Settings) UpdateWorkerHeartbeatTimeout(timeout string, now time.Time) {
	if timeout == "" {
		timeout = "90s"
	}
	s.WorkerHeartbeat = timeout
	s.Version++
	s.UpdatedAt = now
	s.pendingEvents = append(s.pendingEvents, newEvent("WorkerHeartbeatTimeoutUpdated", "Settings", s.ID, s.Version, map[string]any{"timeout": timeout}, now))
}

func (s *Settings) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), s.pendingEvents...)
	s.pendingEvents = nil
	return events
}
