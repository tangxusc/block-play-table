package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/worker/internal/executor"
)

func TestSendWorkerEventRequiresConnection(t *testing.T) {
	client := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()})
	if err := client.SendWorkerEvent(context.Background(), protocol.WorkerEvent{Type: protocol.MessageTaskLog}); err == nil {
		t.Fatal("SendWorkerEvent without websocket should fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.send(ctx, protocol.Envelope{}); err == nil {
		t.Fatal("send with canceled context should fail")
	}
}

func TestParseWorkerConfigHelpersAndRunCancellation(t *testing.T) {
	if got := ParseProjectBindingMode("SPECIFIC_PROJECTS"); got != domain.WorkerSpecificProjects {
		t.Fatalf("specific binding mode = %s", got)
	}
	if got := ParseProjectBindingMode("anything-else"); got != domain.WorkerAllProjects {
		t.Fatalf("fallback binding mode = %s", got)
	}
	items := ParseCSV(" project-1, ,project-2 ,, ")
	if len(items) != 2 || items[0] != "project-1" || items[1] != "project-2" {
		t.Fatalf("ParseCSV = %#v", items)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := New(Config{ManagerWSURL: "ws://127.0.0.1:1/worker/ws", WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()}).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run canceled error = %v, want context canceled", err)
	}
}

func TestHeartbeatLoopSendsHeartbeat(t *testing.T) {
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
			t.Errorf("read heartbeat: %v", err)
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
	client := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir(), HeartbeatEvery: 10 * time.Millisecond})
	client.conn = conn
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- client.heartbeatLoop(ctx) }()

	select {
	case envelope := <-received:
		if envelope.Type != protocol.MessageWorkerHeartbeat || envelope.WorkerID != "worker-1" {
			t.Fatalf("heartbeat envelope = %+v", envelope)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for heartbeat")
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("heartbeatLoop did not exit")
	}
}

func TestReadLoopHandlesPingTaskStartAndInterrupt(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	serverConn := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverConn <- conn
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	remote := <-serverConn
	defer remote.Close()

	client := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()})
	client.conn = conn
	client.executor = executor.NewExecutor(executor.Config{
		WorkerID: "worker-1",
		WorkDir:  t.TempDir(),
		Agents: map[domain.AgentType]executor.Agent{
			domain.AgentCodex: executor.AgentFunc(func(ctx context.Context, input executor.AgentInput, emit func(executor.AgentEvent)) error {
				emit(executor.AgentEvent{Type: executor.AgentEventStdout, Content: "hello"})
				emit(executor.AgentEvent{Type: executor.AgentEventConversation, Content: "done"})
				return nil
			}),
		},
		Reporter: executor.ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			return client.SendWorkerEvent(ctx, event)
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- client.readLoop(ctx) }()

	writeRawEnvelope(t, remote, rawEnvelope{MessageID: "ping-1", Type: protocol.MessagePing})
	if envelope := readEnvelope(t, remote); envelope.Type != protocol.MessageWorkerHeartbeat {
		t.Fatalf("ping response = %s, want heartbeat", envelope.Type)
	}
	payload := protocol.TaskStartPayload{
		Task:    protocol.TaskPayload{ID: "task-1", Title: "T", AgentType: domain.AgentCodex},
		Project: protocol.ProjectPayload{ID: "project-1", WorktreeNamePrefix: "p"},
	}
	writeRawEnvelope(t, remote, rawEnvelope{MessageID: "start-1", Type: protocol.MessageTaskStart, TaskID: "task-1", Payload: mustRawJSON(t, payload)})
	seen := map[protocol.MessageType]bool{}
	deadline := time.Now().Add(2 * time.Second)
	for !seen[protocol.MessageTaskAccepted] || !seen[protocol.MessageTaskStarted] || !seen[protocol.MessageTaskLog] || !seen[protocol.MessageTaskConversation] || !seen[protocol.MessageTaskCompleted] {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for task envelopes, seen=%v", seen)
		}
		envelope := readEnvelope(t, remote)
		seen[envelope.Type] = true
	}
	writeRawEnvelope(t, remote, rawEnvelope{MessageID: "interrupt-1", Type: protocol.MessageTaskInterrupt, TaskID: "task-1"})
	writeRawEnvelope(t, remote, rawEnvelope{MessageID: "cancel-1", Type: protocol.MessageTaskCancel, TaskID: "task-1"})
	cancel()
	_ = remote.Close()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("readLoop did not exit")
	}
}

func writeRawEnvelope(t *testing.T, conn *websocket.Conn, envelope rawEnvelope) {
	t.Helper()
	if err := conn.WriteJSON(envelope); err != nil {
		t.Fatal(err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) protocol.Envelope {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var envelope protocol.Envelope
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
