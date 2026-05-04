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

	"github.com/hashicorp/yamux"
)

const ProxyPortHeader = "X-Block-Play-Proxy-Port"
const ProxyHostHeader = "X-Block-Play-Proxy-Host"

type ProxyTarget struct {
	Host string
	Port int
	Path string
}

func ProxyHTTP(ctx context.Context, session *yamux.Session, req *http.Request, target ProxyTarget) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
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
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = stream.Close()
		}
	}()

	if err := out.Write(stream); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(stream), out)
	if err != nil {
		return nil, err
	}
	resp.Body = &streamReadCloser{ReadCloser: resp.Body, closer: stream}
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
