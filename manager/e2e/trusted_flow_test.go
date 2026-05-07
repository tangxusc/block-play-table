package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/httpapi"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/frp"
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

func TestTrustedManagerWorkerFRPProxyFlow(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	server := httptest.NewServer(httpapi.NewServer(service).Handler())
	defer server.Close()

	targetRequests := make(chan *http.Request, 1)
	targetBodies := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read target body: %v", err)
		}
		targetBodies <- string(body)
		targetRequests <- r
		w.Header().Set("X-E2E-Target", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("frp ok"))
	}))
	defer target.Close()
	port := mustTargetPort(t, target.URL)

	frpURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/worker/frp?worker_id=worker-frp-e2e&worker_name=FRP%20E2E%20Worker"
	conn, _, err := websocket.DefaultDialer.Dial(frpURL, nil)
	if err != nil {
		t.Fatalf("dial frp ws: %v", err)
	}
	defer conn.Close()
	session, err := yamux.Client(frp.NewWebSocketConn(conn), nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer session.Close()
	go func() {
		if err := frp.ServeWorkerProxy(nil, session); err != nil && err != yamux.ErrSessionShutdown {
			t.Errorf("serve worker proxy: %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/proxy/target/path?from=e2e", strings.NewReader("hello frp"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("worker", "FRP E2E Worker")
	req.Header.Set("worker_port", strconv.Itoa(port))
	req.Header.Set("X-E2E", "yes")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated || res.Header.Get("X-E2E-Target") != "ok" || string(body) != "frp ok" {
		t.Fatalf("frp response = status %d header %q body %q", res.StatusCode, res.Header.Get("X-E2E-Target"), body)
	}
	select {
	case proxied := <-targetRequests:
		if proxied.URL.Path != "/target/path" || proxied.URL.RawQuery != "from=e2e" {
			t.Fatalf("proxied URL = %s?%s", proxied.URL.Path, proxied.URL.RawQuery)
		}
		if proxied.Header.Get("X-E2E") != "yes" {
			t.Fatalf("proxied X-E2E = %q", proxied.Header.Get("X-E2E"))
		}
		if proxied.Header.Get("worker") != "" || proxied.Header.Get("worker_port") != "" {
			t.Fatalf("routing headers leaked to target: %+v", proxied.Header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for proxied request")
	}
	if got := <-targetBodies; got != "hello frp" {
		t.Fatalf("target body = %q, want hello frp", got)
	}
}

func TestTrustedManagerWorkerReviewFRPFlow(t *testing.T) {
	ctx := context.Background()
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	server := httptest.NewServer(httpapi.NewServer(service).Handler())
	defer server.Close()

	root := t.TempDir()
	repo := filepath.Join(root, "task-worktree")
	initReviewGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\nreview change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reviewStub, reviewCapabilities := newReviewE2EStub(t, repo)
	defer reviewStub.Close()

	workerID := "worker-review-e2e"
	workerName := "Review E2E Worker"
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/worker/ws?worker_id=" + workerID
	workerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial worker ws: %v", err)
	}
	defer workerConn.Close()
	if err := workerConn.WriteJSON(protocol.Envelope{
		MessageID: "register-review",
		Type:      protocol.MessageWorkerRegister,
		WorkerID:  workerID,
		Timestamp: time.Now().UTC(),
		Payload: app.RegisterWorkerInput{
			ID:              workerID,
			Name:            workerName,
			SupportedAgents: []domain.AgentType{domain.AgentCodex},
			WorkDir:         root,
			BindingMode:     domain.WorkerAllProjects,
			Capabilities:    reviewCapabilities,
		},
	}); err != nil {
		t.Fatalf("register review worker: %v", err)
	}

	frpURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/worker/frp?worker_id=" + workerID + "&worker_name=Review%20E2E%20Worker"
	frpConn, _, err := websocket.DefaultDialer.Dial(frpURL, nil)
	if err != nil {
		t.Fatalf("dial review frp ws: %v", err)
	}
	defer frpConn.Close()
	session, err := yamux.Client(frp.NewWebSocketConn(frpConn), nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer session.Close()
	go func() {
		if err := frp.ServeWorkerProxy(nil, session); err != nil && err != yamux.ErrSessionShutdown {
			t.Errorf("serve worker proxy: %v", err)
		}
	}()

	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Review Repo", "gitUrl": repo, "defaultBranch": "main", "worktreeNamePrefix": "review"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	task := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"title": "Review E2E", "projectId": projectID, "workerId": workerID, "agentType": "codex", "baseBranch": "main"},
	})
	taskID := task["data"].(map[string]any)["createTask"].(map[string]any)["id"].(string)
	postGraphQL(t, server.URL, `mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }`, map[string]any{"taskId": taskID})
	if _, err := service.ApplyWorkerTaskStarted(ctx, "review-started", taskID, repo); err != nil {
		t.Fatal(err)
	}

	diff := postGraphQL(t, server.URL, `query ReviewDiff($taskId: ID!) {
		taskGitDiff(taskId: $taskId, scope: UNCOMMITTED) {
			files { path staged additions deletions patch }
		}
	}`, map[string]any{"taskId": taskID})
	files := diff["data"].(map[string]any)["taskGitDiff"].(map[string]any)["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "tracked.txt" {
		t.Fatalf("review diff files = %#v", files)
	}

	run := postGraphQL(t, server.URL, `mutation StartReview($input: StartTaskReviewInput!) {
		startTaskReview(input: $input) {
			id status findings { id path status }
		}
	}`, map[string]any{"input": map[string]any{"taskId": taskID, "scope": "UNCOMMITTED"}})
	reviewRun := run["data"].(map[string]any)["startTaskReview"].(map[string]any)
	if reviewRun["status"] != "COMPLETED" || len(reviewRun["findings"].([]any)) != 1 {
		t.Fatalf("review run = %#v", reviewRun)
	}

	postGraphQL(t, server.URL, `mutation Stage($input: TaskGitChangeInput!) {
		stageTaskGitChanges(input: $input) { ok }
	}`, map[string]any{"input": map[string]any{"taskId": taskID, "paths": []string{"tracked.txt"}}})
	staged := postGraphQL(t, server.URL, `query StagedDiff($taskId: ID!) {
		taskGitDiff(taskId: $taskId, scope: UNCOMMITTED, staged: true) { files { path staged } }
	}`, map[string]any{"taskId": taskID})
	stagedFiles := staged["data"].(map[string]any)["taskGitDiff"].(map[string]any)["files"].([]any)
	if len(stagedFiles) != 1 || stagedFiles[0].(map[string]any)["staged"] != true {
		t.Fatalf("staged diff files = %#v", stagedFiles)
	}

	discard := postGraphQL(t, server.URL, `mutation Discard($input: TaskGitChangeInput!) {
		discardTaskGitChanges(input: $input) { ok backup { id paths patchPath } }
	}`, map[string]any{"input": map[string]any{"taskId": taskID, "paths": []string{"tracked.txt"}}})
	backup := discard["data"].(map[string]any)["discardTaskGitChanges"].(map[string]any)["backup"].(map[string]any)
	if backup["id"] == "" || len(backup["paths"].([]any)) != 1 {
		t.Fatalf("discard backup = %#v", backup)
	}
	raw, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "base\n" {
		t.Fatalf("tracked after discard = %q", raw)
	}

	postGraphQL(t, server.URL, `mutation Restore($input: TaskGitChangeInput!) {
		restoreTaskGitBackup(input: $input) { ok }
	}`, map[string]any{"input": map[string]any{"taskId": taskID, "backupId": backup["id"]}})
	restored, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "base\nreview change\n" {
		t.Fatalf("tracked after restore = %q", restored)
	}
}

func newReviewE2EStub(t *testing.T, repo string) (*httptest.Server, map[string]string) {
	t.Helper()
	backups := map[string]string{}
	staged := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/review/tasks/") {
			http.NotFound(w, r)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 4 {
			http.NotFound(w, r)
			return
		}
		taskID := parts[2]
		action := parts[3]
		now := time.Now().UTC()
		switch {
		case r.Method == http.MethodGet && action == "diff":
			if r.URL.Query().Get("staged") == "true" && !staged {
				writeReviewJSON(t, w, map[string]any{"taskId": taskID, "scope": "UNCOMMITTED", "files": []any{}, "generatedAt": now})
				return
			}
			writeReviewJSON(t, w, map[string]any{
				"taskId":      taskID,
				"scope":       "UNCOMMITTED",
				"generatedAt": now,
				"files": []map[string]any{{
					"path":      "tracked.txt",
					"status":    "M",
					"staged":    staged,
					"additions": 1,
					"deletions": 0,
					"patch":     "@@ -1 +1,2 @@\n base\n+review change\n",
				}},
			})
		case r.Method == http.MethodPost && action == "runs":
			runID := "review-run-e2e"
			writeReviewJSON(t, w, map[string]any{
				"run": map[string]any{
					"id":        runID,
					"taskId":    taskID,
					"scope":     "UNCOMMITTED",
					"status":    "COMPLETED",
					"agentType": "codex",
					"summary":   "1 finding(s)",
					"rawResult": `{"summary":"1 finding(s)","findings":[{"path":"tracked.txt"}]}`,
					"createdAt": now,
					"updatedAt": now,
				},
				"findings": []map[string]any{{
					"id":        "review-finding-e2e",
					"runId":     runID,
					"taskId":    taskID,
					"path":      "tracked.txt",
					"line":      2,
					"severity":  "MEDIUM",
					"status":    "OPEN",
					"title":     "Review changed file",
					"body":      "Review this changed file before merging.",
					"createdAt": now,
					"updatedAt": now,
				}},
			})
		case r.Method == http.MethodPost && action == "stage":
			runGitE2E(t, repo, "add", "--", "tracked.txt")
			staged = true
			writeReviewJSON(t, w, map[string]any{"ok": true})
		case r.Method == http.MethodPost && action == "discard":
			raw, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
			if err != nil {
				t.Error(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			backupID := "backup-e2e"
			backups[backupID] = string(raw)
			_ = exec.Command("git", "-C", repo, "restore", "--staged", "--", "tracked.txt").Run()
			runGitE2E(t, repo, "restore", "--worktree", "--", "tracked.txt")
			staged = false
			writeReviewJSON(t, w, map[string]any{
				"ok": true,
				"backup": map[string]any{
					"id":        backupID,
					"taskId":    taskID,
					"paths":     []string{"tracked.txt"},
					"patchPath": filepath.Join(repo, ".review-backups", backupID+".patch"),
					"createdAt": now,
				},
			})
		case r.Method == http.MethodPost && action == "restore":
			var input struct {
				BackupID string `json:"backupId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			raw := backups[input.BackupID]
			if raw == "" {
				http.Error(w, "backup not found", http.StatusNotFound)
				return
			}
			if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte(raw), 0o644); err != nil {
				t.Error(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeReviewJSON(t, w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return server, map[string]string{
		"review_enabled": "true",
		"review_host":    host,
		"review_port":    portText,
	}
}

func writeReviewJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func mustTargetPort(t *testing.T, rawURL string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func initReviewGitRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitE2E(t, repo, "init", "-b", "main")
	runGitE2E(t, repo, "config", "user.email", "bpt@example.test")
	runGitE2E(t, repo, "config", "user.name", "Block Play Table")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitE2E(t, repo, "add", ".")
	runGitE2E(t, repo, "commit", "-m", "base")
}

func runGitE2E(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
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
