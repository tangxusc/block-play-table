package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestWorkerGatewayProxiesTaskReviewThroughFRP(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	}))
	worktree := "/tmp/worker/task-review-worktree"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/review/tasks/task-review/diff" {
			t.Fatalf("review path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("scope") != "UNCOMMITTED" || r.URL.Query().Get("cwd") != worktree {
			t.Fatalf("review query = %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"taskId": "task-review",
			"scope":  "UNCOMMITTED",
			"files":  []map[string]any{{"path": "README.md", "status": "M", "patch": "@@ diff"}},
		})
	}))
	defer target.Close()
	task := createReviewTask(t, service, mustPort(t, target.URL), worktree)
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()
	connectFRPTunnel(t, manager.URL, "worker-review", "Review Worker")

	var response map[string]any
	if err := NewServer(service).gateway.ProxyTaskReview(context.Background(), task.ID, http.MethodGet, "/diff?scope=UNCOMMITTED", nil, &response); err == nil {
		t.Fatal("untracked gateway should not have a tunnel")
	}
	gateway := NewServer(service).gateway
	connectReviewTunnelToGateway(t, gateway, "worker-review", "Review Worker")
	if err := gateway.ProxyTaskReview(context.Background(), task.ID, http.MethodGet, "/diff?scope=UNCOMMITTED", nil, &response); err != nil {
		t.Fatalf("ProxyTaskReview returned error: %v", err)
	}
	files := response["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "README.md" {
		t.Fatalf("review proxy response = %+v", response)
	}
}

