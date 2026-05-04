package frp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
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

func TestWebSocketUpgradeProxyRoundTripsThroughWorkerTunnel(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade target: %v", err)
			return
		}
		defer conn.Close()
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read target websocket message: %v", err)
			return
		}
		if messageType != websocket.TextMessage || string(message) != "ping" {
			t.Errorf("target websocket message = type %d body %q, want text ping", messageType, message)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte("pong")); err != nil {
			t.Errorf("write target websocket message: %v", err)
		}
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

	client, manager := net.Pipe()
	defer client.Close()
	defer manager.Close()
	req := httptest.NewRequest(http.MethodGet, "http://manager.local/terminal/tasks/task-1/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", websocketKey(t))

	done := make(chan error, 1)
	go func() {
		done <- ProxyUpgrade(ctx, managerSession, req, ProxyTarget{Port: port, Path: "/"}, manager, bufio.NewReader(manager))
	}()

	reader := bufio.NewReader(client)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		t.Fatalf("read websocket upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("websocket upgrade status = %d, want 101", resp.StatusCode)
	}
	if err := writeMaskedTextFrame(client, "ping"); err != nil {
		t.Fatalf("write websocket frame: %v", err)
	}
	message, err := readTextFrame(reader)
	if err != nil {
		t.Fatalf("read websocket frame: %v", err)
	}
	if message != "pong" {
		t.Fatalf("websocket response = %q, want pong", message)
	}
	_ = client.Close()

	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("proxy upgrade returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upgrade proxy to close")
	}
}

func websocketKey(t *testing.T) string {
	t.Helper()
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key[:])
}

func writeMaskedTextFrame(w io.Writer, message string) error {
	payload := []byte(message)
	header := []byte{0x81, 0x80 | byte(len(payload))}
	mask := []byte{0x11, 0x22, 0x33, 0x44}
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(mask); err != nil {
		return err
	}
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%len(mask)]
	}
	_, err := w.Write(masked)
	return err
}

func readTextFrame(r *bufio.Reader) (string, error) {
	first, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	if first != 0x81 {
		return "", io.ErrUnexpectedEOF
	}
	length, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	payload := make([]byte, int(length&0x7f))
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", err
	}
	return string(payload), nil
}
