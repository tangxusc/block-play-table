package httpapi

import (
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
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/frp"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestProxyRoutesByWorkerNameThroughFRPTunnel(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	received := make(chan *http.Request, 1)
	bodyCh := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read target body: %v", err)
		}
		bodyCh <- string(body)
		received <- r
		w.Header().Set("X-Proxy-Target", "hit")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("from worker"))
	}))
	defer target.Close()
	port := mustPort(t, target.URL)

	wsURL := "ws" + strings.TrimPrefix(manager.URL, "http") + "/worker/frp?worker_id=worker-1&worker_name=Proxy%20Worker"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial frp: %v", err)
	}
	defer ws.Close()
	session, err := yamux.Client(frp.NewWebSocketConn(ws), nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer session.Close()
	go func() {
		if err := frp.ServeWorkerProxy(nil, session); err != nil && err != yamux.ErrSessionShutdown {
			t.Errorf("worker proxy: %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, manager.URL+"/proxy/echo?x=1", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("worker", "Proxy Worker")
	req.Header.Set("worker_port", strconv.Itoa(port))
	req.Header.Set("X-Test", "yes")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusAccepted || res.Header.Get("X-Proxy-Target") != "hit" || string(body) != "from worker" {
		t.Fatalf("proxy response = status %d header %q body %q", res.StatusCode, res.Header.Get("X-Proxy-Target"), body)
	}

	select {
	case proxied := <-received:
		if proxied.URL.Path != "/echo" || proxied.URL.RawQuery != "x=1" {
			t.Fatalf("proxied URL = %s?%s", proxied.URL.Path, proxied.URL.RawQuery)
		}
		if proxied.Header.Get("X-Test") != "yes" {
			t.Fatalf("proxied X-Test = %q", proxied.Header.Get("X-Test"))
		}
		if proxied.Header.Get("worker") != "" || proxied.Header.Get("worker_port") != "" {
			t.Fatalf("routing headers leaked: %+v", proxied.Header)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for proxied request")
	}
	if got := <-bodyCh; got != "hello" {
		t.Fatalf("proxied body = %q", got)
	}
}

func TestProxyWebRouteTargetsWorkerNetworkHostThroughFRPTunnel(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	received := make(chan *http.Request, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>worker web</html>"))
	}))
	defer target.Close()
	port := mustPort(t, target.URL)

	wsURL := "ws" + strings.TrimPrefix(manager.URL, "http") + "/worker/frp?worker_id=worker-1&worker_name=Proxy%20Worker"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial frp: %v", err)
	}
	defer ws.Close()
	session, err := yamux.Client(frp.NewWebSocketConn(ws), nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer session.Close()
	go func() {
		if err := frp.ServeWorkerProxy(nil, session); err != nil && err != yamux.ErrSessionShutdown {
			t.Errorf("worker proxy: %v", err)
		}
	}()

	res, err := http.Get(manager.URL + "/proxy/web/Proxy%20Worker/localhost/" + strconv.Itoa(port) + "/dashboard?tab=preview")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(body) != "<html>worker web</html>" {
		t.Fatalf("proxy response = status %d body %q", res.StatusCode, body)
	}

	select {
	case proxied := <-received:
		if proxied.URL.Path != "/dashboard" || proxied.URL.RawQuery != "tab=preview" {
			t.Fatalf("proxied URL = %s?%s", proxied.URL.Path, proxied.URL.RawQuery)
		}
		if proxied.Host != "localhost:"+strconv.Itoa(port) {
			t.Fatalf("proxied host = %q, want localhost:%d", proxied.Host, port)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for proxied request")
	}
}

func TestProxyRejectsMissingHeadersInvalidPortAndMissingTunnel(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	manager := httptest.NewServer(NewServer(service).Handler())
	defer manager.Close()

	res, err := http.Get(manager.URL + "/proxy/missing")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing headers status = %d, want 400", res.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, manager.URL+"/proxy/bad-port", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("worker", "Proxy Worker")
	req.Header.Set("worker_port", "70000")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid port status = %d, want 400", res.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, manager.URL+"/proxy/no-tunnel", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("worker", "Missing Worker")
	req.Header.Set("worker_port", "8080")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing tunnel status = %d, want 503", res.StatusCode)
	}
}

func mustPort(t *testing.T, rawURL string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
