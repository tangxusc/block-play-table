package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestWorkerTerminalProxiesWebSocketToWorkerTerminal(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	workdir := t.TempDir()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cwd := r.URL.Query().Get("cwd"); cwd != workdir {
			t.Fatalf("terminal cwd = %q, want %q", cwd, workdir)
		}
		if r.URL.Path == "/terminal/check" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if r.URL.Path != "/terminal/ws" {
			t.Fatalf("terminal ws path = %q, want /terminal/ws", r.URL.Path)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade worker terminal: %v", err)
		}
		defer conn.Close()
		var input map[string]any
		if err := conn.ReadJSON(&input); err != nil {
			t.Fatalf("read terminal input: %v", err)
		}
		if input["type"] != "input" || input["data"] != "pwd\n" {
			t.Fatalf("terminal input = %+v", input)
		}
		if err := conn.WriteJSON(map[string]any{"type": "output", "data": workdir + "\r\n"}); err != nil {
			t.Fatalf("write terminal output: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{"type": "exit"}); err != nil {
			t.Fatalf("write terminal exit: %v", err)
		}
	}))
	defer target.Close()
	port := mustPort(t, target.URL)
	worker := createWorkerTerminalTarget(t, service, port, workdir, true)
	if _, err := service.WorkerConnected(context.Background(), worker.ID); err != nil {
		t.Fatal(err)
	}
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()
	connectFRPTunnel(t, manager.URL, worker.ID, worker.Name)

	res, err := http.Get(manager.URL + "/terminal/workers/" + worker.ID)
	if err != nil {
		t.Fatalf("check worker terminal: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("worker terminal check status = %d body=%q", res.StatusCode, body)
	}

	conn, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(manager.URL, "http")+"/terminal/workers/"+worker.ID+"/ws", nil)
	if err != nil {
		t.Fatalf("dial worker terminal websocket: %v (status=%v)", err, responseStatus(res))
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "input", "data": "pwd\n"}); err != nil {
		t.Fatalf("write worker terminal input: %v", err)
	}
	var output map[string]any
	if err := conn.ReadJSON(&output); err != nil {
		t.Fatalf("read worker terminal output: %v", err)
	}
	if output["type"] != "output" || output["data"] != workdir+"\r\n" {
		t.Fatalf("worker terminal output = %+v", output)
	}
}

func TestWorkerTerminalCheckRejectsOfflineWorker(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	worker := createWorkerTerminalTarget(t, service, 42001, t.TempDir(), true)
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	res, err := http.Get(manager.URL + "/terminal/workers/" + worker.ID)
	if err != nil {
		t.Fatalf("check offline worker terminal: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("offline worker terminal status = %d, want 409", res.StatusCode)
	}
}

func TestWorkerTerminalCheckRejectsDisabledTerminal(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	worker := createWorkerTerminalTarget(t, service, 42001, t.TempDir(), false)
	if _, err := service.WorkerConnected(context.Background(), worker.ID); err != nil {
		t.Fatal(err)
	}
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	res, err := http.Get(manager.URL + "/terminal/workers/" + worker.ID)
	if err != nil {
		t.Fatalf("check disabled terminal: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("disabled worker terminal status = %d, want 409", res.StatusCode)
	}
}

func TestWorkerTerminalCheckReturnsWorkerCwdValidationError(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	workdir := t.TempDir()
	missing := filepath.Join(workdir, "missing")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/terminal/check" {
			t.Fatalf("terminal check path = %q, want /terminal/check", r.URL.Path)
		}
		if cwd := r.URL.Query().Get("cwd"); cwd != missing {
			t.Fatalf("terminal check cwd = %q, want %q", cwd, missing)
		}
		http.Error(w, "terminal cwd does not exist", http.StatusBadRequest)
	}))
	defer target.Close()
	port := mustPort(t, target.URL)
	worker := createWorkerTerminalTarget(t, service, port, missing, true)
	if _, err := service.WorkerConnected(context.Background(), worker.ID); err != nil {
		t.Fatal(err)
	}
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()
	connectFRPTunnel(t, manager.URL, worker.ID, worker.Name)

	res, err := http.Get(manager.URL + "/terminal/workers/" + worker.ID)
	if err != nil {
		t.Fatalf("check worker terminal cwd: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "terminal cwd does not exist") {
		t.Fatalf("worker terminal check response = %d %q, want 400 cwd error", res.StatusCode, body)
	}
}

func TestWorkerTerminalRejectsMissingTunnel(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	worker := createWorkerTerminalTarget(t, service, 42001, t.TempDir(), true)
	if _, err := service.WorkerConnected(context.Background(), worker.ID); err != nil {
		t.Fatal(err)
	}
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	res, err := http.Get(manager.URL + "/terminal/workers/" + worker.ID)
	if err != nil {
		t.Fatalf("check missing tunnel terminal: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing tunnel worker terminal status = %d, want 503", res.StatusCode)
	}
}

func createWorkerTerminalTarget(t *testing.T, service *app.Service, terminalPort int, workdir string, enabled bool) *domain.Worker {
	t.Helper()
	worker, err := service.RegisterWorker(context.Background(), app.RegisterWorkerInput{
		ID:              "worker-terminal-" + strconv.Itoa(terminalPort),
		Name:            "Worker Terminal " + strconv.Itoa(terminalPort),
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         workdir,
		BindingMode:     domain.WorkerAllProjects,
		Capabilities: map[string]string{
			"terminal_enabled": map[bool]string{true: "true", false: "false"}[enabled],
			"terminal_host":    "localhost",
			"terminal_port":    strconv.Itoa(terminalPort),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
