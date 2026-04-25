package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AgentType string

const (
	AgentCodex  AgentType = "codex"
	AgentClaude AgentType = "claude"
)

func (a AgentType) Valid() bool {
	return a == AgentCodex || a == AgentClaude
}

type Role string

const (
	RoleAdmin     Role = "Admin"
	RoleDeveloper Role = "Developer"
	RoleViewer    Role = "Viewer"
)

type DomainEvent struct {
	EventID          string          `json:"eventId"`
	EventType        string          `json:"eventType"`
	AggregateType    string          `json:"aggregateType"`
	AggregateID      string          `json:"aggregateId"`
	AggregateVersion int             `json:"aggregateVersion"`
	Payload          json.RawMessage `json:"payload"`
	OccurredAt       time.Time       `json:"occurredAt"`
	CorrelationID    string          `json:"correlationId,omitempty"`
	CausationID      string          `json:"causationId,omitempty"`
}

type EventFilter struct {
	AggregateID   string
	AggregateType string
	EventType     string
}

func newEvent(eventType, aggregateType, aggregateID string, version int, payload any, occurredAt time.Time) DomainEvent {
	data, _ := json.Marshal(payload)
	return DomainEvent{
		EventID:          "evt_" + uuid.NewString(),
		EventType:        eventType,
		AggregateType:    aggregateType,
		AggregateID:      aggregateID,
		AggregateVersion: version,
		Payload:          data,
		OccurredAt:       occurredAt,
	}
}

func requireNonBlank(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

var (
	ErrInvalidTransition = errors.New("invalid transition")
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
)
