package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServerGraphQLOperationsCoverTrustedModeSurfaces(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "P", "gitUrl": "git://repo", "defaultBranch": "main", "worktreeNamePrefix": "p"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	if got := postGraphQL(t, server.URL, `query { projects { id } }`, nil)["data"].(map[string]any)["projects"].([]any); len(got) != 1 {
		t.Fatalf("projects count = %d, want 1", len(got))
	}

	worker := postGraphQL(t, server.URL, `mutation CreateWorker($input: RegisterWorkerInput!) { createWorker(input: $input) { id status } }`, map[string]any{
		"input": map[string]any{"id": "worker-ops", "name": "W", "supportedAgents": []any{"codex"}, "workDir": "/tmp", "projectBindingMode": "ALL_PROJECTS"},
	})
	if got := worker["data"].(map[string]any)["createWorker"].(map[string]any)["status"]; got != string(domain.WorkerOnline) {
		t.Fatalf("worker status = %v, want ONLINE", got)
	}
	if got := postGraphQL(t, server.URL, `query { workers { id } }`, nil)["data"].(map[string]any)["workers"].([]any); len(got) != 1 {
		t.Fatalf("workers count = %d, want 1", len(got))
	}

	task := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"title": "T", "projectId": projectID, "agentType": "codex"},
	})
	taskID := task["data"].(map[string]any)["createTask"].(map[string]any)["id"].(string)
	_ = postGraphQL(t, server.URL, `mutation AssignWorker($taskId: ID!, $workerId: ID!) { assignWorker(taskId: $taskId, workerId: $workerId) { id } }`, map[string]any{
		"taskId": taskID, "workerId": "worker-ops",
	})
	started := postGraphQL(t, server.URL, `mutation StartTask($id: ID!) { startTask(id: $id) { status } }`, map[string]any{"id": taskID})
	if got := started["data"].(map[string]any)["startTask"].(map[string]any)["status"]; got != string(domain.TaskStarting) {
		t.Fatalf("task status = %v, want STARTING", got)
	}

	if got := postGraphQL(t, server.URL, `query Task($id: ID!) { task(id: $id) { id } }`, map[string]any{"id": taskID})["data"].(map[string]any)["task"].(map[string]any)["id"]; got != taskID {
		t.Fatalf("task id = %v, want %s", got, taskID)
	}
	if logsValue := postGraphQL(t, server.URL, `query TaskLogs($taskId: ID!) { taskLogs(taskId: $taskId) { id } }`, map[string]any{"taskId": taskID})["data"].(map[string]any)["taskLogs"]; logsValue != nil {
		if logs := logsValue.([]any); len(logs) != 0 {
			t.Fatalf("logs count = %d, want 0", len(logs))
		}
	}
	if events := postGraphQL(t, server.URL, `query TaskEvents($taskId: ID!) { taskEvents(taskId: $taskId) { eventType } }`, map[string]any{"taskId": taskID})["data"].(map[string]any)["taskEvents"].([]any); len(events) == 0 {
		t.Fatal("expected task events")
	}
	if got := postGraphQL(t, server.URL, `mutation InterruptTask($taskId: ID!) { interruptTask(taskId: $taskId) { id } }`, map[string]any{"taskId": taskID})["data"].(map[string]any)["interruptTask"].(map[string]any)["id"]; got != taskID {
		t.Fatalf("interrupt task id = %v, want %s", got, taskID)
	}

	settings := postGraphQL(t, server.URL, `mutation UpdateSettings($input: UpdateAgentRuntimeEnvVarsInput!) { updateAgentRuntimeEnvVars(input: $input) { agentRuntimeEnvVars { key valueMasked } } }`, map[string]any{
		"input": map[string]any{"vars": []any{map[string]any{"key": "TOKEN", "value": "secret", "enabled": true, "sensitive": true}}},
	})
	env := settings["data"].(map[string]any)["updateAgentRuntimeEnvVars"].(map[string]any)["agentRuntimeEnvVars"].([]any)
	if env[0].(map[string]any)["valueMasked"] != "********" {
		t.Fatalf("masked settings = %#v", env)
	}
	_ = postGraphQL(t, server.URL, `query { settings { id } }`, nil)
	archived := postGraphQL(t, server.URL, `mutation ArchiveProject($id: ID!) { archiveProject(id: $id) { archived } }`, map[string]any{"id": projectID})
	if archived["data"].(map[string]any)["archiveProject"].(map[string]any)["archived"] != true {
		t.Fatal("project should be archived")
	}
}

