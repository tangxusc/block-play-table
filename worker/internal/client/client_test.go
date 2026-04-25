package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

func TestParseAgentsFiltersInvalidValues(t *testing.T) {
	agents := ParseAgents("codex, nope, claude")
	if len(agents) != 2 || agents[0] != domain.AgentCodex || agents[1] != domain.AgentClaude {
		t.Fatalf("agents = %+v", agents)
	}
}

func TestRunRequiresManagerURL(t *testing.T) {
	err := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()}).Run(context.Background())
	if err == nil {
		t.Fatal("Run without manager url should fail")
	}
}

func TestSendWorkerEventWritesEnvelope(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	received := make(chan protocol.Envelope, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		var envelope protocol.Envelope
		if err := conn.ReadJSON(&envelope); err != nil {
			t.Errorf("read json: %v", err)
			return
		}
		received <- envelope
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()})
	client.conn = conn
	if err := client.SendWorkerEvent(context.Background(), protocol.WorkerEvent{Type: protocol.MessageTaskLog, TaskID: "task-1", Stream: "stdout", Content: "hi"}); err != nil {
		t.Fatalf("SendWorkerEvent returned error: %v", err)
	}
	select {
	case envelope := <-received:
		if envelope.Type != protocol.MessageTaskLog || envelope.WorkerID != "worker-1" || envelope.TaskID != "task-1" {
			t.Fatalf("envelope = %+v", envelope)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for envelope")
	}
}

func TestSendWorkerEventSerializesConcurrentWrites(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	const total = 32
	received := make(chan protocol.Envelope, total)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var envelope protocol.Envelope
			if err := conn.ReadJSON(&envelope); err != nil {
				return
			}
			received <- envelope
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()})
	client.conn = conn

	start := make(chan struct{})
	errs := make(chan error, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- client.SendWorkerEvent(context.Background(), protocol.WorkerEvent{
				Type:    protocol.MessageTaskLog,
				TaskID:  "task-1",
				Stream:  "stdout",
				Content: strings.Repeat("x", 64*1024),
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("SendWorkerEvent returned error: %v", err)
		}
	}

	for i := 0; i < total; i++ {
		select {
		case envelope := <-received:
			if envelope.Type != protocol.MessageTaskLog || envelope.WorkerID != "worker-1" || envelope.TaskID != "task-1" {
				t.Fatalf("envelope = %+v", envelope)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for envelope %d/%d", i+1, total)
		}
	}
}

func TestConnectAndServeSendsRegistration(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	registered := make(chan protocol.Envelope, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("token"); got != "secret" {
			t.Errorf("token query = %q, want secret", got)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		var envelope protocol.Envelope
		if err := conn.ReadJSON(&envelope); err != nil {
			t.Errorf("read register: %v", err)
			return
		}
		registered <- envelope
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := New(Config{
		ManagerWSURL:    "ws" + strings.TrimPrefix(server.URL, "http"),
		WorkerID:        "worker-1",
		WorkerToken:     "secret",
		Name:            "W",
		WorkDir:         t.TempDir(),
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
	}).connectAndServe(ctx)
	if err == nil {
		t.Fatal("connectAndServe should return when server closes")
	}
	select {
	case envelope := <-registered:
		if envelope.Type != protocol.MessageWorkerRegister || envelope.WorkerID != "worker-1" {
			t.Fatalf("register envelope = %+v", envelope)
		}
	default:
		t.Fatal("registration envelope was not sent")
	}
}
