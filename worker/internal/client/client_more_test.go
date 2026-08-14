package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

func TestConfigHelpers(t *testing.T) {
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
}

func TestSendRejectsDisconnectedAndCanceledContext(t *testing.T) {
	worker := New(Config{WorkerID: "worker-1", WorkDir: t.TempDir()})
	if err := worker.send(context.Background(), protocol.Envelope{}); err == nil {
		t.Fatal("send without websocket should fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.send(ctx, protocol.Envelope{}); err == nil {
		t.Fatal("send with canceled context should fail")
	}
}

func TestHeartbeatLoopSendsHeartbeat(t *testing.T) {
	worker, remote, closeConnections := connectedClient(t, 10*time.Millisecond)
	defer closeConnections()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- worker.heartbeatLoop(ctx) }()

	envelope := readEnvelope(t, remote)
	if envelope.Type != protocol.MessageWorkerHeartbeat || envelope.WorkerID != "worker-1" {
		t.Fatalf("heartbeat envelope = %+v", envelope)
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("heartbeatLoop did not exit")
	}
}

func TestReadLoopAnswersPingAndIgnoresUnknownMessage(t *testing.T) {
	worker, remote, closeConnections := connectedClient(t, time.Hour)
	defer closeConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- worker.readLoop(ctx) }()

	if err := remote.WriteJSON(rawEnvelope{MessageID: "unknown", Type: protocol.MessageType("UNSUPPORTED")}); err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteJSON(rawEnvelope{MessageID: "ping", Type: protocol.MessagePing}); err != nil {
		t.Fatal(err)
	}
	if envelope := readEnvelope(t, remote); envelope.Type != protocol.MessageWorkerHeartbeat {
		t.Fatalf("ping response = %s, want heartbeat", envelope.Type)
	}
	_ = remote.Close()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("readLoop did not exit")
	}
}

func TestFRPURLUsesSingleManagerAndWorkerIdentity(t *testing.T) {
	worker := New(Config{ManagerWSURL: "wss://manager.example/base/worker/ws", WorkerID: "worker-1", WorkerToken: "secret", Name: "Worker One"})
	got, err := worker.frpURL(worker.config.ManagerWSURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"wss://manager.example/base/worker/frp", "worker_id=worker-1", "worker_name=Worker+One", "token=secret"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("frpURL = %q, missing %q", got, expected)
		}
	}
}

func connectedClient(t *testing.T, heartbeatEvery time.Duration) (*Client, *websocket.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverConnection := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverConnection <- connection
	}))
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	remote := <-serverConnection
	worker := New(Config{WorkerID: "worker-1", WorkDir: t.TempDir(), HeartbeatEvery: heartbeatEvery})
	worker.conn = connection
	return worker, remote, func() {
		_ = remote.Close()
		_ = connection.Close()
		server.Close()
	}
}

func readEnvelope(t *testing.T, connection *websocket.Conn) protocol.Envelope {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var envelope protocol.Envelope
	if err := connection.ReadJSON(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}
