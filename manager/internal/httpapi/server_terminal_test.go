package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/frp"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestTaskTerminalRejectsTaskWithoutWorktree(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := createTerminalTask(t, service, 0, "")
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	_, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(manager.URL, "http")+"/terminal/tasks/"+task.ID+"/ws", nil)
	if err == nil {
		t.Fatal("terminal websocket without worktree connected unexpectedly")
	}
	if res == nil || res.StatusCode != http.StatusConflict {
		t.Fatalf("terminal without worktree status = %v, want 409", responseStatus(res))
	}
}

func TestTaskTerminalProxiesWebSocketToWorkerTerminal(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	worktree := "/tmp/worker/task-worktree"
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	receivedCwd := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCwd <- r.URL.Query().Get("cwd")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade worker terminal: %v", err)
			return
		}
		defer conn.Close()
		var input map[string]any
		if err := conn.ReadJSON(&input); err != nil {
			t.Errorf("read terminal input: %v", err)
			return
		}
		if input["type"] != "input" || input["data"] != "echo hi\n" {
			t.Errorf("terminal input = %+v", input)
			return
		}
		if err := conn.WriteJSON(map[string]any{"type": "output", "data": "hi\r\n"}); err != nil {
			t.Errorf("write terminal output: %v", err)
			return
		}
	}))
	defer target.Close()
	port := mustPort(t, target.URL)
	task := createTerminalTask(t, service, port, worktree)
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()
	connectFRPTunnel(t, manager.URL, "worker-terminal", "Terminal Worker")

	conn, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(manager.URL, "http")+"/terminal/tasks/"+task.ID+"/ws", nil)
	if err != nil {
		t.Fatalf("dial task terminal: %v (status=%v)", err, responseStatus(res))
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "input", "data": "echo hi\n"}); err != nil {
		t.Fatalf("write task terminal input: %v", err)
	}
	var output map[string]any
	if err := conn.ReadJSON(&output); err != nil {
		t.Fatalf("read task terminal output: %v", err)
	}
	if output["type"] != "output" || output["data"] != "hi\r\n" {
		t.Fatalf("terminal output = %+v", output)
	}

	select {
	case cwd := <-receivedCwd:
		if cwd != worktree {
			t.Fatalf("proxied terminal cwd = %q, want %q", cwd, worktree)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for proxied terminal request")
	}
}

func TestTaskTerminalCheckReturnsWorkerCwdValidationError(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	worktree := "/tmp/worker/missing-task-worktree"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/terminal/check" {
			t.Fatalf("terminal check path = %q, want /terminal/check", r.URL.Path)
		}
		if cwd := r.URL.Query().Get("cwd"); cwd != worktree {
			t.Fatalf("terminal check cwd = %q, want %q", cwd, worktree)
		}
		http.Error(w, "terminal cwd does not exist", http.StatusBadRequest)
	}))
	defer target.Close()
	task := createTerminalTask(t, service, mustPort(t, target.URL), worktree)
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()
	connectFRPTunnel(t, manager.URL, "worker-terminal", "Terminal Worker")

	res, err := http.Get(manager.URL + "/terminal/tasks/" + task.ID)
	if err != nil {
		t.Fatalf("check task terminal: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "terminal cwd does not exist") {
		t.Fatalf("terminal check response = %d %q, want 400 cwd error", res.StatusCode, body)
	}
}

func TestTaskTerminalRejectsArchivedTask(t *testing.T) {
	service := app.NewService(store.NewMemoryStore(), app.WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := createTerminalTask(t, service, 43210, "/tmp/worker/task-worktree")
	if _, err := service.ApplyWorkerTaskCompleted(context.Background(), "completed-"+task.ID, task.ID, "done", "session-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ArchiveTask(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	res, err := http.Get(manager.URL + "/terminal/tasks/" + task.ID)
	if err != nil {
		t.Fatalf("check archived task terminal: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("archived terminal check status = %d, want 409", res.StatusCode)
	}
	_, wsRes, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(manager.URL, "http")+"/terminal/tasks/"+task.ID+"/ws", nil)
	if err == nil {
		t.Fatal("archived task terminal websocket connected unexpectedly")
	}
	if wsRes == nil || wsRes.StatusCode != http.StatusConflict {
		t.Fatalf("archived terminal websocket status = %v, want 409", responseStatus(wsRes))
	}
}

func createTerminalTask(t *testing.T, service *app.Service, terminalPort int, worktree string) *domain.Task {
	t.Helper()
	ctx := context.Background()
	project, err := service.CreateProject(ctx, app.CreateProjectInput{Name: "P", GitURL: "file:///tmp/repo", DefaultBranch: "main", WorktreeNamePrefix: "p"})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]string{"terminal_enabled": "false"}
	if terminalPort > 0 {
		capabilities = map[string]string{
			"terminal_enabled": "true",
			"terminal_host":    "localhost",
			"terminal_port":    strconv.Itoa(terminalPort),
		}
	}
	worker, err := service.RegisterWorker(ctx, app.RegisterWorkerInput{
		ID:              "worker-terminal",
		Name:            "Terminal Worker",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir:         "/tmp/worker",
		BindingMode:     domain.WorkerAllProjects,
		Capabilities:    capabilities,
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
	if worktree == "" {
		return task
	}
	if _, _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = service.ApplyWorkerTaskStarted(ctx, "started-"+task.ID, task.ID, worktree)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func connectFRPTunnel(t *testing.T, managerURL, workerID, workerName string) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(managerURL, "http") + "/worker/frp?worker_id=" + workerID + "&worker_name=" + strings.ReplaceAll(workerName, " ", "%20")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial frp: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	session, err := yamux.Client(frp.NewWebSocketConn(ws), nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitForFRPTunnelReady(t, session)
	go func() {
		if err := frp.ServeWorkerProxy(nil, session); err != nil && err != yamux.ErrSessionShutdown {
			t.Errorf("worker proxy: %v", err)
		}
	}()
}

func waitForFRPTunnelReady(t *testing.T, session *yamux.Session) {
	t.Helper()
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatalf("open frp readiness stream: %v", err)
	}
	defer stream.Close()
	if err := stream.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set frp readiness stream deadline: %v", err)
	}
	var buf [1]byte
	n, err := stream.Read(buf[:])
	if err == nil {
		t.Fatalf("frp readiness stream read %d bytes, want close", n)
	}
	if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		t.Fatalf("timed out waiting for frp tunnel readiness")
	}
	if session.IsClosed() {
		t.Fatalf("frp session closed before readiness: %v", err)
	}
}

func responseStatus(res *http.Response) int {
	if res == nil {
		return 0
	}
	return res.StatusCode
}
