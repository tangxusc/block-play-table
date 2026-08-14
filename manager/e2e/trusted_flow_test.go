package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/httpapi"
	"github.com/tangxusc/block-play-table/manager/migrations"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/frp"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestTrustedManagerWorkerFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	root := repositoryRoot(t)
	runtimeDir := t.TempDir()
	managerStore, err := store.OpenSQLStore(ctx, store.SQLDriverSQLite, filepath.Join(runtimeDir, "manager.db"))
	if err != nil {
		t.Fatalf("open manager sqlite: %v", err)
	}
	t.Cleanup(func() { _ = managerStore.Close() })
	storeMigrations := make([]store.Migration, 0, len(migrations.All))
	for _, migration := range migrations.All {
		storeMigrations = append(storeMigrations, store.Migration{Version: migration.Version, SQL: migration.SQL})
	}
	if err := managerStore.MigrateVersioned(ctx, storeMigrations); err != nil {
		t.Fatalf("migrate manager sqlite: %v", err)
	}
	service := app.NewService(managerStore)
	apiServer := httpapi.NewServer(service)
	server := httptest.NewServer(apiServer.Handler())
	defer server.Close()
	reconciler := app.NewReconciler(service, 0, 10*time.Millisecond, app.WithA2ATransport(apiServer.A2ATransport()))
	go reconciler.Run(ctx)

	workerBinary := filepath.Join(runtimeDir, "worker")
	codexBinary := filepath.Join(runtimeDir, "fake-codex")
	claudeBinary := filepath.Join(runtimeDir, "fake-claude")
	buildE2EBinary(t, root, workerBinary, "./worker/cmd/worker")
	buildE2EBinary(t, root, codexBinary, "./e2e/fixtures/fake_agent")
	buildE2EBinary(t, root, claudeBinary, "./e2e/fixtures/fake_agent")
	workerDir := filepath.Join(runtimeDir, "worker-data")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workerLogPath := filepath.Join(runtimeDir, "worker.log")
	workerLog, err := os.Create(workerLogPath)
	if err != nil {
		t.Fatal(err)
	}
	workerCommand := exec.CommandContext(ctx, workerBinary)
	workerCommand.Dir = root
	workerCommand.Env = append(os.Environ(),
		"MANAGER_WS_URL=ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws",
		"WORKER_ID=worker-e2e", "WORKER_NAME=e2e-worker", "WORKER_WORK_DIR="+workerDir,
		"WORKER_A2A_DB_PATH="+filepath.Join(workerDir, "a2a.db"),
		"WORKER_A2A_HOST=127.0.0.1", "WORKER_A2A_PORT=0",
		"WORKER_TERMINAL_ENABLED=false", "WORKER_REVIEW_ENABLED=false",
		"CODEX_BINARY="+codexBinary, "CLAUDE_BINARY="+claudeBinary,
	)
	workerCommand.Stdout = workerLog
	workerCommand.Stderr = workerLog
	if err := workerCommand.Start(); err != nil {
		_ = workerLog.Close()
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if workerCommand.Process != nil {
			_ = workerCommand.Process.Kill()
		}
		_ = workerCommand.Wait()
		_ = workerLog.Close()
	})
	if ready := eventuallyGraphQL(t, server.URL, `query Workers { workers { id status capabilities { key value } } }`, nil, func(body map[string]any) bool {
		data, _ := body["data"].(map[string]any)
		workers, _ := data["workers"].([]any)
		for _, item := range workers {
			worker := item.(map[string]any)
			if worker["id"] != "worker-e2e" || worker["status"] != "ONLINE" {
				continue
			}
			capabilities := worker["capabilities"].([]any)
			for _, raw := range capabilities {
				capability := raw.(map[string]any)
				if capability["key"] == "a2a_version" && capability["value"] == "1.0" {
					return true
				}
			}
		}
		return false
	}); ready == nil {
		t.Fatalf("worker did not register A2A capability:\n%s", readE2ELog(workerLogPath))
	}

	repository := filepath.Join(runtimeDir, "repository")
	initReviewGitRepo(t, repository)
	project := postGraphQL(t, server.URL, `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`, map[string]any{
		"input": map[string]any{"name": "Repo", "gitUrl": repository, "defaultBranch": "main", "worktreeNamePrefix": "repo"},
	})
	projectID := project["data"].(map[string]any)["createProject"].(map[string]any)["id"].(string)
	task := postGraphQL(t, server.URL, `mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, map[string]any{
		"input": map[string]any{
			"title": "E2E", "description": "BPT_RESULT_B64=" + base64.StdEncoding.EncodeToString([]byte("done")),
			"projectId": projectID, "workerId": "worker-e2e", "agentType": "codex", "baseBranch": "main",
		},
	})
	taskID := task["data"].(map[string]any)["createTask"].(map[string]any)["id"].(string)
	postGraphQL(t, server.URL, `mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }`, map[string]any{"taskId": taskID})

	loaded := eventuallyGraphQL(t, server.URL, `query Task($id: ID!) {
		task(id: $id) { id status result agentSessionId worktreePath }
		taskLogs(taskId: $id) { stream content }
		taskConversations(taskId: $id) { role content }
		taskA2AExecutions(taskId: $id) { turn operation a2aTaskId contextId remoteStatus lastSequence }
	}`, map[string]any{"id": taskID}, func(body map[string]any) bool {
		task := body["data"].(map[string]any)["task"].(map[string]any)
		return task["status"] == string(domain.TaskCompleted)
	})
	if loaded == nil {
		persistedTask, taskErr := service.Store().Task(ctx, taskID)
		rounds, roundsErr := service.Store().TaskA2ARounds(ctx, taskID)
		var intent *domain.TaskA2ADispatchIntent
		var intentErr error
		if len(rounds) > 0 {
			intent, intentErr = service.Store().A2ADispatchIntent(ctx, "dispatch_"+rounds[len(rounds)-1].CommandID)
		}
		persistedWorker, workerErr := service.Store().Worker(ctx, "worker-e2e")
		t.Fatalf("任务未完成：task=%+v taskErr=%v rounds=%+v roundsErr=%v intent=%+v intentErr=%v worker=%+v workerErr=%v\nWorker 日志：\n%s",
			persistedTask, taskErr, rounds, roundsErr, intent, intentErr, persistedWorker, workerErr, readE2ELog(workerLogPath))
	}
	data := loaded["data"].(map[string]any)
	loadedTask := data["task"].(map[string]any)
	if loadedTask["result"] != "done" || loadedTask["agentSessionId"] == "" || loadedTask["worktreePath"] == "" {
		t.Fatalf("projected task = %#v", loadedTask)
	}
	logs := data["taskLogs"].([]any)
	if !containsE2ERecord(logs, "stream", "stderr", "content", "fake codex turn started") {
		t.Fatalf("projected logs = %#v", logs)
	}
	conversations := data["taskConversations"].([]any)
	if !containsE2ERecord(conversations, "role", "assistant", "content", "done") {
		t.Fatalf("projected conversations = %#v", conversations)
	}
	executions := data["taskA2AExecutions"].([]any)
	if len(executions) != 1 {
		t.Fatalf("A2A executions = %#v", executions)
	}
	execution := executions[0].(map[string]any)
	if execution["turn"] != float64(1) || execution["operation"] != "START" || execution["a2aTaskId"] == "" ||
		execution["contextId"] == "" || execution["remoteStatus"] != "COMPLETED" || execution["lastSequence"].(float64) < 1 {
		t.Fatalf("A2A execution = %#v", execution)
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
	applyReviewWorkspaceReady(t, ctx, service, taskID, repo)

	diff := postGraphQL(t, server.URL, `query ReviewDiff($taskId: ID!) {
		taskGitDiff(taskId: $taskId, scope: UNCOMMITTED) {
			files { path staged additions deletions patch }
		}
	}`, map[string]any{"taskId": taskID})
	files := diff["data"].(map[string]any)["taskGitDiff"].(map[string]any)["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "tracked.txt" {
		t.Fatalf("review diff files = %#v", files)
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

func applyReviewWorkspaceReady(t *testing.T, ctx context.Context, service *app.Service, taskID, worktreePath string) {
	t.Helper()
	round, err := service.Store().LatestTaskA2ARound(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	intents, err := service.Store().A2ADispatchIntentsDue(ctx, time.Now().Add(100*365*24*time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	var intentID string
	for _, intent := range intents {
		if intent.RoundID == round.ID {
			intentID = intent.ID
			break
		}
	}
	if intentID == "" {
		t.Fatalf("任务 %s 缺少待发送的 A2A intent", taskID)
	}
	event := &a2aext.ExecutionEvent{
		Kind:    a2aext.EventKind,
		Version: a2aext.Version,
		Event: a2aext.EventHeader{
			ID:         uuid.Must(uuid.NewV7()).String(),
			Sequence:   round.LastSequence + 1,
			Type:       a2aext.EventWorkspaceReady,
			OccurredAt: time.Now().UTC(),
		},
		Scope: a2aext.EventScope{
			LocalTaskID: taskID,
			ExecutionID: round.ExecutionID,
			Attempt:     round.Attempt,
			Turn:        round.Turn,
			WorkerID:    round.WorkerID,
		},
		Runtime: &a2aext.RuntimeInfo{WorktreePath: worktreePath},
		Payload: map[string]any{},
	}
	if err := service.ApplyA2ADispatchUpdate(ctx, intentID, app.A2ARemoteUpdate{
		TaskID:    "a2a-review-" + round.ID,
		ContextID: "context-review-" + round.ExecutionID,
		Status:    domain.TaskA2ARemoteStatusWorking,
		Sequence:  event.Event.Sequence,
		Event:     event,
	}); err != nil {
		t.Fatalf("投影 review workspace.ready: %v", err)
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

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func buildE2EBinary(t *testing.T, root, output, packagePath string) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, packagePath)
	command.Dir = root
	command.Env = os.Environ()
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", packagePath, err, raw)
	}
}

func readE2ELog(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	return string(raw)
}

func containsE2ERecord(records []any, key, value, contentKey, content string) bool {
	for _, raw := range records {
		record, ok := raw.(map[string]any)
		if !ok || record[key] != value {
			continue
		}
		if text, ok := record[contentKey].(string); ok && strings.Contains(text, content) {
			return true
		}
	}
	return false
}

func eventuallyGraphQL(t *testing.T, baseURL, query string, variables map[string]any, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
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
