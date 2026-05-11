package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/frp"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestManagerTokenGatewayAllowsCompatibilityWhenWorkerTokenEmpty(t *testing.T) {
	server := httptest.NewServer(NewServer(app.NewService(store.NewMemoryStore())).Handler())
	defer server.Close()

	status := getAuthStatus(t, server.URL)
	if status.Required {
		t.Fatalf("auth status required = true, want false")
	}

	res := postRawGraphQL(t, server.URL, `query { settings { id } }`, nil)
	if res["errors"] != nil {
		t.Fatalf("graphql without token errors = %#v", res["errors"])
	}
}

func TestManagerTokenGatewayProtectsGraphQLAndAuthEndpoints(t *testing.T) {
	server := httptest.NewServer(NewServer(app.NewService(store.NewMemoryStore()), WithWorkerToken("secret")).Handler())
	defer server.Close()

	status := getAuthStatus(t, server.URL)
	if !status.Required {
		t.Fatalf("auth status required = false, want true")
	}

	badVerify := postAuthVerify(t, server.URL, "wrong")
	if badVerify.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad verify status = %d, want 401", badVerify.StatusCode)
	}
	goodVerify := postAuthVerify(t, server.URL, "secret")
	if goodVerify.StatusCode != http.StatusOK {
		t.Fatalf("good verify status = %d, want 200", goodVerify.StatusCode)
	}

	unauthorized := postRawGraphQLResponse(t, server.URL, `query { settings { id } }`, nil, nil)
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("graphql without token status = %d, want 401", unauthorized.StatusCode)
	}
	authorizedByBearer := postRawGraphQLResponse(t, server.URL, `query { settings { id } }`, nil, map[string]string{
		"Authorization": "Bearer secret",
	})
	if authorizedByBearer.StatusCode != http.StatusOK || authorizedByBearer.Body["errors"] != nil {
		t.Fatalf("graphql bearer response = %d %#v", authorizedByBearer.StatusCode, authorizedByBearer.Body)
	}
	authorizedByHeader := postRawGraphQLResponse(t, server.URL, `query { settings { id } }`, nil, map[string]string{
		"X-Manager-Token": "secret",
	})
	if authorizedByHeader.StatusCode != http.StatusOK || authorizedByHeader.Body["errors"] != nil {
		t.Fatalf("graphql header response = %d %#v", authorizedByHeader.StatusCode, authorizedByHeader.Body)
	}
}

func TestManagerTokenGatewayProtectsRealtimeTerminalAndProxyEntrypoints(t *testing.T) {
	server := httptest.NewServer(NewServer(app.NewService(store.NewMemoryStore()), WithWorkerToken("secret")).Handler())
	defer server.Close()

	dialer := websocket.Dialer{Subprotocols: []string{"graphql-transport-ws"}}
	_, res, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/subscriptions", nil)
	if err == nil {
		t.Fatal("subscription without token connected unexpectedly")
	}
	if res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("subscription without token status = %v, err = %v", responseStatus(res), err)
	}
	conn, res, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/subscriptions?token=secret", nil)
	if err != nil {
		t.Fatalf("subscription with token failed: %v (status=%v)", err, responseStatus(res))
	}
	_ = conn.Close()

	terminal, err := http.Get(server.URL + "/terminal/tasks/task-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = terminal.Body.Close()
	if terminal.StatusCode != http.StatusUnauthorized {
		t.Fatalf("terminal without token status = %d, want 401", terminal.StatusCode)
	}

	proxy, err := http.Get(server.URL + "/proxy/web/worker/localhost/80")
	if err != nil {
		t.Fatal(err)
	}
	_ = proxy.Body.Close()
	if proxy.StatusCode != http.StatusUnauthorized {
		t.Fatalf("proxy without token status = %d, want 401", proxy.StatusCode)
	}
}

func TestManagerTokenQueryIsNotForwardedToWorkerProxy(t *testing.T) {
	service := app.NewService(store.NewMemoryStore())
	manager := httptest.NewServer(NewServer(service, WithWorkerToken("secret")).Handler())
	defer manager.Close()

	workerHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("token"); got != "" {
			http.Error(w, "manager token was forwarded", http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("tab"); got != "preview" {
			http.Error(w, "tab query missing", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer workerHTTP.Close()
	portText := strings.TrimPrefix(workerHTTP.URL, "http://127.0.0.1:")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	connectFRPTunnelWithToken(t, manager.URL, "worker-1", "Proxy Worker", "secret")

	res, err := http.Get(manager.URL + "/proxy/web/Proxy%20Worker/localhost/" + strconv.Itoa(port) + "/dashboard?tab=preview&token=secret")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("proxy response status = %d, want 200", res.StatusCode)
	}
}

func TestManagerTokenGatewayLeavesHealthReadyOptionsAndWorkerSocketsOnExistingRules(t *testing.T) {
	server := httptest.NewServer(NewServer(app.NewService(store.NewMemoryStore()), WithWorkerToken("secret")).Handler())
	defer server.Close()

	for _, path := range []string{"/healthz", "/readyz"} {
		res, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, res.StatusCode)
		}
	}

	req, err := http.NewRequest(http.MethodOptions, server.URL+"/graphql", nil)
	if err != nil {
		t.Fatal(err)
	}
	options, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = options.Body.Close()
	if options.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS /graphql status = %d, want 204", options.StatusCode)
	}

	_, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/worker/ws?worker_id=worker-token", nil)
	if err == nil {
		t.Fatal("worker websocket without worker token connected unexpectedly")
	}
	if res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("worker websocket without token status = %v, err = %v", responseStatus(res), err)
	}
}

func connectFRPTunnelWithToken(t *testing.T, managerURL, workerID, workerName, token string) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(managerURL, "http") + "/worker/frp?worker_id=" + workerID + "&worker_name=" + strings.ReplaceAll(workerName, " ", "%20") + "&token=" + token
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

type authStatusResponse struct {
	Required bool `json:"required"`
}

func getAuthStatus(t *testing.T, baseURL string) authStatusResponse {
	t.Helper()
	res, err := http.Get(baseURL + "/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("auth status HTTP = %d, want 200", res.StatusCode)
	}
	var decoded authStatusResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func postAuthVerify(t *testing.T, baseURL, token string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/auth/verify", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

type rawGraphQLHTTPResponse struct {
	StatusCode int
	Body       map[string]any
}

func postRawGraphQLResponse(t *testing.T, baseURL, query string, variables map[string]any, headers map[string]string) rawGraphQLHTTPResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return rawGraphQLHTTPResponse{StatusCode: res.StatusCode, Body: decoded}
}
