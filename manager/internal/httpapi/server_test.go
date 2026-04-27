package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServerTrustedGraphQLFlowDoesNotRequireAuthHeaders(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id name } }`, map[string]any{
		"input": map[string]any{"name": "Block Play Table", "gitUrl": "file:///tmp/repo", "defaultBranch": "main", "worktreeNamePrefix": "bpt"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)

	worker := postGraphQL(t, server.URL, `mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }`, map[string]any{
		"input": map[string]any{"id": "worker-1", "name": "local", "supportedAgents": []any{"codex"}, "workDir": "/tmp/worker", "projectBindingMode": "ALL_PROJECTS"},
	})
	if got := worker["data"].(map[string]any)["registerWorker"].(map[string]any)["status"]; got != string(domain.WorkerOnline) {
		t.Fatalf("registered worker status = %v, want ONLINE", got)
	}

	task := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id status startDate endDate } }`, map[string]any{
		"input": map[string]any{"title": "Implement", "projectId": projectID, "agentType": "codex", "baseBranch": "main"},
	})
	createdTask := task["data"].(map[string]any)["createTask"].(map[string]any)
	taskID := createdTask["id"].(string)
	if createdTask["startDate"] != "2026-04-25T00:00:00Z" || createdTask["endDate"] != "2026-04-25T00:00:00Z" {
		t.Fatalf("default task dates = %+v", createdTask)
	}
	assigned := postGraphQL(t, server.URL, `mutation AssignWorker($taskId: ID!, $workerId: ID!) { assignWorker(taskId: $taskId, workerId: $workerId) { status workerId } }`, map[string]any{
		"taskId": taskID, "workerId": "worker-1",
	})
	if got := assigned["data"].(map[string]any)["assignWorker"].(map[string]any)["status"]; got != string(domain.TaskAssigned) {
		t.Fatalf("assigned status = %v, want ASSIGNED", got)
	}

	tasks := postGraphQL(t, server.URL, `query { tasks { nodes { id title status startDate endDate } totalCount } }`, nil)
	if got := int(tasks["data"].(map[string]any)["tasks"].(map[string]any)["totalCount"].(float64)); got != 1 {
		t.Fatalf("tasks count = %d, want 1", got)
	}
	firstTask := tasks["data"].(map[string]any)["tasks"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	if firstTask["startDate"] != "2026-04-25T00:00:00Z" || firstTask["endDate"] != "2026-04-25T00:00:00Z" {
		t.Fatalf("queried task dates = %+v", firstTask)
	}
}

func TestServerGraphQLCreatesAgentlessAndWorkerAssignedTasks(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Block Play Table", "gitUrl": "file:///tmp/repo", "defaultBranch": "main", "worktreeNamePrefix": "bpt"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	postGraphQL(t, server.URL, `mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }`, map[string]any{
		"input": map[string]any{"id": "worker-1", "name": "local", "supportedAgents": []any{"codex"}, "workDir": "/tmp/worker", "projectBindingMode": "ALL_PROJECTS"},
	})

	agentless := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id status agentType workerId } }`, map[string]any{
		"input": map[string]any{"title": "Agentless", "projectId": projectID},
	})
	agentlessTask := agentless["data"].(map[string]any)["createTask"].(map[string]any)
	if got := agentlessTask["status"]; got != string(domain.TaskCreated) {
		t.Fatalf("agentless status = %v, want CREATED", got)
	}
	if got := agentlessTask["agentType"]; got != nil {
		t.Fatalf("agentless agentType = %v, want nil", got)
	}

	assigned := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id status agentType workerId startDate endDate } }`, map[string]any{
		"input": map[string]any{"title": "Assigned", "projectId": projectID, "workerId": "worker-1", "agentType": "codex", "startDate": "2026-05-01T00:00:00Z", "endDate": "2026-05-03T00:00:00Z"},
	})
	assignedTask := assigned["data"].(map[string]any)["createTask"].(map[string]any)
	if got := assignedTask["status"]; got != string(domain.TaskAssigned) {
		t.Fatalf("assigned status = %v, want ASSIGNED", got)
	}
	if got := assignedTask["agentType"]; got != "codex" {
		t.Fatalf("assigned agentType = %v, want codex", got)
	}
	if got := assignedTask["workerId"]; got != "worker-1" {
		t.Fatalf("assigned workerId = %v, want worker-1", got)
	}
	if assignedTask["startDate"] != "2026-05-01T00:00:00Z" || assignedTask["endDate"] != "2026-05-03T00:00:00Z" {
		t.Fatalf("assigned task dates = %+v", assignedTask)
	}
}

