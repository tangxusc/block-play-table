package frp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/hashicorp/yamux"
)

const ProxyPortHeader = "X-Block-Play-Proxy-Port"

type ProxyTarget struct {
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
	if target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("worker port %d is invalid", target.Port)
	}
	if target.Path == "" {
		target.Path = "/"
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

	out := req.Clone(ctx)
	out.RequestURI = ""
	out.URL.Path = target.Path
	out.URL.RawPath = ""
	out.URL.Scheme = ""
	out.URL.Host = ""
	out.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(target.Port))
	out.Header.Del("worker")
	out.Header.Del("worker_port")
	out.Header.Set(ProxyPortHeader, strconv.Itoa(target.Port))
	RemoveHopByHopHeaders(out.Header)
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
