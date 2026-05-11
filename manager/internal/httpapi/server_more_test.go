package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/migrations"
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

	worker := postGraphQL(t, server.URL, `mutation CreateWorker($input: CreateWorkerInput!) { createWorker(input: $input) { id status currentTaskIds agentRuntimeEnv { agentType vars { key valueMasked enabled sensitive } } } }`, map[string]any{
		"input": map[string]any{
			"id":                 "worker-ops",
			"name":               "W",
			"supportedAgents":    []any{"codex"},
			"workDir":            "/tmp",
			"projectBindingMode": "ALL_PROJECTS",
			"agentRuntimeEnv": []any{map[string]any{
				"agentType": "codex",
				"vars": []any{
					map[string]any{"key": "TOKEN", "value": "secret", "enabled": true, "sensitive": true},
					map[string]any{"key": "DISABLED", "value": "ignored", "enabled": false, "sensitive": false},
				},
			}},
		},
	})
	createdWorker := worker["data"].(map[string]any)["createWorker"].(map[string]any)
	if got := createdWorker["status"]; got != string(domain.WorkerOnline) {
		t.Fatalf("worker status = %v, want ONLINE", got)
	}
	if got := createdWorker["currentTaskIds"].([]any); len(got) != 0 {
		t.Fatalf("created worker currentTaskIds = %+v, want empty", got)
	}
	workerEnv := createdWorker["agentRuntimeEnv"].([]any)
	firstVar := workerEnv[0].(map[string]any)["vars"].([]any)[0].(map[string]any)
	if firstVar["valueMasked"] != "********" {
		t.Fatalf("worker env should mask sensitive values: %#v", workerEnv)
	}
	if got := postGraphQL(t, server.URL, `query { workers { id } }`, nil)["data"].(map[string]any)["workers"].([]any); len(got) != 1 {
		t.Fatalf("workers count = %d, want 1", len(got))
	}
	workerConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-ops", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer workerConn.Close()

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
	workerAfterStart := postGraphQL(t, server.URL, `query Worker($id: ID!) { worker(id: $id) { currentTaskIds } }`, map[string]any{"id": "worker-ops"})
	currentTaskIDs := workerAfterStart["data"].(map[string]any)["worker"].(map[string]any)["currentTaskIds"].([]any)
	if len(currentTaskIDs) != 1 || currentTaskIDs[0] != taskID {
		t.Fatalf("worker currentTaskIds = %+v, want [%s]", currentTaskIDs, taskID)
	}
	_ = workerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var start rawEnvelope
	if err := workerConn.ReadJSON(&start); err != nil {
		t.Fatal(err)
	}
	var payload protocol.TaskStartPayload
	if data, err := json.Marshal(start.Payload); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.AgentRuntimeEnv) != 1 || payload.AgentRuntimeEnv[0].Key != "TOKEN" || payload.AgentRuntimeEnv[0].Value != "secret" {
		t.Fatalf("task start env = %+v", payload.AgentRuntimeEnv)
	}

	if got := postGraphQL(t, server.URL, `query Task($id: ID!) { task(id: $id) { id } }`, map[string]any{"id": taskID})["data"].(map[string]any)["task"].(map[string]any)["id"]; got != taskID {
		t.Fatalf("task id = %v, want %s", got, taskID)
	}
	if logsValue := postGraphQL(t, server.URL, `query TaskLogs($taskId: ID!) { taskLogs(taskId: $taskId) { id } }`, map[string]any{"taskId": taskID})["data"].(map[string]any)["taskLogs"]; logsValue != nil {
		if logs := logsValue.([]any); len(logs) != 0 {
			t.Fatalf("logs count = %d, want 0", len(logs))
		}
	}
	if messagesValue := postGraphQL(t, server.URL, `query TaskConversations($taskId: ID!) { taskConversations(taskId: $taskId) { id } }`, map[string]any{"taskId": taskID})["data"].(map[string]any)["taskConversations"]; messagesValue != nil {
		if messages := messagesValue.([]any); len(messages) != 0 {
			t.Fatalf("conversation count = %d, want 0", len(messages))
		}
	}
	if events := postGraphQL(t, server.URL, `query TaskEvents($taskId: ID!) { taskEvents(taskId: $taskId) { eventType } }`, map[string]any{"taskId": taskID})["data"].(map[string]any)["taskEvents"].([]any); len(events) == 0 {
		t.Fatal("expected task events")
	}
	if events := postGraphQL(t, server.URL, `query DomainEvents($aggregateId: ID!) { domainEvents(aggregateId: $aggregateId) { eventType } }`, map[string]any{"aggregateId": taskID})["data"].(map[string]any)["domainEvents"].([]any); len(events) == 0 {
		t.Fatal("expected domain events")
	}
	outbox := postGraphQL(t, server.URL, `query { outboxMessages { id status event { eventType } } }`, nil)["data"].(map[string]any)["outboxMessages"].([]any)
	if len(outbox) == 0 || outbox[0].(map[string]any)["status"] != string(domain.OutboxPublished) {
		t.Fatalf("expected published outbox messages, got %#v", outbox)
	}
	if got := postGraphQL(t, server.URL, `mutation InterruptTask($taskId: ID!) { interruptTask(taskId: $taskId) { id } }`, map[string]any{"taskId": taskID})["data"].(map[string]any)["interruptTask"].(map[string]any)["id"]; got != taskID {
		t.Fatalf("interrupt task id = %v, want %s", got, taskID)
	}

	rawSettingsEnv := postRawGraphQL(t, server.URL, `mutation UpdateSettings($input: UpdateAgentRuntimeEnvVarsInput!) { updateAgentRuntimeEnvVars(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"vars": []any{map[string]any{"key": "TOKEN", "value": "secret", "enabled": true, "sensitive": true}}},
	})
	if rawSettingsEnv["errors"] == nil {
		t.Fatalf("old settings env mutation should be removed: %#v", rawSettingsEnv)
	}
	_ = postGraphQL(t, server.URL, `query { settings { id } }`, nil)
	archiveProject := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Archive", "gitUrl": "git://archive"},
	})
	archiveProjectID := archiveProject["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	archived := postGraphQL(t, server.URL, `mutation ArchiveProject($id: ID!) { archiveProject(id: $id) { archived } }`, map[string]any{"id": archiveProjectID})
	if archived["data"].(map[string]any)["archiveProject"].(map[string]any)["archived"] != true {
		t.Fatal("project should be archived")
	}
}

func TestServerTaskLogsFallBackToConversationsWhenNoLogsPersisted(t *testing.T) {
	ctx := context.Background()
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	project, err := service.CreateProject(ctx, app.CreateProjectInput{
		Name:               "P",
		GitURL:             "file:///tmp/repo",
		DefaultBranch:      "main",
		WorktreeNamePrefix: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, app.RegisterWorkerInput{
		ID:              "worker-conversation-log",
		Name:            "W",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, app.CreateTaskInput{
		Title:      "Conversation-only",
		ProjectID:  project.ID,
		AgentType:  domain.AgentCodex,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskStarted(ctx, "started-conversation-log", task.ID, "/tmp/worktree"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerConversation(ctx, "conversation-log", task.ID, "assistant", "conversation appears in logs"); err != nil {
		t.Fatal(err)
	}

	logs := postGraphQL(t, server.URL, `query TaskLogs($taskId: ID!) {
		taskLogs(taskId: $taskId) { id stream content createdAt }
	}`, map[string]any{"taskId": task.ID})["data"].(map[string]any)["taskLogs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("fallback logs count = %d, want 1: %#v", len(logs), logs)
	}
	first := logs[0].(map[string]any)
	if first["stream"] != "assistant" || first["content"] != "conversation appears in logs" {
		t.Fatalf("fallback log = %#v", first)
	}

	if _, err := service.ApplyWorkerTaskLog(ctx, "real-log", task.ID, "stdout", "real persisted log"); err != nil {
		t.Fatal(err)
	}
	logs = postGraphQL(t, server.URL, `query TaskLogs($taskId: ID!) {
		taskLogs(taskId: $taskId) { stream content }
	}`, map[string]any{"taskId": task.ID})["data"].(map[string]any)["taskLogs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("persisted logs count = %d, want only real logs: %#v", len(logs), logs)
	}
	only := logs[0].(map[string]any)
	if only["stream"] != "stdout" || only["content"] != "real persisted log" {
		t.Fatalf("persisted log = %#v", only)
	}
}

func TestServerPaginationConnections(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		current := now
		now = now.Add(time.Minute)
		return current
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	firstProject := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "First Project", "gitUrl": "git://first"},
	})["data"].(map[string]any)["createProject"].(map[string]any)
	secondProject := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Second Project", "gitUrl": "git://second"},
	})["data"].(map[string]any)["createProject"].(map[string]any)
	firstProjectID := firstProject["id"].(string)
	secondProjectID := secondProject["id"].(string)

	for _, input := range []map[string]any{
		{"id": "worker-page-1", "name": "First Worker", "supportedAgents": []any{"codex"}, "workDir": "/tmp/first"},
		{"id": "worker-page-2", "name": "Second Worker", "supportedAgents": []any{"codex"}, "workDir": "/tmp/second"},
	} {
		_ = postGraphQL(t, server.URL, `mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id } }`, map[string]any{"input": input})
	}

	for _, title := range []string{"Task 1", "Task 2", "Task 3"} {
		_ = postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
			"input": map[string]any{"title": title, "projectId": secondProjectID, "agentType": "codex"},
		})
	}

	projects := postGraphQL(t, server.URL, `query {
		projectsConnection(filter: { includeArchived: true }, page: { offset: 0, limit: 1 }) {
			totalCount
			nodes { id name }
		}
	}`, nil)["data"].(map[string]any)["projectsConnection"].(map[string]any)
	if projects["totalCount"] != float64(2) {
		t.Fatalf("project totalCount = %v, want 2", projects["totalCount"])
	}
	projectNodes := projects["nodes"].([]any)
	if len(projectNodes) != 1 || projectNodes[0].(map[string]any)["id"] != secondProjectID {
		t.Fatalf("project nodes = %#v, want latest project", projectNodes)
	}

	workers := postGraphQL(t, server.URL, `query {
		workersConnection(filter: { includeDisabled: true }, page: { offset: 0, limit: 1 }) {
			totalCount
			nodes { id name }
		}
	}`, nil)["data"].(map[string]any)["workersConnection"].(map[string]any)
	if workers["totalCount"] != float64(2) {
		t.Fatalf("worker totalCount = %v, want 2", workers["totalCount"])
	}
	workerNodes := workers["nodes"].([]any)
	if len(workerNodes) != 1 || workerNodes[0].(map[string]any)["id"] != "worker-page-2" {
		t.Fatalf("worker nodes = %#v, want latest worker", workerNodes)
	}

	events := postGraphQL(t, server.URL, `query {
		domainEventsConnection(filter: { aggregateType: "Project" }, page: { offset: 0, limit: 1 }) {
			totalCount
			nodes { eventType aggregateId }
		}
	}`, nil)["data"].(map[string]any)["domainEventsConnection"].(map[string]any)
	if events["totalCount"] != float64(2) {
		t.Fatalf("event totalCount = %v, want 2", events["totalCount"])
	}
	eventNodes := events["nodes"].([]any)
	if len(eventNodes) != 1 || eventNodes[0].(map[string]any)["aggregateId"] != secondProjectID {
		t.Fatalf("event nodes = %#v, want latest project event", eventNodes)
	}

	board := postGraphQL(t, server.URL, `query {
		board(page: { offset: 0, limit: 2 }) {
			totalCount
			tasks { title }
			columns { tasks { title } }
		}
	}`, nil)["data"].(map[string]any)["board"].(map[string]any)
	if board["totalCount"] != float64(3) {
		t.Fatalf("board totalCount = %v, want 3", board["totalCount"])
	}
	taskNodes := board["tasks"].([]any)
	if len(taskNodes) != 2 || taskNodes[0].(map[string]any)["title"] != "Task 3" {
		t.Fatalf("board tasks = %#v, want latest two tasks", taskNodes)
	}

	searchedBoard := postGraphQL(t, server.URL, `query {
		board(filter: { includeArchived: true, search: "Task 1" }, sort: { field: TITLE, direction: ASC }, page: { offset: 0, limit: 1 }) {
			totalCount
			tasks { title }
		}
	}`, nil)["data"].(map[string]any)["board"].(map[string]any)
	searchedTasks := searchedBoard["tasks"].([]any)
	if searchedBoard["totalCount"] != float64(1) || len(searchedTasks) != 1 || searchedTasks[0].(map[string]any)["title"] != "Task 1" {
		t.Fatalf("searched board = %#v", searchedBoard)
	}

	for _, title := range []string{"First Project Task 1", "First Project Task 2"} {
		_ = postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
			"input": map[string]any{"title": title, "projectId": firstProjectID, "agentType": "codex"},
		})
	}
	projectBoard := postGraphQL(t, server.URL, `query Board($projectId: ID!) {
		board(filter: { includeArchived: true, projectId: $projectId }, sort: { field: TITLE, direction: ASC }, page: { offset: 1, limit: 1 }) {
			totalCount
			tasks { title }
		}
	}`, map[string]any{"projectId": secondProjectID})["data"].(map[string]any)["board"].(map[string]any)
	projectBoardTasks := projectBoard["tasks"].([]any)
	if projectBoard["totalCount"] != float64(3) || len(projectBoardTasks) != 1 || projectBoardTasks[0].(map[string]any)["title"] != "Task 2" {
		t.Fatalf("project-filtered board = %#v", projectBoard)
	}

	searchedProjects := postGraphQL(t, server.URL, `query {
		projectsConnection(filter: { includeArchived: true, search: "First Project" }, sort: { field: NAME, direction: ASC }, page: { offset: 0, limit: 1 }) {
			totalCount
			nodes { id name }
		}
	}`, nil)["data"].(map[string]any)["projectsConnection"].(map[string]any)
	searchedProjectNodes := searchedProjects["nodes"].([]any)
	if searchedProjects["totalCount"] != float64(1) || len(searchedProjectNodes) != 1 || searchedProjectNodes[0].(map[string]any)["id"] != firstProjectID {
		t.Fatalf("searched projects = %#v", searchedProjects)
	}

	searchedWorkers := postGraphQL(t, server.URL, `query {
		workersConnection(filter: { includeDisabled: true, search: "First Worker" }, sort: { field: NAME, direction: ASC }, page: { offset: 0, limit: 1 }) {
			totalCount
			nodes { id name }
		}
	}`, nil)["data"].(map[string]any)["workersConnection"].(map[string]any)
	searchedWorkerNodes := searchedWorkers["nodes"].([]any)
	if searchedWorkers["totalCount"] != float64(1) || len(searchedWorkerNodes) != 1 || searchedWorkerNodes[0].(map[string]any)["id"] != "worker-page-1" {
		t.Fatalf("searched workers = %#v", searchedWorkers)
	}

	searchedEvents := postGraphQL(t, server.URL, `query {
		domainEventsConnection(filter: { search: "Second Project" }, sort: { field: EVENT_TYPE, direction: ASC }, page: { offset: 0, limit: 1 }) {
			totalCount
			nodes { eventType aggregateId }
		}
	}`, nil)["data"].(map[string]any)["domainEventsConnection"].(map[string]any)
	searchedEventNodes := searchedEvents["nodes"].([]any)
	if searchedEvents["totalCount"] != float64(1) || len(searchedEventNodes) != 1 || searchedEventNodes[0].(map[string]any)["aggregateId"] != secondProjectID {
		t.Fatalf("searched events = %#v", searchedEvents)
	}

	legacyEvents := postGraphQL(t, server.URL, `query DomainEvents($aggregateId: ID!) {
		domainEvents(aggregateId: $aggregateId) { eventType }
	}`, map[string]any{"aggregateId": firstProjectID})["data"].(map[string]any)["domainEvents"].([]any)
	if len(legacyEvents) != 1 {
		t.Fatalf("legacy domainEvents count = %d, want 1", len(legacyEvents))
	}
}

func TestServerBoardArchivedFilterAndDeleteTaskMutation(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Archived Board", "gitUrl": "git://archived-board"},
	})["data"].(map[string]any)["createProject"].(map[string]any)
	projectID := project["id"].(string)
	activeTask := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }`, map[string]any{
		"input": map[string]any{"title": "Visible active", "projectId": projectID, "agentType": "codex"},
	})["data"].(map[string]any)["createTask"].(map[string]any)
	archivedTask := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }`, map[string]any{
		"input": map[string]any{"title": "Hidden archived", "projectId": projectID, "agentType": "codex"},
	})["data"].(map[string]any)["createTask"].(map[string]any)
	archivedID := archivedTask["id"].(string)
	_ = postGraphQL(t, server.URL, `mutation ArchiveTask($taskId: ID!) { archiveTask(taskId: $taskId) { id status } }`, map[string]any{"taskId": archivedID})

	defaultBoard := postGraphQL(t, server.URL, `query {
		board {
			totalCount
			tasks { id title status }
			columns { tasks { id status } }
			calendarItems { task { id status } }
		}
	}`, nil)["data"].(map[string]any)["board"].(map[string]any)
	if defaultBoard["totalCount"] != float64(1) {
		t.Fatalf("default board totalCount = %v, want 1", defaultBoard["totalCount"])
	}
	defaultTasks := defaultBoard["tasks"].([]any)
	if len(defaultTasks) != 1 || defaultTasks[0].(map[string]any)["id"] != activeTask["id"] {
		t.Fatalf("default board tasks = %#v", defaultTasks)
	}
	for _, column := range defaultBoard["columns"].([]any) {
		for _, task := range column.(map[string]any)["tasks"].([]any) {
			if task.(map[string]any)["status"] == string(domain.TaskArchived) {
				t.Fatalf("default board column included archived task: %#v", defaultBoard["columns"])
			}
		}
	}
	for _, item := range defaultBoard["calendarItems"].([]any) {
		task := item.(map[string]any)["task"].(map[string]any)
		if task["status"] == string(domain.TaskArchived) {
			t.Fatalf("default board calendar included archived task: %#v", defaultBoard["calendarItems"])
		}
	}

	archivedBoard := postGraphQL(t, server.URL, `query {
		board(filter: { status: ARCHIVED, includeArchived: true }) {
			totalCount
			tasks { id title status }
		}
	}`, nil)["data"].(map[string]any)["board"].(map[string]any)
	archivedTasks := archivedBoard["tasks"].([]any)
	if archivedBoard["totalCount"] != float64(1) || len(archivedTasks) != 1 || archivedTasks[0].(map[string]any)["id"] != archivedID {
		t.Fatalf("archived board = %#v", archivedBoard)
	}

	rawDeleteActive := postRawGraphQL(t, server.URL, `mutation DeleteTask($taskId: ID!) { deleteTask(taskId: $taskId) }`, map[string]any{"taskId": activeTask["id"]})
	if rawDeleteActive["errors"] == nil {
		t.Fatalf("delete active task should fail: %#v", rawDeleteActive)
	}
	deleted := postGraphQL(t, server.URL, `mutation DeleteTask($taskId: ID!) { deleteTask(taskId: $taskId) }`, map[string]any{"taskId": archivedID})["data"].(map[string]any)["deleteTask"].(bool)
	if !deleted {
		t.Fatal("deleteTask should return true")
	}
	deletedQuery := postGraphQL(t, server.URL, `query Task($id: ID!) { task(id: $id) { id } }`, map[string]any{"id": archivedID})["data"].(map[string]any)["task"]
	if deletedQuery != nil {
		t.Fatalf("deleted task query = %#v, want nil", deletedQuery)
	}
	events := postGraphQL(t, server.URL, `query TaskEvents($taskId: ID!) { taskEvents(taskId: $taskId) { eventType } }`, map[string]any{"taskId": archivedID})["data"].(map[string]any)["taskEvents"].([]any)
	foundDeleteEvent := false
	for _, event := range events {
		if event.(map[string]any)["eventType"] == "TaskDeleted" {
			foundDeleteEvent = true
		}
	}
	if !foundDeleteEvent {
		t.Fatalf("TaskDeleted event missing from taskEvents: %#v", events)
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

	dialer := websocket.Dialer{Subprotocols: []string{"graphql-transport-ws"}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/subscriptions", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("subscription read: %v", err)
	}
	if message["type"] != "connection_ack" {
		t.Fatalf("subscription message = %#v", message)
	}
}

func TestServerSubscriptionsPushDomainEvents(t *testing.T) {
	server := httptest.NewServer(NewServer(app.NewService(store.NewMemoryStore())).Handler())
	defer server.Close()

	dialer := websocket.Dialer{Subprotocols: []string{"graphql-transport-ws"}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/subscriptions", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ack map[string]any
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("subscription ack: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"id":   "sub-1",
		"type": "subscribe",
		"payload": map[string]any{
			"query": `subscription { domainEvents(filter: { eventType: "ProjectCreated" }) { eventType } }`,
		},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	_ = postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "P", "gitUrl": "git://repo"},
	})
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("subscription read: %v", err)
		}
		if message["type"] != "next" {
			continue
		}
		payload := message["payload"].(map[string]any)
		data := payload["data"].(map[string]any)
		event := data["domainEvents"].(map[string]any)
		if event["eventType"] != "ProjectCreated" {
			t.Fatalf("domain event = %#v", event)
		}
		return
	}
}

func TestServerSubscriptionsAcceptBrowserOrigin(t *testing.T) {
	server := httptest.NewServer(NewServer(app.NewService(store.NewMemoryStore())).Handler())
	defer server.Close()

	dialer := websocket.Dialer{Subprotocols: []string{"graphql-transport-ws"}}
	conn, _, err := dialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/subscriptions",
		http.Header{"Origin": []string{"http://localhost:3000"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("subscription read: %v", err)
	}
	if message["type"] != "connection_ack" {
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
	sendWS(t, conn, protocol.Envelope{MessageID: "waiting-1", Type: protocol.MessageTaskWaitingInput, WorkerID: "worker-ws", TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Content: "waiting"}})
	sendWS(t, conn, protocol.Envelope{MessageID: "result-1", Type: protocol.MessageTaskResult, WorkerID: "worker-ws", TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Result: "structured result"}})
	sendWS(t, conn, protocol.Envelope{MessageID: "done-1", Type: protocol.MessageTaskCompleted, WorkerID: "worker-ws", TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{}})

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
	loaded, err := service.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Result != "structured result" {
		t.Fatalf("task result = %q, want structured result", loaded.Result)
	}
	if logs, _ := service.Store().TaskLogs(ctx, task.ID); len(logs) != 2 {
		t.Fatalf("logs count = %d, want 2", len(logs))
	}
	if messages, _ := service.Store().TaskConversations(ctx, task.ID); len(messages) != 1 {
		t.Fatalf("conversation count = %d, want 1", len(messages))
	}
}

func TestWorkerGatewayRoutesTaskInteractionResponse(t *testing.T) {
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
	worker, err := service.RegisterWorker(ctx, app.RegisterWorkerInput{
		ID:              "worker-interaction",
		Name:            "W",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp",
		BindingMode:     domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, app.CreateTaskInput{Title: "T", ProjectID: project.ID, WorkerID: worker.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-interaction", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	postGraphQL(t, server.URL, `mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }`, map[string]any{"taskId": task.ID})
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var start rawEnvelope
	if err := conn.ReadJSON(&start); err != nil {
		t.Fatal(err)
	}
	if start.Type != protocol.MessageTaskStart {
		t.Fatalf("start envelope = %+v", start)
	}
	sendWS(t, conn, protocol.Envelope{MessageID: "started-interaction", Type: protocol.MessageTaskStarted, WorkerID: worker.ID, TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Content: "/tmp/worktree"}})
	sendWS(t, conn, protocol.Envelope{MessageID: "interaction-request", Type: protocol.MessageTaskInteractionRequest, WorkerID: worker.ID, TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.TaskInteractionRequestPayload{
		InteractionID: "interaction-1",
		TaskID:        task.ID,
		Kind:          domain.TaskInteractionCommandApproval,
		Title:         "Approve command",
		Body:          "Run make test",
		RawPayload:    `{"command":"make test"}`,
	}})

	deadline := time.Now().Add(2 * time.Second)
	for {
		loaded, err := service.Task(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == domain.TaskWaitingInput {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task status = %s, want WAITING_INPUT", loaded.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- postGraphQLError(server.URL, `mutation Respond($input: RespondTaskInteractionInput!) {
			respondTaskInteraction(input: $input) { id status responseDecision }
		}`, map[string]any{
			"input": map[string]any{"interactionId": "interaction-1", "decision": "APPROVE"},
		})
	}()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var response rawEnvelope
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if response.Type != protocol.MessageTaskInteractionResponse {
		t.Fatalf("interaction response envelope = %+v", response)
	}
	var payload protocol.TaskInteractionResponsePayload
	if err := json.Unmarshal(response.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.InteractionID != "interaction-1" || payload.Decision != domain.TaskInteractionApprove {
		t.Fatalf("interaction response payload = %+v", payload)
	}
	sendWS(t, conn, protocol.Envelope{MessageID: "interaction-resolved", Type: protocol.MessageTaskInteractionResolved, WorkerID: worker.ID, TaskID: task.ID, Timestamp: time.Now(), Payload: protocol.TaskInteractionResolvedPayload{
		InteractionID: "interaction-1",
		TaskID:        task.ID,
	}})
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	loaded, err := service.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.TaskRunning {
		t.Fatalf("task after resolved = %+v", loaded)
	}
	interaction, err := service.Store().TaskInteraction(ctx, "interaction-1")
	if err != nil {
		t.Fatal(err)
	}
	if interaction.Status != domain.TaskInteractionAnswered || interaction.ResponseDecision != domain.TaskInteractionApprove {
		t.Fatalf("interaction after response = %+v", interaction)
	}
}

func TestGraphQLContinueTaskSendsTaskContinueToOriginalWorker(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "P", "gitUrl": "git://repo", "defaultBranch": "main", "worktreeNamePrefix": "p"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	postGraphQL(t, server.URL, `mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }`, map[string]any{
		"input": map[string]any{"id": "worker-continue", "name": "W", "supportedAgents": []any{"codex"}, "workDir": "/tmp/worker", "projectBindingMode": "ALL_PROJECTS"},
	})
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-continue", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	task := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"title": "T", "projectId": projectID, "workerId": "worker-continue", "agentType": "codex"},
	})
	taskID := task["data"].(map[string]any)["createTask"].(map[string]any)["id"].(string)
	postGraphQL(t, server.URL, `mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }`, map[string]any{"taskId": taskID})
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var start rawEnvelope
	if err := conn.ReadJSON(&start); err != nil {
		t.Fatal(err)
	}
	if start.Type != protocol.MessageTaskStart {
		t.Fatalf("start envelope = %+v", start)
	}
	sendWS(t, conn, protocol.Envelope{MessageID: "started-continue", Type: protocol.MessageTaskStarted, WorkerID: "worker-continue", TaskID: taskID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Content: "/tmp/worktree"}})
	sendWS(t, conn, protocol.Envelope{MessageID: "completed-continue", Type: protocol.MessageTaskCompleted, WorkerID: "worker-continue", TaskID: taskID, Timestamp: time.Now(), Payload: protocol.WorkerEvent{Result: "first result", AgentSessionID: "session-1"}})

	deadline := time.Now().Add(2 * time.Second)
	for {
		loaded, err := service.Task(context.Background(), taskID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == domain.TaskCompleted && loaded.AgentSessionID == "session-1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task after completion = %+v", loaded)
		}
		time.Sleep(10 * time.Millisecond)
	}

	continued := postGraphQL(t, server.URL, `mutation ContinueTask($input: ContinueTaskInput!) { continueTask(input: $input) { id status agentSessionId result } }`, map[string]any{
		"input": map[string]any{"taskId": taskID, "message": "follow up"},
	})
	continuedTask := continued["data"].(map[string]any)["continueTask"].(map[string]any)
	if continuedTask["status"] != string(domain.TaskStarting) || continuedTask["agentSessionId"] != "session-1" || continuedTask["result"] != nil {
		t.Fatalf("continued task = %#v", continuedTask)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var continuation rawEnvelope
	if err := conn.ReadJSON(&continuation); err != nil {
		t.Fatal(err)
	}
	if continuation.Type != protocol.MessageTaskContinue || continuation.TaskID != taskID {
		t.Fatalf("continue envelope = %+v", continuation)
	}
	var payload protocol.TaskContinuePayload
	if err := json.Unmarshal(continuation.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AgentSessionID != "session-1" || payload.Message != "follow up" || payload.WorktreePath != "/tmp/worktree" {
		t.Fatalf("continue payload = %+v", payload)
	}
	messages := postGraphQL(t, server.URL, `query TaskConversations($taskId: ID!) { taskConversations(taskId: $taskId) { role content } }`, map[string]any{"taskId": taskID})
	taskMessages := messages["data"].(map[string]any)["taskConversations"].([]any)
	if len(taskMessages) != 1 || taskMessages[0].(map[string]any)["role"] != "user" {
		t.Fatalf("task conversations = %#v", taskMessages)
	}
}

func TestWorkerGatewayRequiresTokenAndMarksDisconnectOffline(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	server := httptest.NewServer(NewServer(service, WithWorkerToken("secret")).Handler())
	defer server.Close()

	_, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-token", nil)
	if err == nil {
		t.Fatal("worker websocket without token should fail")
	}
	if res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized response = %+v, err = %v", res, err)
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-token&token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	sendWS(t, conn, protocol.Envelope{MessageID: "reg-token", Type: protocol.MessageWorkerRegister, WorkerID: "worker-token", Timestamp: time.Now(), Payload: map[string]any{
		"id": "worker-token", "name": "Token Worker", "supportedAgents": []string{"codex"}, "workDir": "/tmp", "projectBindingMode": "ALL_PROJECTS",
	}})
	deadline := time.Now().Add(2 * time.Second)
	for {
		worker, err := service.Store().Worker(context.Background(), "worker-token")
		if err == nil && worker.Status == domain.WorkerOnline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not come online: %+v, %v", worker, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = conn.Close()
	deadline = time.Now().Add(2 * time.Second)
	for {
		worker, err := service.Store().Worker(context.Background(), "worker-token")
		if err == nil && worker.Status == domain.WorkerOffline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not go offline: %+v, %v", worker, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerGatewaySendsTaskCancelAndHeartbeatOption(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	api := NewServer(service, WithWorkerHeartbeatTimeout(7*time.Second))
	if api.heartbeatTimeout != 7*time.Second {
		t.Fatalf("heartbeat timeout = %s, want 7s", api.heartbeatTimeout)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := api.gateway.SendTaskCancel("", "task-1"); err != nil {
		t.Fatalf("SendTaskCancel with empty worker id returned error: %v", err)
	}
	if err := api.gateway.SendTaskCancel("missing-worker", "task-1"); err == nil {
		t.Fatal("SendTaskCancel to missing worker should fail")
	}
	if err := api.gateway.SendTaskInterrupt("", "task-1"); err != nil {
		t.Fatalf("SendTaskInterrupt with empty worker id returned error: %v", err)
	}
	if err := api.gateway.SendTaskInterrupt("missing-worker", "task-1"); err == nil {
		t.Fatal("SendTaskInterrupt to missing worker should fail")
	}
	if err := api.gateway.SendTaskInterrupt("worker-cancel", "task-2"); err != nil {
		t.Fatalf("SendTaskInterrupt returned error: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var interrupt protocol.Envelope
	if err := conn.ReadJSON(&interrupt); err != nil {
		t.Fatal(err)
	}
	if interrupt.Type != protocol.MessageTaskInterrupt || interrupt.WorkerID != "worker-cancel" || interrupt.TaskID != "task-2" {
		t.Fatalf("interrupt envelope = %+v", interrupt)
	}
	if err := api.gateway.SendTaskCancel("worker-cancel", "task-1"); err != nil {
		t.Fatalf("SendTaskCancel returned error: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var envelope protocol.Envelope
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != protocol.MessageTaskCancel || envelope.WorkerID != "worker-cancel" || envelope.TaskID != "task-1" {
		t.Fatalf("cancel envelope = %+v", envelope)
	}
	if got := taskIDFromEnvelope(rawEnvelope{}, protocol.WorkerEvent{TaskID: "task-from-event"}); got != "task-from-event" {
		t.Fatalf("taskIDFromEnvelope fallback = %q", got)
	}
}

func TestServerReadyzChecksStorage(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := store.OpenSQLStore(ctx, store.SQLDriverSQLite, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.Migrate(ctx, migrations.SchemaSQL); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(app.NewService(sqlStore)).Handler())
	defer server.Close()

	res, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", res.StatusCode)
	}
	if err := sqlStore.Close(); err != nil {
		t.Fatal(err)
	}
	res, err = http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503 after closing db", res.StatusCode)
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

func postGraphQLError(baseURL, query string, variables map[string]any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	res, err := http.Post(baseURL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("graphql status %d: %#v", res.StatusCode, decoded)
	}
	if decoded["errors"] != nil {
		return fmt.Errorf("graphql errors: %#v", decoded["errors"])
	}
	return nil
}

func sendWS(t *testing.T, conn *websocket.Conn, envelope protocol.Envelope) {
	t.Helper()
	if err := conn.WriteJSON(envelope); err != nil {
		t.Fatal(err)
	}
}