func TestServerGraphQLWritesAgentConfigOnCreateAndAssign(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Block Play Table", "gitUrl": "file:///tmp/repo", "defaultBranch": "main", "worktreeNamePrefix": "bpt"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	postGraphQL(t, server.URL, `mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"id": "worker-create", "name": "create", "supportedAgents": []any{"codex"}, "workDir": "/tmp/create", "projectBindingMode": "ALL_PROJECTS"},
	})
	postGraphQL(t, server.URL, `mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"id": "worker-assign", "name": "assign", "supportedAgents": []any{"claude"}, "workDir": "/tmp/assign", "projectBindingMode": "ALL_PROJECTS"},
	})

	created := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) {
		createTask(input: $input) {
			id status agentConfig {
				workMode
				codex { model reasoningEffort sandboxMode approvalPolicy fullAuto bypassApprovalsAndSandbox }
				claude { model }
			}
		}
	}`, map[string]any{
		"input": map[string]any{
			"title":     "Created with config",
			"projectId": projectID,
			"workerId":  "worker-create",
			"agentType": "codex",
			"agentConfig": map[string]any{
				"workMode": "IMPLEMENT",
				"codex": map[string]any{
					"model":                     "gpt-5.4",
					"reasoningEffort":           "HIGH",
					"sandboxMode":               "WORKSPACE_WRITE",
					"approvalPolicy":            "NEVER",
					"fullAuto":                  true,
					"bypassApprovalsAndSandbox": false,
				},
			},
		},
	})
	createdTask := created["data"].(map[string]any)["createTask"].(map[string]any)
	createdConfig := createdTask["agentConfig"].(map[string]any)
	createdCodex := createdConfig["codex"].(map[string]any)
	if createdConfig["workMode"] != "IMPLEMENT" ||
		createdCodex["model"] != "gpt-5.4" ||
		createdCodex["reasoningEffort"] != "HIGH" ||
		createdCodex["sandboxMode"] != "WORKSPACE_WRITE" ||
		createdCodex["approvalPolicy"] != "NEVER" ||
		createdCodex["fullAuto"] != true {
		t.Fatalf("created agentConfig = %#v", createdConfig)
	}
	if createdConfig["claude"] != nil {
		t.Fatalf("created task should not expose claude config: %#v", createdConfig)
	}

	agentless := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"title": "Assigned with config", "projectId": projectID},
	})
	taskID := agentless["data"].(map[string]any)["createTask"].(map[string]any)["id"].(string)
	assigned := postGraphQL(t, server.URL, `mutation AssignWorker($input: AssignWorkerInput!) {
		assignWorker(input: $input) {
			id status agentType agentConfig {
				workMode
				codex { model }
				claude { model effort permissionMode }
			}
		}
	}`, map[string]any{
		"input": map[string]any{
			"taskId":    taskID,
			"workerId":  "worker-assign",
			"agentType": "claude",
			"agentConfig": map[string]any{
				"workMode": "PLAN",
				"claude": map[string]any{
					"model":          "claude-sonnet-4-5",
					"effort":         "MAX",
					"permissionMode": "BYPASS_PERMISSIONS",
				},
			},
		},
	})
	assignedTask := assigned["data"].(map[string]any)["assignWorker"].(map[string]any)
	assignedConfig := assignedTask["agentConfig"].(map[string]any)
	assignedClaude := assignedConfig["claude"].(map[string]any)
	if assignedTask["agentType"] != "claude" ||
		assignedConfig["workMode"] != "PLAN" ||
		assignedClaude["model"] != "claude-sonnet-4-5" ||
		assignedClaude["effort"] != "MAX" ||
		assignedClaude["permissionMode"] != "BYPASS_PERMISSIONS" {
		t.Fatalf("assigned agentConfig = %#v", assignedTask)
	}
	if assignedConfig["codex"] != nil {
		t.Fatalf("assigned task should not expose codex config: %#v", assignedConfig)
	}
}

func TestHealthAndReady(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	handler := NewServer(service).Handler()
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, res.Code)
		}
	}
}

func postGraphQL(t *testing.T, baseURL, query string, variables map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("graphql status = %d body = %#v", res.StatusCode, decoded)
	}
	if decoded["errors"] != nil {
		t.Fatalf("graphql errors = %#v", decoded["errors"])
	}
	return decoded
}
