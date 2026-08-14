package frp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

const ProxyPortHeader = "X-Block-Play-Proxy-Port"
const ProxyHostHeader = "X-Block-Play-Proxy-Host"

type ProxyTarget struct {
	Host string
	Port int
	Path string
}

// ProxyHTTP 通过 Worker yamux 会话转发一次 HTTP 请求。
// 参数：ctx 控制请求写入、响应头等待及响应体生命周期；session 是活动隧道；req 和 target 描述原请求与受限目标。
// 返回：已绑定隧道流生命周期的 HTTP 响应。
// 错误：context 取消、目标非法、隧道断开、请求写入或响应解析失败时返回错误。
func ProxyHTTP(ctx context.Context, session *yamux.Session, req *http.Request, target ProxyTarget) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("worker proxy tunnel is not connected")
	}
	out, err := proxyRequest(ctx, req, target, true)
	if err != nil {
		return nil, err
	}
	stream, err := session.OpenStream()
	if err != nil {
		return nil, err
	}
	streamLifecycle := newContextStreamCloser(ctx, stream)
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = streamLifecycle.Close()
		}
	}()

	if err := out.Write(stream); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(stream), out)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	resp.Body = &streamReadCloser{ReadCloser: resp.Body, closer: streamLifecycle}
	closeOnError = false
	return resp, nil
}

func ProxyUpgrade(ctx context.Context, session *yamux.Session, req *http.Request, target ProxyTarget, client net.Conn, clientReader *bufio.Reader) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if session == nil || session.IsClosed() {
		return fmt.Errorf("worker proxy tunnel is not connected")
	}
	if client == nil {
		return fmt.Errorf("client connection is required")
	}
	if clientReader == nil {
		clientReader = bufio.NewReader(client)
	}
	out, err := proxyRequest(ctx, req, target, false)
	if err != nil {
		return err
	}
	stream, err := session.OpenStream()
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := out.Write(stream); err != nil {
		return err
	}
	workerReader := bufio.NewReader(stream)
	resp, err := http.ReadResponse(workerReader, out)
	if err != nil {
		return err
	}
	if err := resp.Write(client); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Body.Close()
		return fmt.Errorf("worker websocket upgrade failed with status %d", resp.StatusCode)
	}

	errCh := make(chan error, 2)
	go copyRaw(errCh, stream, clientReader)
	go copyRaw(errCh, client, workerReader)
	err = <-errCh
	_ = stream.Close()
	_ = client.Close()
	if proxyPipeClosed(err) {
		return nil
	}
	return err
}

func proxyRequest(ctx context.Context, req *http.Request, target ProxyTarget, removeHopByHop bool) (*http.Request, error) {
	if target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("worker port %d is invalid", target.Port)
	}
	targetHost, err := NormalizeProxyHost(target.Host)
	if err != nil {
		return nil, err
	}
	if target.Path == "" {
		target.Path = "/"
	}
	targetPath := target.Path
	targetQuery := ""
	if path, query, ok := strings.Cut(target.Path, "?"); ok {
		targetPath = path
		targetQuery = query
	}
	out := req.Clone(ctx)
	out.RequestURI = ""
	out.URL.Path = targetPath
	if targetQuery != "" {
		out.URL.RawQuery = targetQuery
	}
	out.URL.RawPath = ""
	out.URL.Scheme = ""
	out.URL.Host = ""
	out.Host = net.JoinHostPort(targetHost, strconv.Itoa(target.Port))
	out.Header.Del("worker")
	out.Header.Del("worker_host")
	out.Header.Del("worker_port")
	out.Header.Set(ProxyPortHeader, strconv.Itoa(target.Port))
	out.Header.Set(ProxyHostHeader, targetHost)
	if removeHopByHop {
		RemoveHopByHopHeaders(out.Header)
	}
	return out, nil
}

func copyRaw(errCh chan<- error, dst io.Writer, src io.Reader) {
	_, err := io.Copy(dst, src)
	errCh <- err
}

func proxyPipeClosed(err error) bool {
	if err == nil {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "closed") || strings.Contains(text, "broken pipe") || strings.Contains(text, "reset by peer")
}

func NormalizeProxyHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "127.0.0.1", nil
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	if strings.ContainsAny(host, "/\\") {
		return "", fmt.Errorf("worker proxy host %q is invalid", host)
	}
	for _, r := range host {
		if r <= 31 || r == 127 || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return "", fmt.Errorf("worker proxy host %q is invalid", host)
		}
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", fmt.Errorf("worker proxy host %q is invalid", host)
	}
	return host, nil
}

func RemoveHopByHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, item := range headerValues(value) {
			header.Del(item)
		}
	}
	for _, key := range []string{
		"Connection",
		"Proxy-Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		header.Del(key)
	}
}

func headerValues(value string) []string {
	var out []string
	for _, item := range stringsSplitComma(value) {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func stringsSplitComma(value string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(value); i++ {
		if i == len(value) || value[i] == ',' {
			item := value[start:i]
			for len(item) > 0 && (item[0] == ' ' || item[0] == '\t') {
				item = item[1:]
			}
			for len(item) > 0 && (item[len(item)-1] == ' ' || item[len(item)-1] == '\t') {
				item = item[:len(item)-1]
			}
			out = append(out, item)
			start = i + 1
		}
	}
	return out
}

type streamReadCloser struct {
	io.ReadCloser
	closer interface{ Close() error }
}

func (r *streamReadCloser) Close() error {
	_ = r.ReadCloser.Close()
	return r.closer.Close()
}

type contextStreamCloser struct {
	stream io.Closer
	done   chan struct{}
	once   sync.Once
}

func newContextStreamCloser(ctx context.Context, stream io.Closer) *contextStreamCloser {
	closer := &contextStreamCloser{stream: stream, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-closer.done:
		}
	}()
	return closer
}

// Close 幂等终止 context 监视并关闭底层隧道流。
// 参数：无。
// 返回：首次关闭底层流的结果；重复关闭返回 nil。
// 错误：底层流关闭失败时返回对应错误。
func (c *contextStreamCloser) Close() error {
	var closeErr error
	c.once.Do(func() {
		close(c.done)
		if stream, ok := c.stream.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = stream.SetReadDeadline(time.Now())
		}
		closeErr = c.stream.Close()
	})
	return closeErr
}
