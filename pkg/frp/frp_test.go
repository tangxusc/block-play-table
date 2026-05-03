package frp

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

func TestWebSocketConnCarriesYamuxStreams(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	serverReady := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		session, err := yamux.Server(NewWebSocketConn(conn), nil)
		if err != nil {
			t.Errorf("yamux server: %v", err)
			return
		}
		close(serverReady)
		stream, err := session.Accept()
		if err != nil {
			t.Errorf("accept stream: %v", err)
			return
		}
		defer stream.Close()
		body := make([]byte, 4)
		_, err = io.ReadFull(stream, body)
		if err != nil {
			t.Errorf("read stream: %v", err)
			return
		}
		if string(body) != "ping" {
			t.Errorf("stream body = %q, want ping", body)
		}
		_, _ = stream.Write([]byte("pong"))
	}))
	defer server.Close()

	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	clientSession, err := yamux.Client(NewWebSocketConn(ws), nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer clientSession.Close()
	<-serverReady

	stream, err := clientSession.Open()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	got := make([]byte, 4)
	_, err = io.ReadFull(stream, got)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(got) != "pong" {
		t.Fatalf("stream response = %q, want pong", got)
	}
}

func TestHTTPProxyRoundTripsThroughWorkerTunnel(t *testing.T) {
	received := make(chan *http.Request, 1)
	bodyCh := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read target body: %v", err)
		}
		bodyCh <- string(body)
		received <- r
		w.Header().Set("X-Worker-Target", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer target.Close()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	left, right := net.Pipe()
	managerSession, err := yamux.Client(left, nil)
	if err != nil {
		t.Fatalf("manager yamux: %v", err)
	}
	defer managerSession.Close()
	workerSession, err := yamux.Server(right, nil)
	if err != nil {
		t.Fatalf("worker yamux: %v", err)
	}
	defer workerSession.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := ServeWorkerProxy(ctx, workerSession); err != nil && ctx.Err() == nil {
			t.Errorf("serve worker proxy: %v", err)
		}
	}()

	req := httptest.NewRequest(http.MethodPost, "http://manager.local/proxy/api?x=1", strings.NewReader("hello"))
	req.Header.Set("X-Test", "yes")
	resp, err := ProxyHTTP(ctx, managerSession, req, ProxyTarget{Port: port, Path: "/api"})
	if err != nil {
		t.Fatalf("proxy http: %v", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Worker-Target") != "ok" || string(respBody) != "proxied" {
		t.Fatalf("response = status %d header %q body %q", resp.StatusCode, resp.Header.Get("X-Worker-Target"), respBody)
	}

	select {
	case proxied := <-received:
		if proxied.URL.Path != "/api" || proxied.URL.RawQuery != "x=1" {
			t.Fatalf("proxied URL = %s?%s", proxied.URL.Path, proxied.URL.RawQuery)
		}
		if proxied.Header.Get("X-Test") != "yes" {
			t.Fatalf("proxied header X-Test = %q", proxied.Header.Get("X-Test"))
		}
		if proxied.Header.Get("worker") != "" || proxied.Header.Get("worker_port") != "" {
			t.Fatalf("routing headers leaked to worker target: %+v", proxied.Header)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for target request")
	}
	if body := <-bodyCh; body != "hello" {
		t.Fatalf("target body = %q, want hello", body)
	}
}
