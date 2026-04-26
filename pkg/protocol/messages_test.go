package protocol

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestProjectPayloadDoesNotExposeSetupCommands(t *testing.T) {
	if _, ok := reflect.TypeOf(ProjectPayload{}).FieldByName("SetupCommands"); ok {
		t.Fatal("ProjectPayload should not expose setup commands")
	}
}

func TestTaskContinuePayloadAndWorkerEventCarryAgentSessionID(t *testing.T) {
	payload := TaskContinuePayload{
		Task:           TaskPayload{ID: "task-1", AgentType: domain.AgentCodex},
		Message:        "follow up",
		AgentSessionID: "session-1",
		WorktreePath:   "/tmp/worktree",
		AgentRuntimeEnv: []RuntimeEnvVar{{
			Key:       "TOKEN",
			Value:     "secret",
			Sensitive: true,
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["message"] != "follow up" || decoded["agentSessionId"] != "session-1" || decoded["worktreePath"] != "/tmp/worktree" {
		t.Fatalf("continue payload JSON = %#v", decoded)
	}

	eventRaw, err := json.Marshal(WorkerEvent{Type: MessageTaskCompleted, TaskID: "task-1", Result: "done", AgentSessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	decoded = map[string]any{}
	if err := json.Unmarshal(eventRaw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["agentSessionId"] != "session-1" {
		t.Fatalf("worker event JSON = %#v", decoded)
	}
}
