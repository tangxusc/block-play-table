package frp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

func TestProxyValidationAndRequestMappingBranches(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://manager/path", nil)
	if _, err := ProxyHTTP(nil, nil, request, ProxyTarget{}); err == nil {
		t.Fatal("nil session 应失败")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ProxyHTTP(canceled, nil, request, ProxyTarget{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消 ProxyHTTP=%v", err)
	}
	if err := ProxyUpgrade(nil, nil, request, ProxyTarget{}, nil, nil); err == nil {
		t.Fatal("nil upgrade session 应失败")
	}
	if err := ServeWorkerProxy(nil, nil); err == nil {
		t.Fatal("nil yamux session 应失败")
	}

	for _, port := range []int{0, 65536} {
		if _, err := proxyRequest(context.Background(), request, ProxyTarget{Port: port}, true); err == nil {
			t.Fatalf("非法端口 %d 未被拒绝", port)
		}
	}
	if _, err := proxyRequest(context.Background(), request, ProxyTarget{Host: "bad/host", Port: 80}, true); err == nil {
		t.Fatal("非法 host 未被拒绝")
	}
	request.Header.Set("Connection", "keep-alive, X-Custom")
	request.Header.Set("X-Custom", "remove")
	request.Header.Set("worker", "route")
	out, err := proxyRequest(context.Background(), request, ProxyTarget{Host: "[::1]", Port: 8080, Path: "/a2a?stream=true"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.URL.Path != "/a2a" || out.URL.RawQuery != "stream=true" || out.Host != "[::1]:8080" || out.Header.Get("X-Custom") != "" || out.Header.Get("worker") != "" {
		t.Fatalf("代理请求映射错误: url=%s host=%s headers=%v", out.URL, out.Host, out.Header)
	}
	defaultPath, err := proxyRequest(context.Background(), httptest.NewRequest(http.MethodGet, "http://manager/path", nil), ProxyTarget{Port: 80}, false)
	if err != nil || defaultPath.URL.Path != "/" {
		t.Fatalf("默认代理路径=%q err=%v", defaultPath.URL.Path, err)
	}
}

func TestProxyHelperClosedHostAndHeaderBranches(t *testing.T) {
	for _, testCase := range []struct {
		host    string
		want    string
		wantErr bool
	}{
		{"", "127.0.0.1", false},
		{" [::1] ", "::1", false},
		{"host\\name", "", true},
		{"bad host", "", true},
		{"bad\x7fhost", "", true},
		{"not:ipv6", "", true},
	} {
		got, err := NormalizeProxyHost(testCase.host)
		if (err != nil) != testCase.wantErr || got != testCase.want {
			t.Fatalf("NormalizeProxyHost(%q)=%q,%v", testCase.host, got, err)
		}
	}
	for _, err := range []error{nil, errors.New("closed connection"), errors.New("broken pipe"), errors.New("reset by peer")} {
		if !proxyPipeClosed(err) {
			t.Fatalf("应识别已关闭管道: %v", err)
		}
	}
	if proxyPipeClosed(errors.New("permission denied")) {
		t.Fatal("普通错误不应识别为关闭")
	}
	if !headerHasToken("keep-alive, Upgrade", "upgrade") || headerHasToken("keep-alive", "upgrade") {
		t.Fatal("Connection token 匹配错误")
	}
	if got := headerValues(" , keep-alive,\tupgrade , "); len(got) != 2 || got[0] != "keep-alive" || got[1] != "upgrade" {
		t.Fatalf("headerValues=%q", got)
	}
}

func TestContextAndStreamClosersCoverIdempotentPaths(t *testing.T) {
	stream := &recordingCloser{}
	closer := newContextStreamCloser(context.Background(), stream)
	if err := closer.Close(); err != nil || stream.closes != 1 {
		t.Fatalf("首次关闭 err=%v closes=%d", err, stream.closes)
	}
	if err := closer.Close(); err != nil || stream.closes != 1 {
		t.Fatalf("重复关闭 err=%v closes=%d", err, stream.closes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	deadlineStream := &deadlineRecordingCloser{}
	_ = newContextStreamCloser(ctx, deadlineStream)
	cancel()
	deadline := time.Now().Add(time.Second)
	for deadlineStream.closes == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if deadlineStream.closes != 1 || deadlineStream.deadlines != 1 {
		t.Fatalf("context 关闭未设置 deadline: %+v", deadlineStream)
	}

	read := &recordingReadCloser{Reader: strings.NewReader("data")}
	outer := &recordingCloser{}
	streamBody := &streamReadCloser{ReadCloser: read, closer: outer}
	if err := streamBody.Close(); err != nil || read.closes != 1 || outer.closes != 1 {
		t.Fatalf("响应体关闭错误: read=%d outer=%d err=%v", read.closes, outer.closes, err)
	}
}

func TestProxyUpgradeRejectsClientAndNonSwitchingResponse(t *testing.T) {
	managerSession, workerSession, closeSessions := pipeSessions(t)
	defer closeSessions()
	request := httptest.NewRequest(http.MethodGet, "http://manager/terminal", nil)
	if err := ProxyUpgrade(context.Background(), managerSession, request, ProxyTarget{Port: 80}, nil, nil); err == nil {
		t.Fatal("nil client connection 应失败")
	}

	go func() {
		stream, err := workerSession.AcceptStream()
		if err != nil {
			return
		}
		defer stream.Close()
		req, err := http.ReadRequest(bufio.NewReader(stream))
		if err != nil {
			return
		}
		_ = req.Body.Close()
		_, _ = io.WriteString(stream, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
	}()
	client, manager := net.Pipe()
	defer client.Close()
	defer manager.Close()
	done := make(chan error, 1)
	go func() {
		done <- ProxyUpgrade(nil, managerSession, request, ProxyTarget{Port: 80}, manager, nil)
	}()
	response, err := http.ReadResponse(bufio.NewReader(client), request)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("upgrade response=%+v err=%v", response, err)
	}
	_ = response.Body.Close()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("非 101 upgrade err=%v", err)
	}
}

func TestWorkerProxyReturnsBadRequestAndBadGateway(t *testing.T) {
	tests := []struct {
		name      string
		request   string
		transport http.RoundTripper
		status    int
	}{
		{"malformed", "not-http\r\n\r\n", roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil }), http.StatusBadRequest},
		{"port", "GET / HTTP/1.1\r\nHost: x\r\n" + ProxyPortHeader + ": invalid\r\n\r\n", roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil }), http.StatusBadRequest},
		{"host", "GET / HTTP/1.1\r\nHost: x\r\n" + ProxyPortHeader + ": 80\r\n" + ProxyHostHeader + ": bad/host\r\n\r\n", roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil }), http.StatusBadRequest},
		{"transport", "GET / HTTP/1.1\r\nHost: x\r\n" + ProxyPortHeader + ": 80\r\n\r\n", roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("dial failed") }), http.StatusBadGateway},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			managerSession, workerSession, closeSessions := pipeSessions(t)
			defer closeSessions()
			go func() {
				stream, err := workerSession.AcceptStream()
				if err != nil {
					return
				}
				serveWorkerProxyStream(context.Background(), stream, testCase.transport)
			}()
			stream, err := managerSession.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if _, err := io.WriteString(stream, testCase.request); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(stream), nil)
			if err != nil || response.StatusCode != testCase.status {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			_ = response.Body.Close()
		})
	}
}

func TestCopyRawPropagatesWriterFailure(t *testing.T) {
	errCh := make(chan error, 1)
	copyRaw(errCh, errorWriter{}, bytes.NewBufferString("data"))
	if err := <-errCh; !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("copyRaw error=%v", err)
	}
}

type recordingCloser struct{ closes int }

func (c *recordingCloser) Close() error { c.closes++; return nil }

type deadlineRecordingCloser struct {
	recordingCloser
	deadlines int
}

func (c *deadlineRecordingCloser) SetReadDeadline(time.Time) error { c.deadlines++; return nil }

type recordingReadCloser struct {
	io.Reader
	closes int
}

func (c *recordingReadCloser) Close() error { c.closes++; return nil }

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func pipeSessions(t *testing.T) (*yamux.Session, *yamux.Session, func()) {
	t.Helper()
	left, right := net.Pipe()
	manager, err := yamux.Client(left, nil)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := yamux.Server(right, nil)
	if err != nil {
		_ = manager.Close()
		t.Fatal(err)
	}
	return manager, worker, func() {
		_ = manager.Close()
		_ = worker.Close()
	}
}