func TestServerValidationCORSAndSubscriptions(t *testing.T) {
	server := httptest.NewServer(NewServer(app.NewService(store.NewMemoryStore())).Handler())
	defer server.Close()

	req, err := http.NewRequest(http.MethodOptions, server.URL+"/graphql", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("CORS origin = %q", res.Header.Get("Access-Control-Allow-Origin"))
	}

	res, err = http.Get(server.URL + "/graphql")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /graphql status = %d, want 405", res.StatusCode)
	}

	badJSON, err := http.Post(server.URL+"/graphql", "application/json", bytes.NewBufferString("{"))
	if err != nil {
		t.Fatal(err)
	}
	_ = badJSON.Body.Close()
	if badJSON.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400", badJSON.StatusCode)
	}

	raw := postRawGraphQL(t, server.URL, `query { unsupported }`, nil)
	if raw["errors"] == nil {
		t.Fatalf("unsupported query should return errors: %#v", raw)
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/subscriptions", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("subscription read: %v", err)
	}
	if message["type"] != "KEEPALIVE" {
		t.Fatalf("subscription message = %#v", message)
	}
}

func TestWorkerGatewayAppliesWorkerMessages(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	ctx := context.Background()
	project, err := service.CreateProject(ctx, app.CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, app.CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sendWS(t, conn, protocol.Envelope{MessageID: "reg-1", Type: protocol.MessageWorkerRegister, WorkerID: "worker-ws", Timestamp: time.Now(), Payload: map[string]any{
		"id": "worker-ws", "name": "WS", "supportedAgents": []string{"codex"}, "workDir": "/tmp", "projectBindingMode": "ALL_PROJECTS",
	}})
	sendWS(t, conn, protocol.Envelope{MessageID: "hb-1", Type: protocol.MessageWorkerHeartbeat, WorkerID: "worker-ws", Timestamp: time.Now()})
	deadline := time.Now().Add(2 * time.Second)
	for {
		worker, err := service.Store().Worker(ctx, "worker-ws")
		if err == nil && worker.Status == domain.WorkerOnline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker registration err = %v worker = %+v", err, worker)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := service.AssignWorker(ctx, task.ID, "worker-ws"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	sendWS(t, conn, protocol.Envelope{MessageID: "started-1", Type: protocol.MessageTaskStarted, WorkerID: "worker-ws", TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Content: "/tmp/worktree"}})
	sendWS(t, conn, protocol.Envelope{MessageID: "log-1", Type: protocol.MessageTaskLog, WorkerID: "worker-ws", TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Stream: "stdout", Content: "hello"}})
	sendWS(t, conn, protocol.Envelope{MessageID: "conv-1", Type: protocol.MessageTaskConversation, WorkerID: "worker-ws", TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Content: "done"}})
	sendWS(t, conn, protocol.Envelope{MessageID: "done-1", Type: protocol.MessageTaskCompleted, WorkerID: "worker-ws", TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Result: "completed"}})

	deadline = time.Now().Add(2 * time.Second)
	for {
		loaded, err := service.Task(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == domain.TaskCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task status = %s, want completed", loaded.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if logs, _ := service.Store().TaskLogs(ctx, task.ID); len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if messages, _ := service.Store().TaskConversations(ctx, task.ID); len(messages) != 1 {
		t.Fatalf("conversation count = %d, want 1", len(messages))
	}
}

func postRawGraphQL(t *testing.T, baseURL, query string, variables map[string]any) map[string]any {
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
	return decoded
}

func sendWS(t *testing.T, conn *websocket.Conn, envelope protocol.Envelope) {
	t.Helper()
	if err := conn.WriteJSON(envelope); err != nil {
		t.Fatal(err)
	}
}