func TestGraphQLTaskReviewDiffActionsAndFeedbackLoop(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	}))
	worktree := "/tmp/worker/task-review-worktree"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/review/tasks/task-review/diff":
			if r.URL.Query().Get("scope") != "UNCOMMITTED" || r.URL.Query().Get("cwd") != worktree {
				t.Fatalf("diff query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"taskId":      "task-review",
				"scope":       "UNCOMMITTED",
				"generatedAt": time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC),
				"files": []map[string]any{{
					"path":      "README.md",
					"status":    "M",
					"staged":    false,
					"additions": 2,
					"deletions": 1,
					"patch":     "@@ -1 +1 @@\n-old\n+new\n",
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/review/tasks/task-review/git-status":
			if r.URL.Query().Get("remote") != "upstream" || r.URL.Query().Get("branch") != "develop" || r.URL.Query().Get("cwd") != worktree {
				t.Fatalf("git-status query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"taskId":             "task-review",
				"remote":             "upstream",
				"branch":             "develop",
				"currentBranch":      "task/task-review",
				"headRef":            "abc123",
				"targetRef":          "def456",
				"ahead":              2,
				"behind":             1,
				"hasStagedChanges":   true,
				"hasUnstagedChanges": false,
				"hasUntrackedFiles":  true,
				"generatedAt":        time.Date(2026, 5, 5, 10, 0, 4, 0, time.UTC),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/review/tasks/task-review/discard":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"backup": map[string]any{
					"id":        "backup-worker",
					"taskId":    "task-review",
					"paths":     []string{"README.md"},
					"patchPath": "/tmp/worker/.review-backups/task-review/backup-worker.patch",
					"createdAt": time.Date(2026, 5, 5, 10, 0, 2, 0, time.UTC),
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/review/tasks/task-review/git-command":
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input["command"] != "COMMIT" || input["message"] != "review commit" {
				t.Fatalf("git command input = %#v", input)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      true,
				"command": "COMMIT",
				"output":  "[task/task-review abc123] review commit",
				"headRef": "abc123",
				"baseRef": "def456",
				"diff": map[string]any{
					"taskId":      "task-review",
					"scope":       "UNCOMMITTED",
					"generatedAt": time.Date(2026, 5, 5, 10, 0, 3, 0, time.UTC),
					"files":       []map[string]any{},
				},
			})
		case r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/stage") || strings.HasSuffix(r.URL.Path, "/unstage") || strings.HasSuffix(r.URL.Path, "/restore")):
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected review request %s %s", r.Method, r.URL.String())
		}
	}))
	defer target.Close()

	server := httptest.NewServer(NewServer(service).Handler())
	defer server.Close()
	task := createReviewTask(t, service, mustPort(t, target.URL), worktree)
	connectFRPTunnel(t, server.URL, "worker-review", "Review Worker")

	diff := postGraphQL(t, server.URL, `query ReviewDiff($taskId: ID!) {
		taskGitDiff(taskId: $taskId, scope: UNCOMMITTED) {
			taskId scope files { path status additions deletions patch }
		}
	}`, map[string]any{"taskId": task.ID})
	diffData := diff["data"].(map[string]any)["taskGitDiff"].(map[string]any)
	if diffData["scope"] != "UNCOMMITTED" || diffData["files"].([]any)[0].(map[string]any)["path"] != "README.md" {
		t.Fatalf("taskGitDiff = %#v", diffData)
	}

	statusResult := postGraphQL(t, server.URL, `query GitStatus($taskId: ID!) {
		taskGitStatus(taskId: $taskId, remote: "upstream", branch: "develop") {
			taskId remote branch currentBranch headRef targetRef ahead behind hasStagedChanges hasUnstagedChanges hasUntrackedFiles
		}
	}`, map[string]any{"taskId": task.ID})
	status := statusResult["data"].(map[string]any)["taskGitStatus"].(map[string]any)
	if status["remote"] != "upstream" || status["branch"] != "develop" || status["ahead"] != float64(2) || status["behind"] != float64(1) || status["hasUntrackedFiles"] != true {
		t.Fatalf("taskGitStatus = %#v", status)
	}

	for name, query := range map[string]string{
		"start review": `mutation {
			startTaskReview(input: { taskId: "task-review", scope: UNCOMMITTED }) { id }
		}`,
		"add comment": `mutation {
			addTaskReviewComment(input: { taskId: "task-review", path: "README.md", line: 4, body: "Please tighten this." }) { id }
		}`,
		"review lists": `query {
			taskReviewRuns(taskId: "task-review") { id }
			taskReviewFindings(taskId: "task-review", status: OPEN) { id }
			taskReviewComments(taskId: "task-review") { id }
		}`,
		"finding status": `mutation {
			resolveTaskReviewFinding(id: "finding-worker") { id }
			dismissTaskReviewFinding(id: "finding-worker") { id }
		}`,
		"feedback continue": `mutation {
			continueTaskWithReviewFeedback(input: { taskId: "task-review" }) { id }
		}`,
	} {
		if err := postGraphQLError(server.URL, query, nil); err == nil {
			t.Fatalf("%s GraphQL operation should be rejected after review feedback removal", name)
		}
	}

	discardResult := postGraphQL(t, server.URL, `mutation Discard($taskId: ID!) {
		discardTaskGitChanges(input: { taskId: $taskId, paths: ["README.md"] }) {
			ok backup { id paths patchPath }
		}
	}`, map[string]any{"taskId": task.ID})
	backup := discardResult["data"].(map[string]any)["discardTaskGitChanges"].(map[string]any)["backup"].(map[string]any)
	if backup["id"] != "backup-worker" || backup["paths"].([]any)[0] != "README.md" {
		t.Fatalf("discardTaskGitChanges backup = %#v", backup)
	}

	commandResult := postGraphQL(t, server.URL, `mutation GitCommand($taskId: ID!) {
		runTaskGitCommand(input: { taskId: $taskId, command: COMMIT, message: "review commit" }) {
			ok command output headRef baseRef diff { taskId scope files { path } }
		}
	}`, map[string]any{"taskId": task.ID})
	command := commandResult["data"].(map[string]any)["runTaskGitCommand"].(map[string]any)
	if command["command"] != "COMMIT" || command["headRef"] != "abc123" || command["baseRef"] != "def456" {
		t.Fatalf("runTaskGitCommand = %#v", command)
	}

	lists := postGraphQL(t, server.URL, `query ReviewLists($taskId: ID!) {
		taskGitBackups(taskId: $taskId) { id paths }
	}`, map[string]any{"taskId": task.ID})
	listsData := lists["data"].(map[string]any)
	if len(listsData["taskGitBackups"].([]any)) != 1 {
		t.Fatalf("review lists = %#v", listsData)
	}
}

func createReviewTask(t *testing.T, service *app.Service, reviewPort int, worktree string) *domain.Task {
	t.Helper()
	ctx := context.Background()
	project, err := service.CreateProject(ctx, app.CreateProjectInput{Name: "P", GitURL: "file:///tmp/repo", DefaultBranch: "main", WorktreeNamePrefix: "p"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, app.RegisterWorkerInput{
		ID:              "worker-review",
		Name:            "Review Worker",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
		Capabilities: map[string]string{
			"review_enabled": "true",
			"review_host":    "localhost",
			"review_port":    strconv.Itoa(reviewPort),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, app.CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex, WorkerID: worker.ID})
	if err != nil {
		t.Fatal(err)
	}
	task.ID = "task-review"
	if err := service.Store().SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	return applyHTTPAPITestA2AEvent(t, ctx, service, task.ID, a2aext.EventWorkspaceReady, domain.TaskA2ARemoteStatusWorking, &a2aext.RuntimeInfo{WorktreePath: worktree}, map[string]any{})
}

func connectReviewTunnelToGateway(t *testing.T, gateway *WorkerGateway, workerID, workerName string) {
	t.Helper()
	manager := httptest.NewServer(http.HandlerFunc(gateway.HandleFRP))
	t.Cleanup(manager.Close)
	connectFRPTunnel(t, manager.URL, workerID, strings.ReplaceAll(workerName, " ", "%20"))
}
