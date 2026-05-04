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

func ServeWorkerProxy(ctx context.Context, session *yamux.Session) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if session == nil {
		return fmt.Errorf("yamux session is required")
	}
	for {
		stream, err := session.AcceptStreamWithContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go serveWorkerProxyStream(ctx, stream, http.DefaultTransport)
	}
}

func serveWorkerProxyStream(ctx context.Context, stream *yamux.Stream, transport http.RoundTripper) {
	defer stream.Close()
	streamReader := bufio.NewReader(stream)
	req, err := http.ReadRequest(streamReader)
	if err != nil {
		writeStreamError(stream, http.StatusBadRequest, err)
		return
	}
	defer req.Body.Close()
	portText := req.Header.Get(ProxyPortHeader)
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		writeStreamError(stream, http.StatusBadRequest, fmt.Errorf("invalid worker proxy port %q", portText))
		return
	}
	host, err := NormalizeProxyHost(req.Header.Get(ProxyHostHeader))
	if err != nil {
		writeStreamError(stream, http.StatusBadRequest, err)
		return
	}
	req.RequestURI = ""
	req.URL.Scheme = "http"
	req.URL.Host = net.JoinHostPort(host, strconv.Itoa(port))
	req.Host = req.URL.Host
	req.Header.Del(ProxyPortHeader)
	req.Header.Del(ProxyHostHeader)
	req.Header.Del("worker")
	req.Header.Del("worker_host")
	req.Header.Del("worker_port")
	if isUpgradeRequest(req) {
		serveWorkerProxyUpgrade(ctx, stream, streamReader, req)
		return
	}
	RemoveHopByHopHeaders(req.Header)
	resp, err := transport.RoundTrip(req.WithContext(ctx))
	if err != nil {
		writeStreamError(stream, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()
	RemoveHopByHopHeaders(resp.Header)
	if err := resp.Write(stream); err != nil {
		return
	}
}

func serveWorkerProxyUpgrade(ctx context.Context, stream *yamux.Stream, streamReader *bufio.Reader, req *http.Request) {
	target, err := (&net.Dialer{}).DialContext(ctx, "tcp", req.URL.Host)
	if err != nil {
		writeStreamError(stream, http.StatusBadGateway, err)
		return
	}
	defer target.Close()
	if err := req.Write(target); err != nil {
		writeStreamError(stream, http.StatusBadGateway, err)
		return
	}
	targetReader := bufio.NewReader(target)
	resp, err := http.ReadResponse(targetReader, req)
	if err != nil {
		writeStreamError(stream, http.StatusBadGateway, err)
		return
	}
	if err := resp.Write(stream); err != nil {
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Body.Close()
		return
	}
	errCh := make(chan error, 2)
	go copyRaw(errCh, target, streamReader)
	go copyRaw(errCh, stream, targetReader)
	<-errCh
	_ = target.Close()
	_ = stream.Close()
}

func isUpgradeRequest(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket") && headerHasToken(req.Header.Get("Connection"), "upgrade")
}

func headerHasToken(value, token string) bool {
	for _, item := range stringsSplitComma(value) {
		if strings.EqualFold(item, token) {
			return true
		}
	}
	return false
}

func writeStreamError(w io.Writer, status int, err error) {
	body := err.Error()
	resp := &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
	_ = resp.Write(w)
}
