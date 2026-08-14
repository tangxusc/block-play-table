package httpapi

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/hashicorp/yamux"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/frp"
)

// TestA2ATunnelRoundTripperControlsAuthorizationAcrossFRP 验证 A2A 鉴权头由 Manager 覆盖并完整穿过 FRP。
func TestA2ATunnelRoundTripperControlsAuthorizationAcrossFRP(t *testing.T) {
	type receivedHeaders struct {
		authorization []string
		extension     []string
	}
	received := make(chan receivedHeaders, 2)
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received <- receivedHeaders{
			authorization: append([]string(nil), request.Header.Values("Authorization")...),
			extension:     append([]string(nil), request.Header.Values(a2a.SvcParamExtensions)...),
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{}`)
	}))
	t.Cleanup(target.Close)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	managerConn, workerConn := net.Pipe()
	managerSession, err := yamux.Client(managerConn, nil)
	if err != nil {
		t.Fatalf("创建 Manager yamux: %v", err)
	}
	t.Cleanup(func() { _ = managerSession.Close() })
	workerSession, err := yamux.Server(workerConn, nil)
	if err != nil {
		t.Fatalf("创建 Worker yamux: %v", err)
	}
	t.Cleanup(func() { _ = workerSession.Close() })
	proxyCtx, stopProxy := context.WithCancel(context.Background())
	t.Cleanup(stopProxy)
	go func() {
		_ = frp.ServeWorkerProxy(proxyCtx, workerSession)
	}()
	tunnel := &workerProxyTunnel{session: managerSession}

	tests := []struct {
		name              string
		token             string
		wantAuthorization []string
	}{
		{name: "token mode", token: "worker-token", wantAuthorization: []string{"Bearer worker-token"}},
		{name: "trusted local mode", wantAuthorization: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &a2aTunnelRoundTripper{
				tunnel: tunnel, host: host, port: port, cardPath: "/.well-known/agent-card.json",
				endpointPath: "/a2a", token: test.token,
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target.URL+"/a2a", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Add("Authorization", "Bearer untrusted-upstream")
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatalf("FRP A2A 往返: %v", err)
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("读取响应: read=%v close=%v", readErr, closeErr)
			}
			got := <-received
			if strings.Join(got.authorization, "\x00") != strings.Join(test.wantAuthorization, "\x00") {
				t.Fatalf("Authorization = %q，期望 %q", got.authorization, test.wantAuthorization)
			}
			if len(got.extension) != 1 || got.extension[0] != a2aext.ExtensionURI {
				t.Fatalf("A2A-Extensions = %q", got.extension)
			}
		})
	}
}
