package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestTerminalPathParsersCoverMalformedAndTrimmedIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		parse func(string) (string, bool)
		path  string
		want  string
		ok    bool
	}{
		{"task ws prefix", terminalTaskIDFromPath, "/other/task/ws", "", false},
		{"task ws suffix", terminalTaskIDFromPath, "/terminal/tasks/task/check", "", false},
		{"task ws empty", terminalTaskIDFromPath, "/terminal/tasks//ws", "", false},
		{"task ws slash", terminalTaskIDFromPath, "/terminal/tasks/a/b/ws", "", false},
		{"task ws escape", terminalTaskIDFromPath, "/terminal/tasks/%zz/ws", "", false},
		{"task ws whitespace", terminalTaskIDFromPath, "/terminal/tasks/%20/ws", "", false},
		{"task ws trim", terminalTaskIDFromPath, "/terminal/tasks/%20task%20/ws", "task", true},
		{"task check prefix", terminalTaskIDFromCheckPath, "/other/task", "", false},
		{"task check empty", terminalTaskIDFromCheckPath, "/terminal/tasks/", "", false},
		{"task check slash", terminalTaskIDFromCheckPath, "/terminal/tasks/a/b", "", false},
		{"task check escape", terminalTaskIDFromCheckPath, "/terminal/tasks/%zz", "", false},
		{"task check whitespace", terminalTaskIDFromCheckPath, "/terminal/tasks/%20", "", false},
		{"worker ws prefix", workerTerminalIDFromPath, "/other/worker/ws", "", false},
		{"worker ws suffix", workerTerminalIDFromPath, "/terminal/workers/worker/check", "", false},
		{"worker ws empty", workerTerminalIDFromPath, "/terminal/workers//ws", "", false},
		{"worker ws slash", workerTerminalIDFromPath, "/terminal/workers/a/b/ws", "", false},
		{"worker ws escape", workerTerminalIDFromPath, "/terminal/workers/%zz/ws", "", false},
		{"worker ws whitespace", workerTerminalIDFromPath, "/terminal/workers/%20/ws", "", false},
		{"worker ws trim", workerTerminalIDFromPath, "/terminal/workers/%20worker%20/ws", "worker", true},
		{"worker check prefix", workerTerminalIDFromCheckPath, "/other/worker", "", false},
		{"worker check empty", workerTerminalIDFromCheckPath, "/terminal/workers/", "", false},
		{"worker check slash", workerTerminalIDFromCheckPath, "/terminal/workers/a/b", "", false},
		{"worker check escape", workerTerminalIDFromCheckPath, "/terminal/workers/%zz", "", false},
		{"worker check whitespace", workerTerminalIDFromCheckPath, "/terminal/workers/%20", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := test.parse(test.path)
			if got != test.want || ok != test.ok {
				t.Fatalf("parse(%q) = (%q,%v), want (%q,%v)", test.path, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestProxyWebTargetPathAndHeaderValidationBranches(t *testing.T) {
	for _, path := range []string{"/outside", "/proxy/web/worker", "/proxy/web/%20/127.0.0.1/8080", "/proxy/web/%zz/127.0.0.1/8080", "/proxy/web/worker/%zz/8080", "/proxy/web/worker/a:b/8080", "/proxy/web/worker/127.0.0.1/bad"} {
		_, matched, err := proxyWebTargetFromPath(path)
		if path == "/outside" {
			if matched || err != nil {
				t.Fatalf("非 web proxy 路径被匹配: matched=%v err=%v", matched, err)
			}
			continue
		}
		if !matched || err == nil {
			t.Fatalf("非法路径 %q = matched=%v err=%v", path, matched, err)
		}
	}
	for _, path := range []string{"/proxy/web/worker/127.0.0.1/8080", "/proxy/web/worker/127.0.0.1/8080/"} {
		target, matched, err := proxyWebTargetFromPath(path)
		if err != nil || !matched || target.path != "/" {
			t.Fatalf("默认 target path %q = %+v, %v", path, target, err)
		}
	}
	target, matched, err := proxyWebTargetFromPath("/proxy/web/%20worker%20/localhost/8080/api/health")
	if err != nil || !matched || target.workerName != "worker" || target.host != "localhost" || target.port != 8080 || target.path != "/api/health" {
		t.Fatalf("合法 web proxy = %+v, matched=%v err=%v", target, matched, err)
	}

	request := httptest.NewRequest("GET", "/proxy/api", nil)
	if _, err := proxyTargetFromRequest(request); err == nil {
		t.Fatal("缺少 proxy header 未失败")
	}
	request.Header.Set("worker", "worker")
	request.Header.Set("worker_port", "bad")
	if _, err := proxyTargetFromRequest(request); err == nil {
		t.Fatal("非法 header port 未失败")
	}
	request.Header.Set("worker_port", "8080")
	request.Header.Set("worker_host", "bad/host")
	if _, err := proxyTargetFromRequest(request); err == nil {
		t.Fatal("非法 header host 未失败")
	}
	request.Header.Set("worker_host", "127.0.0.1")
	target, err = proxyTargetFromRequest(request)
	if err != nil || target.path != "/api" || target.port != 8080 {
		t.Fatalf("合法 header proxy = %+v, %v", target, err)
	}
}

func TestTerminalHandlersRejectMethodsPathsAndMissingResources(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	gateway := NewWorkerGateway(service, nil, "")
	for _, test := range []struct {
		handler http.HandlerFunc
		method  string
		path    string
		status  int
	}{
		{gateway.HandleTaskTerminal, http.MethodPost, "/terminal/tasks/task", http.StatusMethodNotAllowed},
		{gateway.HandleTaskTerminal, http.MethodGet, "/terminal/tasks/a/b/ws", http.StatusNotFound},
		{gateway.HandleTaskTerminal, http.MethodGet, "/terminal/tasks/missing", http.StatusNotFound},
		{gateway.HandleWorkerTerminal, http.MethodPost, "/terminal/workers/worker", http.StatusMethodNotAllowed},
		{gateway.HandleWorkerTerminal, http.MethodGet, "/terminal/workers/a/b/ws", http.StatusNotFound},
		{gateway.HandleWorkerTerminal, http.MethodGet, "/terminal/workers/missing", http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		test.handler(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.status {
			t.Fatalf("%s %s = %d, want %d", test.method, test.path, recorder.Code, test.status)
		}
	}

	ctx := context.Background()
	storage := service.Store()
	if err := storage.SaveTask(ctx, &domain.Task{ID: "no-worker", Status: domain.TaskRunning}); err != nil {
		t.Fatal(err)
	}
	if _, status, _ := gateway.resolveTaskTerminal(ctx, "no-worker"); status != http.StatusConflict {
		t.Fatalf("无 Worker Task terminal status = %d", status)
	}
	if err := storage.SaveTask(ctx, &domain.Task{ID: "missing-worker", WorkerID: "absent", WorktreePath: "/tmp", Status: domain.TaskRunning}); err != nil {
		t.Fatal(err)
	}
	if _, status, _ := gateway.resolveTaskTerminal(ctx, "missing-worker"); status != http.StatusNotFound {
		t.Fatalf("Worker 缺失 Task terminal status = %d", status)
	}
}

func TestTerminalResolversRejectUnavailableWorkersCapabilitiesAndTunnels(t *testing.T) {
	ctx := context.Background()
	service := app.NewService(store.NewMemoryStore())
	gateway := NewWorkerGateway(service, nil, "")
	storage := service.Store()

	workers := []*domain.Worker{
		{ID: "offline", Name: "offline", Status: domain.WorkerOffline},
		{ID: "disabled-terminal", Name: "disabled-terminal", Status: domain.WorkerOnline},
		{ID: "invalid-terminal", Name: "invalid-terminal", Status: domain.WorkerOnline, Capabilities: map[string]string{
			"terminal_enabled": "true", "terminal_host": "bad/host", "terminal_port": "8080",
		}},
		{ID: "no-tunnel", Name: "no-tunnel", Status: domain.WorkerOnline, Capabilities: map[string]string{
			"terminal_enabled": "true", "terminal_port": "8080",
		}},
	}
	for _, worker := range workers {
		if err := storage.SaveWorker(ctx, worker); err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		workerID string
		status   int
	}{
		{"missing", http.StatusNotFound},
		{"offline", http.StatusConflict},
		{"disabled-terminal", http.StatusConflict},
		{"invalid-terminal", http.StatusConflict},
		{"no-tunnel", http.StatusServiceUnavailable},
	} {
		if _, status, _ := gateway.resolveWorkerTerminal(ctx, test.workerID); status != test.status {
			t.Fatalf("Worker %s terminal status = %d, want %d", test.workerID, status, test.status)
		}
	}

	tasks := []*domain.Task{
		{ID: "archived", Status: domain.TaskArchived},
		{ID: "no-worktree", WorkerID: "no-tunnel", Status: domain.TaskRunning},
		{ID: "offline-task", WorkerID: "offline", WorktreePath: "/tmp/offline", Status: domain.TaskRunning},
		{ID: "disabled-task", WorkerID: "disabled-terminal", WorktreePath: "/tmp/disabled", Status: domain.TaskRunning},
		{ID: "invalid-task", WorkerID: "invalid-terminal", WorktreePath: "/tmp/invalid", Status: domain.TaskRunning},
		{ID: "no-tunnel-task", WorkerID: "no-tunnel", WorktreePath: "/tmp/no-tunnel", Status: domain.TaskRunning},
	}
	for _, task := range tasks {
		if err := storage.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		taskID string
		status int
	}{
		{"archived", http.StatusConflict},
		{"no-worktree", http.StatusConflict},
		{"offline-task", http.StatusConflict},
		{"disabled-task", http.StatusConflict},
		{"invalid-task", http.StatusConflict},
		{"no-tunnel-task", http.StatusServiceUnavailable},
	} {
		if _, status, _ := gateway.resolveTaskTerminal(ctx, test.taskID); status != test.status {
			t.Fatalf("Task %s terminal status = %d, want %d", test.taskID, status, test.status)
		}
	}
}

func TestWorkerGatewayApplyControlMessageBranches(t *testing.T) {
	ctx := context.Background()
	service := app.NewService(store.NewMemoryStore())
	gateway := NewWorkerGateway(service, nil, "")
	if err := gateway.apply(ctx, rawEnvelope{Type: protocol.MessageWorkerRegister, Payload: json.RawMessage("{")}, "fallback"); err == nil {
		t.Fatal("畸形注册 payload 未失败")
	}
	if err := gateway.apply(ctx, rawEnvelope{Type: protocol.MessageWorkerRegister}, ""); err == nil {
		t.Fatal("空 Worker 注册未失败")
	}
	payload, err := json.Marshal(app.RegisterWorkerInput{
		ID: "worker", Name: "Worker", WorkDir: "/tmp/worker", SupportedAgents: []domain.AgentType{domain.AgentCodex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.apply(ctx, rawEnvelope{Type: protocol.MessageWorkerRegister, Payload: payload}, "fallback"); err != nil {
		t.Fatal(err)
	}
	if err := gateway.apply(ctx, rawEnvelope{Type: protocol.MessageWorkerHeartbeat}, "worker"); err != nil {
		t.Fatal(err)
	}
	if err := gateway.apply(ctx, rawEnvelope{Type: protocol.MessageWorkerHeartbeat}, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("缺失 Worker 心跳错误 = %v", err)
	}
	if err := gateway.apply(ctx, rawEnvelope{Type: protocol.MessageType("TASK_START")}, "worker"); err == nil {
		t.Fatal("旧 TASK_* 控制消息未拒绝")
	}
}
