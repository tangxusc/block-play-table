package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/httpapi"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestTrustedManagerWorkerFlow(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(httpapi.NewServer(service).Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/worker/ws?worker_id=worker-e2e"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial worker ws: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(protocol.Envelope{
		MessageID: "register-1",
		Type:      protocol.MessageWorkerRegister,
		WorkerID:  "worker-e2e",
		Timestamp: time.Now().UTC(),
		Payload: app.RegisterWorkerInput{
			ID:              "worker-e2e",
			Name:            "e2e worker",
			SupportedAgents: []domain.AgentType{domain.AgentCodex},
			WorkDir:         t.TempDir(),
			BindingMode:     domain.WorkerAllProjects,
		},
	}); err != nil {
		t.Fatalf("register worker: %v", err)
	}

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Repo", "gitUrl": t.TempDir(), "defaultBranch": "main", "worktreeNamePrefix": "repo"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	task := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"title": "E2E", "projectId": projectID, "agentType": "codex", "baseBranch": "main"},
	})
	taskID := task["data"].(map[string]any)["createTask"].(map[string]any)["id"].(string)
	postGraphQL(t, server.URL, `mutation AssignWorker($taskId: ID!, $workerId: ID!) { assignWorker(taskId: $taskId, workerId: $workerId) { id } }`, map[string]any{"taskId": taskID, "workerId": "worker-e2e"})
	postGraphQL(t, server.URL, `mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }`, map[string]any{"taskId": taskID})

	var start rawEnvelope
	if err := conn.ReadJSON(&start); err != nil {
		t.Fatalf("read task start: %v", err)
	}
	if start.Type != protocol.MessageTaskStart || start.TaskID != taskID {
		t.Fatalf("start envelope = %+v", start)
	}
	for _, event := range []protocol.WorkerEvent{
		{MessageID: "started-1", Type: protocol.MessageTaskStarted, TaskID: taskID, Content: "/tmp/worktree"},
		{MessageID: "log-1", Type: protocol.MessageTaskLog, TaskID: taskID, Stream: "stdout", Content: "hello from worker"},
		{MessageID: "done-1", Type: protocol.MessageTaskCompleted, TaskID: taskID, Result: "done"},
	} {
		if err := conn.WriteJSON(protocol.Envelope{MessageID: event.MessageID, Type: event.Type, WorkerID: "worker-e2e", TaskID: taskID, Timestamp: time.Now().UTC(), Payload: event}); err != nil {
			t.Fatalf("write worker event %s: %v", event.Type, err)
		}
	}

	loaded := eventuallyGraphQL(t, server.URL, `query Task($id: ID!) { task(id: $id) { id status } }`, map[string]any{"id": taskID}, func(body map[string]any) bool {
		task := body["data"].(map[string]any)["task"].(map[string]any)
		return task["status"] == string(domain.TaskCompleted)
	})
	if loaded == nil {
		t.Fatal("task did not complete")
	}
	logs := postGraphQL(t, server.URL, `query TaskLogs($taskId: ID!) { taskLogs(taskId: $taskId) { content } }`, map[string]any{"taskId": taskID})
	if got := len(logs["data"].(map[string]any)["taskLogs"].([]any)); got != 1 {
		t.Fatalf("logs count = %d, want 1", got)
	}
}

type rawEnvelope struct {
	MessageID string               `json:"messageId"`
	Type      protocol.MessageType `json:"type"`
	WorkerID  string               `json:"workerId"`
	TaskID    string               `json:"taskId"`
	Payload   json.RawMessage      `json:"payload"`
}

func eventuallyGraphQL(t *testing.T, baseURL, query string, variables map[string]any, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body := postGraphQL(t, baseURL, query, variables)
		if ok(body) {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func postGraphQL(t *testing.T, baseURL, query string, variables map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(baseURL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["errors"] != nil {
		t.Fatalf("graphql errors = %#v", decoded["errors"])
	}
	return decoded
}
