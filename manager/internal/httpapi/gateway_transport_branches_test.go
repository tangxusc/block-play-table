package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

// TestA2ATunnelRoundTripperRejectsOutOfScopeRequests 验证 FRP RoundTripper 不允许 SDK 请求越过声明的 A2A 目标。
func TestA2ATunnelRoundTripperRejectsOutOfScopeRequests(t *testing.T) {
	transport := &a2aTunnelRoundTripper{
		host: "127.0.0.1", port: 39123,
		cardPath: "/.well-known/agent-card.json", endpointPath: "/a2a",
	}
	requests := []*http.Request{
		nil,
		{},
		{URL: &url.URL{Scheme: "https", Host: "127.0.0.1:39123", Path: "/a2a"}},
		{URL: &url.URL{Scheme: "http", Host: "localhost:39123", Path: "/a2a"}},
		{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:39123", Path: "/outside"}},
		{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:not-a-port", Path: "/a2a"}},
		{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:39124", Path: "/a2a"}},
	}
	for index, request := range requests {
		if _, err := transport.RoundTrip(request); !errors.Is(err, app.ErrA2AProtocolConflict) {
			t.Fatalf("request %d err=%v, want protocol conflict", index, err)
		}
	}
}

// TestWorkerGatewayResolveA2ATargetValidationBranches 验证 Worker A2A 能力和 FRP 隧道必须同时可信。
func TestWorkerGatewayResolveA2ATargetValidationBranches(t *testing.T) {
	ctx := context.Background()
	storage := store.NewMemoryStore()
	service := app.NewService(storage)
	gateway := NewWorkerGateway(service, nil, "token")
	if _, err := gateway.resolveWorkerA2ATarget(ctx, "missing"); !errors.Is(err, app.ErrA2AProjection) {
		t.Fatalf("missing worker err=%v", err)
	}

	validCapabilities := map[string]string{
		"a2a_host":          "127.0.0.1",
		"a2a_port":          "39123",
		"a2a_version":       a2aext.Version,
		"a2a_transport":     string(a2a.TransportProtocolJSONRPC),
		"a2a_extension":     a2aext.ExtensionURI,
		"a2a_card_path":     "/.well-known/agent-card.json",
		"a2a_endpoint_path": "/a2a",
	}
	saveWorker := func(id string, capabilities map[string]string) {
		t.Helper()
		if err := storage.SaveWorker(ctx, &domain.Worker{ID: id, Name: id, Capabilities: capabilities}); err != nil {
			t.Fatal(err)
		}
	}
	clone := func() map[string]string {
		copy := make(map[string]string, len(validCapabilities))
		for key, value := range validCapabilities {
			copy[key] = value
		}
		return copy
	}

	badHost := clone()
	badHost["a2a_host"] = "0.0.0.0"
	saveWorker("bad-host", badHost)
	if _, err := gateway.resolveWorkerA2ATarget(ctx, "bad-host"); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("bad host err=%v", err)
	}
	ipv6 := clone()
	ipv6["a2a_host"] = "::1"
	saveWorker("ipv6", ipv6)
	if _, err := gateway.resolveWorkerA2ATarget(ctx, "ipv6"); err == nil || errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("ipv6 without tunnel err=%v", err)
	}
	badPort := clone()
	badPort["a2a_port"] = "invalid"
	saveWorker("bad-port", badPort)
	if _, err := gateway.resolveWorkerA2ATarget(ctx, "bad-port"); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("bad port err=%v", err)
	}
	badCapabilities := clone()
	badCapabilities["a2a_version"] = "0"
	saveWorker("bad-capabilities", badCapabilities)
	if _, err := gateway.resolveWorkerA2ATarget(ctx, "bad-capabilities"); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("bad capabilities err=%v", err)
	}
	saveWorker("missing-tunnel", clone())
	if _, err := gateway.resolveWorkerA2ATarget(ctx, "missing-tunnel"); err == nil || errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("missing tunnel err=%v", err)
	}
	if err := gateway.trackProxyTunnel(&workerProxyTunnel{workerID: "other", workerName: "missing-tunnel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.resolveWorkerA2ATarget(ctx, "missing-tunnel"); err == nil {
		t.Fatal("foreign worker tunnel should fail")
	}
}

// TestWorkerGatewayTrackingBranches 验证连接和 FRP 隧道替换只影响当前注册实例。
func TestWorkerGatewayTrackingBranches(t *testing.T) {
	gateway := NewWorkerGateway(nil, nil, "")
	firstConnection := &workerConnection{}
	secondConnection := &workerConnection{}
	gateway.trackConnection("", firstConnection)
	if gateway.untrackConnection("", firstConnection) {
		t.Fatal("empty worker connection should not be tracked")
	}
	gateway.trackConnection("worker", firstConnection)
	if gateway.untrackConnection("worker", secondConnection) {
		t.Fatal("non-current connection should not be removed")
	}
	if !gateway.untrackConnection("worker", firstConnection) {
		t.Fatal("current connection should be removed")
	}

	if err := gateway.trackProxyTunnel(nil); err == nil {
		t.Fatal("nil tunnel should fail")
	}
	if err := gateway.trackProxyTunnel(&workerProxyTunnel{}); err == nil {
		t.Fatal("unnamed tunnel should fail")
	}
	first := &workerProxyTunnel{workerID: "worker", workerName: "runner"}
	if err := gateway.trackProxyTunnel(first); err != nil {
		t.Fatal(err)
	}
	if got := gateway.proxyTunnel("runner"); got != first {
		t.Fatalf("tracked tunnel=%p want=%p", got, first)
	}
	if got := gateway.proxyTunnelByWorkerID("worker"); got != first {
		t.Fatalf("worker id tunnel=%p want=%p", got, first)
	}
	if err := gateway.ensureProxyTunnelNameAvailable("runner", "other"); err == nil {
		t.Fatal("foreign worker should not reuse tunnel name")
	}
	if err := gateway.ensureProxyTunnelNameAvailable("runner", "worker"); err != nil {
		t.Fatal(err)
	}
	if err := gateway.trackProxyTunnel(&workerProxyTunnel{workerID: "other", workerName: "runner"}); err == nil {
		t.Fatal("foreign tunnel replacement should fail")
	}
	second := &workerProxyTunnel{workerID: "worker", workerName: "runner"}
	if err := gateway.trackProxyTunnel(second); err != nil {
		t.Fatal(err)
	}
	gateway.untrackProxyTunnel("", second)
	gateway.untrackProxyTunnel("runner", first)
	if gateway.proxyTunnel("runner") != second {
		t.Fatal("stale tunnel untrack removed current tunnel")
	}
	renamed := &workerProxyTunnel{workerID: "worker", workerName: "renamed-runner"}
	if err := gateway.trackProxyTunnel(renamed); err != nil {
		t.Fatal(err)
	}
	if gateway.proxyTunnel("runner") != nil || gateway.proxyTunnelByWorkerID("worker") != renamed {
		t.Fatal("Worker 在线改名后 ID 索引未切换到新隧道")
	}
	gateway.untrackProxyTunnel("runner", second)
	gateway.untrackProxyTunnel("renamed-runner", renamed)
	if gateway.proxyTunnel("renamed-runner") != nil || gateway.proxyTunnelByWorkerID("worker") != nil {
		t.Fatal("current tunnel remains tracked")
	}
}
