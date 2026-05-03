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
	req, err := http.ReadRequest(bufio.NewReader(stream))
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
	req.RequestURI = ""
	req.URL.Scheme = "http"
	req.URL.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	req.Host = req.URL.Host
	req.Header.Del(ProxyPortHeader)
	req.Header.Del("worker")
	req.Header.Del("worker_port")
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
