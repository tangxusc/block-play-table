package client

import (
	"context"
	"errors"
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

func TestRunRequiresManagerURL(t *testing.T) {
	err := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()}).Run(context.Background())
	if err == nil {
		t.Fatal("Run without manager url should fail")
	}
}

func TestSendSerializesConcurrentWrites(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	const total = 32
	received := make(chan protocol.Envelope, total)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		for {
			var envelope protocol.Envelope
			if err := connection.ReadJSON(&envelope); err != nil {
				return
			}
			received <- envelope
		}
	}))
	defer server.Close()

	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	worker := New(Config{WorkerID: "worker-1", Name: "W", WorkDir: t.TempDir()})
	worker.conn = connection

	start := make(chan struct{})
	errorsByWrite := make(chan error, total)
	var wait sync.WaitGroup
	for index := 0; index < total; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errorsByWrite <- worker.send(context.Background(), protocol.Envelope{
				MessageID: "heartbeat", Type: protocol.MessageWorkerHeartbeat, WorkerID: "worker-1", Timestamp: time.Now().UTC(),
			})
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByWrite)
	for writeErr := range errorsByWrite {
		if writeErr != nil {
			t.Fatalf("send returned error: %v", writeErr)
		}
	}
	for index := 0; index < total; index++ {
		select {
		case envelope := <-received:
			if envelope.Type != protocol.MessageWorkerHeartbeat || envelope.WorkerID != "worker-1" {
				t.Fatalf("envelope = %+v", envelope)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for envelope %d/%d", index+1, total)
		}
	}
}

func TestConnectAndServeSendsRegistration(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	registered := make(chan protocol.Envelope, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/worker/ws", func(response http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("token"); got != "secret" {
			t.Errorf("token query = %q, want secret", got)
		}
		connection, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		var envelope protocol.Envelope
		if err := connection.ReadJSON(&envelope); err != nil {
			t.Errorf("read register: %v", err)
			return
		}
		registered <- envelope
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := New(Config{
		ManagerWSURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/worker/ws", WorkerID: "worker-1",
		WorkerToken: "secret", Name: "W", WorkDir: t.TempDir(), SupportedAgents: []domain.AgentType{domain.AgentCodex},
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

func TestRunHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := New(Config{ManagerWSURL: "ws://127.0.0.1:1/worker/ws", WorkerID: "worker-1", WorkDir: t.TempDir()}).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run canceled error = %v, want context canceled", err)
	}
}
