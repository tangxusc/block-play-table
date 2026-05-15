package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestStripProxyPathHandlesAllForms(t *testing.T) {
	cases := map[string]string{
		"":                "/",
		"/proxy":          "/",
		"/proxy/":         "/",
		"/proxy/foo/bar":  "/foo/bar",
		"/other":          "/other",
		"/proxy-not-here": "/proxy-not-here",
	}
	for input, want := range cases {
		if got := stripProxyPath(input); got != want {
			t.Fatalf("stripProxyPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTaskIDFromEnvelopePrefersEnvelopeID(t *testing.T) {
	envelope := rawEnvelope{TaskID: "task-from-envelope"}
	event := protocol.WorkerEvent{TaskID: "task-from-event"}
	if got := taskIDFromEnvelope(envelope, event); got != "task-from-envelope" {
		t.Fatalf("envelope precedence = %q", got)
	}
	if got := taskIDFromEnvelope(rawEnvelope{}, event); got != "task-from-event" {
		t.Fatalf("event fallback = %q", got)
	}
	if got := taskIDFromEnvelope(rawEnvelope{}, protocol.WorkerEvent{}); got != "" {
		t.Fatalf("empty result = %q", got)
	}
}

func TestCloneMetadataCopiesEntries(t *testing.T) {
	source := map[string]string{"a": "1", "b": "2"}
	clone := cloneMetadata(source)
	if len(clone) != len(source) {
		t.Fatalf("clone size = %d, want %d", len(clone), len(source))
	}
	clone["a"] = "mutated"
	if source["a"] != "1" {
		t.Fatalf("clone mutation leaked into source: %q", source["a"])
	}
	empty := cloneMetadata(nil)
	if len(empty) != 0 {
		t.Fatalf("nil input should produce empty map, got %+v", empty)
	}
}

func TestReviewCapabilityTargetValidates(t *testing.T) {
	if _, _, err := reviewCapabilityTarget(map[string]string{}); err == nil {
		t.Fatal("missing review_enabled should error")
	}
	if _, _, err := reviewCapabilityTarget(map[string]string{"review_enabled": "true"}); err == nil {
		t.Fatal("missing review_port should error")
	}
	if _, _, err := reviewCapabilityTarget(map[string]string{"review_enabled": "true", "review_port": "0"}); err == nil {
		t.Fatal("invalid review_port should error")
	}
	host, port, err := reviewCapabilityTarget(map[string]string{"review_enabled": "TRUE", "review_port": "12345"})
	if err != nil {
		t.Fatalf("valid capability returned error: %v", err)
	}
	if host != "127.0.0.1" || port != 12345 {
		t.Fatalf("default host/port = %s:%d, want 127.0.0.1:12345", host, port)
	}
	host, port, err = reviewCapabilityTarget(map[string]string{"review_enabled": "true", "review_host": "10.0.0.5", "review_port": "8080"})
	if err != nil {
		t.Fatalf("explicit capability returned error: %v", err)
	}
	if host != "10.0.0.5" || port != 8080 {
		t.Fatalf("explicit host/port = %s:%d, want 10.0.0.5:8080", host, port)
	}
}

func TestTaskReviewPathBuildsRouteWithQuery(t *testing.T) {
	got, err := taskReviewPath("task-1", "", "/tmp/work", "feature", "main")
	if err != nil {
		t.Fatalf("default path returned error: %v", err)
	}
	if !strings.HasPrefix(got, "/review/tasks/task-1/diff") {
		t.Fatalf("default path = %q", got)
	}
	if !strings.Contains(got, "cwd=%2Ftmp%2Fwork") {
		t.Fatalf("default path missing cwd: %q", got)
	}
	if !strings.Contains(got, "baseBranch=feature") || !strings.Contains(got, "defaultBranch=main") {
		t.Fatalf("default path missing branches: %q", got)
	}

	got, err = taskReviewPath("task-1", "stage?paths=foo", "/tmp/work", "", "")
	if err != nil {
		t.Fatalf("stage path returned error: %v", err)
	}
	if !strings.HasPrefix(got, "/review/tasks/task-1/stage") {
		t.Fatalf("stage path = %q", got)
	}
	if !strings.Contains(got, "paths=foo") {
		t.Fatalf("stage path missing paths: %q", got)
	}
}

func TestTerminalPathParsersRejectInvalidInput(t *testing.T) {
	taskCases := map[string]bool{
		"/terminal/tasks/task-1/ws":       true,
		"/terminal/tasks//ws":             false,
		"/terminal/tasks/task/sub/ws":     false,
		"/different/prefix":               false,
		"/terminal/tasks/task-1":          false,
	}
	for path, want := range taskCases {
		_, ok := terminalTaskIDFromPath(path)
		if ok != want {
			t.Fatalf("terminalTaskIDFromPath(%q) ok = %v, want %v", path, ok, want)
		}
	}
	checkCases := map[string]bool{
		"/terminal/tasks/task-1": true,
		"/terminal/tasks/":       false,
		"/terminal/tasks/a/b":    false,
		"/wrong":                 false,
	}
	for path, want := range checkCases {
		_, ok := terminalTaskIDFromCheckPath(path)
		if ok != want {
			t.Fatalf("terminalTaskIDFromCheckPath(%q) ok = %v, want %v", path, ok, want)
		}
	}
	workerCases := map[string]bool{
		"/terminal/workers/worker-1/ws": true,
		"/terminal/workers//ws":         false,
		"/terminal/workers/a/b/ws":      false,
		"/different":                    false,
	}
	for path, want := range workerCases {
		_, ok := workerTerminalIDFromPath(path)
		if ok != want {
			t.Fatalf("workerTerminalIDFromPath(%q) ok = %v, want %v", path, ok, want)
		}
	}
	workerCheckCases := map[string]bool{
		"/terminal/workers/worker-1": true,
		"/terminal/workers/":         false,
		"/terminal/workers/a/b":      false,
		"/different":                 false,
	}
	for path, want := range workerCheckCases {
		_, ok := workerTerminalIDFromCheckPath(path)
		if ok != want {
			t.Fatalf("workerTerminalIDFromCheckPath(%q) ok = %v, want %v", path, ok, want)
		}
	}
}

func TestResolveTaskReviewRejectsInvalidStates(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time { return now }))
	gateway := NewServer(service).gateway

	if err := gateway.ProxyTaskReview(ctx, "missing-task", http.MethodGet, "/diff", nil, nil); err == nil {
		t.Fatal("missing task should error")
	} else if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing task err = %v, want ErrNotFound", err)
	}

	project, err := service.CreateProject(ctx, app.CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, app.RegisterWorkerInput{
		ID:              "worker-review",
		Name:            "rev",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
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
	if err := gateway.ProxyTaskReview(ctx, task.ID, http.MethodGet, "/diff", nil, nil); err == nil {
		t.Fatal("task without worktree should error")
	}

	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorkerTaskStarted(ctx, "started-review", task.ID, "/tmp/worktree"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerDisconnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if err := gateway.ProxyTaskReview(ctx, task.ID, http.MethodGet, "/diff", nil, nil); err == nil {
		t.Fatal("offline worker should error")
	}
}

func TestHandleFRPRejectsMissingTokenAndIDs(t *testing.T) {
	api := NewServer(app.NewService(store.NewMemoryStore()), WithWorkerToken("secret"))
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	res, err := http.Get(server.URL + "/worker/frp")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", res.StatusCode)
	}
	res.Body.Close()

	res, err = http.Get(server.URL + "/worker/frp?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing worker id status = %d, want 400", res.StatusCode)
	}
	res.Body.Close()
}

func TestHandleProxyRejectsMissingHeaders(t *testing.T) {
	api := NewServer(app.NewService(store.NewMemoryStore()))
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	res, err := http.Get(server.URL + "/proxy/foo")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing worker headers status = %d, want 400", res.StatusCode)
	}
	res.Body.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/proxy/foo", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("worker", "missing-worker")
	req.Header.Set("worker_port", "8080")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing tunnel status = %d, want 503", res.StatusCode)
	}
	res.Body.Close()
}

func TestTerminalTargetValidates(t *testing.T) {
	if _, _, err := terminalTarget(map[string]string{}); err == nil {
		t.Fatal("missing terminal_enabled should error")
	}
	if _, _, err := terminalTarget(map[string]string{"terminal_enabled": "true"}); err == nil {
		t.Fatal("missing terminal_port should error")
	}
	host, port, err := terminalTarget(map[string]string{"terminal_enabled": "TRUE", "terminal_port": "9000"})
	if err != nil {
		t.Fatalf("valid capability returned error: %v", err)
	}
	if host != "127.0.0.1" || port != 9000 {
		t.Fatalf("default host/port = %s:%d", host, port)
	}
	host, port, err = terminalTarget(map[string]string{"terminal_enabled": "true", "terminal_host": "10.1.2.3", "terminal_port": "5555"})
	if err != nil {
		t.Fatalf("explicit capability returned error: %v", err)
	}
	if host != "10.1.2.3" || port != 5555 {
		t.Fatalf("explicit host/port = %s:%d", host, port)
	}
}
