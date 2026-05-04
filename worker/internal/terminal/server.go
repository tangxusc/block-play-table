package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	Enabled bool
	Host    string
	WorkDir string
	Shell   string
	Logger  *slog.Logger
}

type Server struct {
	enabled bool
	host    string
	workDir string
	shell   string
	logger  *slog.Logger

	mu       sync.RWMutex
	addr     string
	port     int
	listener net.Listener
	server   *http.Server
}

type clientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Cols int    `json:"cols,omitempty"`
}

type serverMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Code int    `json:"code,omitempty"`
}

var terminalUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func NewServer(config Config) *Server {
	host := strings.TrimSpace(config.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Server{
		enabled: config.Enabled,
		host:    host,
		workDir: config.WorkDir,
		shell:   strings.TrimSpace(config.Shell),
		logger:  config.Logger,
	}
}

func (s *Server) Start(ctx context.Context) error {
	if !s.enabled {
		return nil
	}
	if strings.TrimSpace(s.workDir) == "" {
		return fmt.Errorf("worker work dir is required")
	}
	if err := os.MkdirAll(s.workDir, 0o755); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.host, "0"))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/terminal/check", s.handleCheck)
	mux.HandleFunc("/terminal/ws", s.handleTerminal)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	_, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.port = port
	s.listener = ln
	s.server = srv
	s.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed && s.logger != nil {
			s.logger.Warn("terminal server stopped", "error", err)
		}
	}()
	if ctx != nil {
		go func() {
			<-ctx.Done()
			_ = s.Close()
		}()
	}
	return nil
}

func (s *Server) Close() error {
	s.mu.RLock()
	srv := s.server
	s.mu.RUnlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

func (s *Server) Capabilities() map[string]string {
	out := map[string]string{"terminal_enabled": "false"}
	if !s.enabled {
		return out
	}
	out["terminal_enabled"] = "true"
	out["terminal_host"] = s.host
	if port := s.Port(); port > 0 {
		out["terminal_port"] = strconv.Itoa(port)
	}
	return out
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := s.validateCwd(r.URL.Query().Get("cwd")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	cwd, err := s.validateCwd(r.URL.Query().Get("cwd"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	conn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	session, err := startShellSession(shellConfig{Cwd: cwd, Shell: s.shell, Rows: rows, Cols: cols})
	if err != nil {
		_ = conn.WriteJSON(serverMessage{Type: "error", Data: err.Error()})
		return
	}
	defer session.Close()
	s.serveSession(conn, session)
}

func (s *Server) validateCwd(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("terminal cwd is required")
	}
	workDir, err := filepath.Abs(s.workDir)
	if err != nil {
		return "", err
	}
	workDir, err = filepath.EvalSymlinks(workDir)
	if err != nil {
		return "", fmt.Errorf("worker work dir does not exist: %w", err)
	}
	candidate, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("terminal cwd does not exist: %w", err)
	}
	if !stat.IsDir() {
		return "", fmt.Errorf("terminal cwd is not a directory")
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(workDir, candidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("terminal cwd must be within worker work dir")
	}
	return candidate, nil
}

func (s *Server) serveSession(conn *websocket.Conn, session *shellSession) {
	var writeMu sync.Mutex
	writeJSON := func(message serverMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(message)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message clientMessage
			if err := json.Unmarshal(raw, &message); err != nil {
				_ = writeJSON(serverMessage{Type: "error", Data: err.Error()})
				continue
			}
			switch message.Type {
			case "input":
				if err := session.Write(message.Data); err != nil {
					_ = writeJSON(serverMessage{Type: "error", Data: err.Error()})
					return
				}
			case "resize":
				if err := session.Resize(message.Rows, message.Cols); err != nil {
					_ = writeJSON(serverMessage{Type: "error", Data: err.Error()})
				}
			case "close":
				return
			}
		}
	}()

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buffer := make([]byte, 4096)
		for {
			n, err := session.Read(buffer)
			if n > 0 {
				_ = writeJSON(serverMessage{Type: "output", Data: string(buffer[:n])})
			}
			if err != nil {
				if err != io.EOF && !terminalReadClosed(err) {
					_ = writeJSON(serverMessage{Type: "error", Data: err.Error()})
				}
				return
			}
		}
	}()

	waitDone := make(chan int, 1)
	go func() {
		waitDone <- session.Wait()
	}()

	select {
	case <-done:
	case <-outputDone:
	case code := <-waitDone:
		_ = writeJSON(serverMessage{Type: "exit", Code: code})
		return
	}
	_ = session.Close()
	select {
	case code := <-waitDone:
		_ = writeJSON(serverMessage{Type: "exit", Code: code})
	case <-time.After(500 * time.Millisecond):
	}
}
