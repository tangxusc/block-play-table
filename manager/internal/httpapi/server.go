package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/graph"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/frp"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/vektah/gqlparser/v2/ast"
)

type Server struct {
	service          *app.Service
	gateway          *WorkerGateway
	logger           *slog.Logger
	workerToken      string
	trustModeUserID  string
	heartbeatTimeout time.Duration
}

type Option func(*Server)

func WithWorkerToken(token string) Option {
	return func(s *Server) {
		s.workerToken = strings.TrimSpace(token)
	}
}

func WithTrustModeUserID(userID string) Option {
	return func(s *Server) {
		s.trustModeUserID = strings.TrimSpace(userID)
	}
}

func WithWorkerHeartbeatTimeout(timeout time.Duration) Option {
	return func(s *Server) {
		s.heartbeatTimeout = timeout
	}
}

func NewServer(service *app.Service, options ...Option) *Server {
	server := &Server{
		service:          service,
		logger:           slog.Default(),
		heartbeatTimeout: 90 * time.Second,
	}
	for _, option := range options {
		option(server)
	}
	server.gateway = NewWorkerGateway(service, server.logger, server.workerToken)
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	gqlHandler := s.graphqlHandler()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/auth/status", s.authStatus)
	mux.HandleFunc("/auth/verify", s.authVerify)
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, errorResponse("method not allowed"))
			return
		}
		gqlHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/worker/ws", s.gateway.Handle)
	mux.HandleFunc("/worker/frp", s.gateway.HandleFRP)
	mux.HandleFunc("/terminal/workers/", s.gateway.HandleWorkerTerminal)
	mux.HandleFunc("/terminal/tasks/", s.gateway.HandleTaskTerminal)
	mux.HandleFunc("/proxy", s.gateway.HandleProxy)
	mux.HandleFunc("/proxy/", s.gateway.HandleProxy)
	mux.Handle("/subscriptions", gqlHandler)
	return withCORS(s.withUserIdentity(s.withManagerToken(mux)))
}

func (s *Server) graphqlHandler() http.Handler {
	gqlHandler := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: graph.NewResolver(s.service, s.gateway)}))
	gqlHandler.AddTransport(transport.Websocket{
		KeepAlivePingInterval: 10 * time.Second,
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	})
	gqlHandler.AddTransport(transport.Options{})
	gqlHandler.AddTransport(transport.GET{})
	gqlHandler.AddTransport(transport.POST{})
	gqlHandler.AddTransport(transport.MultipartForm{})
	gqlHandler.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	gqlHandler.Use(extension.Introspection{})
	gqlHandler.Use(extension.AutomaticPersistedQuery{Cache: lru.New[string](100)})
	return gqlHandler
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.service.Store().Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"required": s.managerTokenRequired()})
}

func (s *Server) authVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse("method not allowed"))
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid JSON body"))
		return
	}
	if s.managerTokenRequired() && !constantTimeTokenEqual(input.Token, s.workerToken) {
		writeJSON(w, http.StatusUnauthorized, errorResponse("invalid manager token"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) withManagerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.managerTokenRequired() || !managerTokenProtectedPath(r.URL.Path) || s.requestHasManagerToken(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, errorResponse("invalid manager token"))
	})
}

func (s *Server) managerTokenRequired() bool {
	return strings.TrimSpace(s.workerToken) != ""
}

func (s *Server) requestHasManagerToken(r *http.Request) bool {
	return constantTimeTokenEqual(managerTokenFromRequest(r), s.workerToken)
}

func managerTokenProtectedPath(path string) bool {
	return path == "/graphql" ||
		path == "/subscriptions" ||
		path == "/proxy" ||
		strings.HasPrefix(path, "/proxy/") ||
		strings.HasPrefix(path, "/terminal/tasks/") ||
		strings.HasPrefix(path, "/terminal/workers/")
}

func managerTokenFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Manager-Token")); value != "" {
		return value
	}
	if value := bearerToken(r.Header.Get("Authorization")); value != "" {
		return value
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

func bearerToken(header string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func constantTimeTokenEqual(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" || want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errorResponse(message string) map[string]any {
	return map[string]any{"errors": []map[string]string{{"message": message}}}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Manager-Token, X-API-Key, X-User-Role, X-User-Id, worker, worker_host, worker_port")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var defaultUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) withUserIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var userID string
		if s.managerTokenRequired() {
			userID = s.trustModeUserID
		} else {
			userID = strings.TrimSpace(r.Header.Get("X-User-Id"))
		}
		ctx := app.WithUserIdentity(r.Context(), userID, s.managerTokenRequired())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type WorkerGateway struct {
	service     *app.Service
	logger      *slog.Logger
	workerToken string

	mu          sync.RWMutex
	connections map[string]*workerConnection
	proxyByName map[string]*workerProxyTunnel
}

type workerConnection struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c *workerConnection) writeJSON(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(value)
}

type workerProxyTunnel struct {
	workerID    string
	workerName  string
	conn        *websocket.Conn
	session     *yamux.Session
	connectedAt time.Time
}

func (t *workerProxyTunnel) close() {
	if t == nil {
		return
	}
	if t.session != nil {
		_ = t.session.Close()
	}
	if t.conn != nil {
		_ = t.conn.Close()
	}
}

func NewWorkerGateway(service *app.Service, logger *slog.Logger, workerToken string) *WorkerGateway {
	return &WorkerGateway{
		service:     service,
		logger:      logger,
		workerToken: workerToken,
		connections: map[string]*workerConnection{},
		proxyByName: map[string]*workerProxyTunnel{},
	}
}

func (g *WorkerGateway) Handle(w http.ResponseWriter, r *http.Request) {
	if g.workerToken != "" && r.URL.Query().Get("token") != g.workerToken {
		http.Error(w, "invalid worker token", http.StatusUnauthorized)
		return
	}
	conn, err := defaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	wsConn := &workerConnection{conn: conn}
	workerID := r.URL.Query().Get("worker_id")
	var pumpCancel context.CancelFunc
	defer func() {
		if pumpCancel != nil {
			pumpCancel()
		}
	}()
	startPump := func(id string) {
		pumpCtx, cancel := context.WithCancel(r.Context())
		pumpCancel = cancel
		go g.runWatchPump(pumpCtx, id, wsConn)
	}
	if workerID != "" {
		g.trackConnection(workerID, wsConn)
		startPump(workerID)
	}
	defer func() {
		removed := g.untrackConnection(workerID, wsConn)
		if removed {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := g.service.WorkerDisconnected(ctx, workerID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				g.logger.Warn("mark worker offline failed", "workerId", workerID, "error", err)
			}
			if _, err := g.service.MarkWorkerLost(ctx, workerID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				g.logger.Warn("mark worker lost failed", "workerId", workerID, "error", err)
			}
		}
		_ = conn.Close()
	}()
	for {
		var envelope rawEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return
		}
		if envelope.WorkerID != "" && envelope.WorkerID != workerID {
			if pumpCancel != nil {
				pumpCancel()
				pumpCancel = nil
			}
			if workerID != "" {
				g.untrackConnection(workerID, wsConn)
			}
			workerID = envelope.WorkerID
			g.trackConnection(workerID, wsConn)
			startPump(workerID)
		}
		if err := g.apply(r.Context(), envelope, workerID); err != nil {
			_ = wsConn.writeJSON(protocol.Envelope{MessageID: "msg_" + uuid.NewString(), Type: protocol.MessageTaskFailed, WorkerID: workerID, Timestamp: time.Now().UTC(), Payload: map[string]string{"error": err.Error()}})
		}
	}
}

func (g *WorkerGateway) HandleFRP(w http.ResponseWriter, r *http.Request) {
	if g.workerToken != "" && r.URL.Query().Get("token") != g.workerToken {
		http.Error(w, "invalid worker token", http.StatusUnauthorized)
		return
	}
	workerID := strings.TrimSpace(r.URL.Query().Get("worker_id"))
	workerName := strings.TrimSpace(r.URL.Query().Get("worker_name"))
	if workerID == "" || workerName == "" {
		http.Error(w, "worker_id and worker_name are required", http.StatusBadRequest)
		return
	}
	if err := g.ensureProxyTunnelNameAvailable(workerName, workerID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	conn, err := defaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	session, err := yamux.Server(frp.NewWebSocketConn(conn), nil)
	if err != nil {
		_ = conn.Close()
		return
	}
	tunnel := &workerProxyTunnel{
		workerID:    workerID,
		workerName:  workerName,
		conn:        conn,
		session:     session,
		connectedAt: time.Now().UTC(),
	}
	if err := g.trackProxyTunnel(tunnel); err != nil {
		tunnel.close()
		return
	}
	defer func() {
		g.untrackProxyTunnel(workerName, tunnel)
		tunnel.close()
	}()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return
		}
		_ = stream.Close()
	}
}

func (g *WorkerGateway) HandleProxy(w http.ResponseWriter, r *http.Request) {
	target, err := proxyTargetFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tunnel := g.proxyTunnel(target.workerName)
	if tunnel == nil || tunnel.session == nil || tunnel.session.IsClosed() {
		http.Error(w, "worker proxy tunnel is not connected", http.StatusServiceUnavailable)
		return
	}
	proxyRequest := requestWithoutManagerTokenQuery(r, g.workerToken)
	resp, err := frp.ProxyHTTP(r.Context(), tunnel.session, proxyRequest, frp.ProxyTarget{
		Host: target.host,
		Port: target.port,
		Path: target.path,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	frp.RemoveHopByHopHeaders(resp.Header)
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func requestWithoutManagerTokenQuery(r *http.Request, workerToken string) *http.Request {
	if r == nil || !constantTimeTokenEqual(r.URL.Query().Get("token"), workerToken) {
		return r
	}
	cloned := r.Clone(r.Context())
	query := cloned.URL.Query()
	query.Del("token")
	cloned.URL.RawQuery = query.Encode()
	return cloned
}

func (g *WorkerGateway) HandleTaskTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if taskID, ok := terminalTaskIDFromCheckPath(r.URL.Path); ok {
		g.handleTaskTerminalCheck(w, r, taskID)
		return
	}
	taskID, ok := terminalTaskIDFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	target, status, message := g.resolveTaskTerminal(r.Context(), taskID)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket hijacking is not supported", http.StatusInternalServerError)
		return
	}
	client, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	query := url.Values{}
	query.Set("cwd", target.task.WorktreePath)
	for _, key := range []string{"rows", "cols"} {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			query.Set(key, value)
		}
	}
	targetPath := "/terminal/ws?" + query.Encode()
	if err := frp.ProxyUpgrade(r.Context(), target.tunnel.session, r, frp.ProxyTarget{
		Host: target.host,
		Port: target.port,
		Path: targetPath,
	}, client, rw.Reader); err != nil && g.logger != nil {
		g.logger.Warn("task terminal proxy failed", "taskId", target.task.ID, "workerId", target.worker.ID, "error", err)
	}
}

func (g *WorkerGateway) HandleWorkerTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if workerID, ok := workerTerminalIDFromCheckPath(r.URL.Path); ok {
		g.handleWorkerTerminalCheck(w, r, workerID)
		return
	}
	workerID, ok := workerTerminalIDFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	target, status, message := g.resolveWorkerTerminal(r.Context(), workerID)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket hijacking is not supported", http.StatusInternalServerError)
		return
	}
	client, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	query := url.Values{}
	query.Set("cwd", target.worker.WorkDir)
	for _, key := range []string{"rows", "cols"} {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			query.Set(key, value)
		}
	}
	targetPath := "/terminal/ws?" + query.Encode()
	if err := frp.ProxyUpgrade(r.Context(), target.tunnel.session, r, frp.ProxyTarget{
		Host: target.host,
		Port: target.port,
		Path: targetPath,
	}, client, rw.Reader); err != nil && g.logger != nil {
		g.logger.Warn("worker terminal proxy failed", "workerId", target.worker.ID, "error", err)
	}
}

func (g *WorkerGateway) handleTaskTerminalCheck(w http.ResponseWriter, r *http.Request, taskID string) {
	target, status, message := g.resolveTaskTerminal(r.Context(), taskID)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	query := url.Values{}
	query.Set("cwd", target.task.WorktreePath)
	resp, err := frp.ProxyHTTP(r.Context(), target.tunnel.session, r, frp.ProxyTarget{
		Host: target.host,
		Port: target.port,
		Path: "/terminal/check?" + query.Encode(),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	frp.RemoveHopByHopHeaders(resp.Header)
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g *WorkerGateway) handleWorkerTerminalCheck(w http.ResponseWriter, r *http.Request, workerID string) {
	target, status, message := g.resolveWorkerTerminal(r.Context(), workerID)
	if status != 0 {
		http.Error(w, message, status)
		return
	}
	query := url.Values{}
	query.Set("cwd", target.worker.WorkDir)
	resp, err := frp.ProxyHTTP(r.Context(), target.tunnel.session, r, frp.ProxyTarget{
		Host: target.host,
		Port: target.port,
		Path: "/terminal/check?" + query.Encode(),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	frp.RemoveHopByHopHeaders(resp.Header)
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

type taskTerminalTarget struct {
	task   *domain.Task
	worker *domain.Worker
	host   string
	port   int
	tunnel *workerProxyTunnel
}

type workerTerminalTarget struct {
	worker *domain.Worker
	host   string
	port   int
	tunnel *workerProxyTunnel
}

func (g *WorkerGateway) resolveTaskTerminal(ctx context.Context, taskID string) (taskTerminalTarget, int, string) {
	task, err := g.service.Task(ctx, taskID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return taskTerminalTarget{}, http.StatusNotFound, "task not found"
		}
		return taskTerminalTarget{}, http.StatusInternalServerError, err.Error()
	}
	if task.Status == domain.TaskArchived {
		return taskTerminalTarget{}, http.StatusConflict, "task is archived"
	}
	if strings.TrimSpace(task.WorkerID) == "" {
		return taskTerminalTarget{}, http.StatusConflict, "task has no worker"
	}
	if strings.TrimSpace(task.WorktreePath) == "" {
		return taskTerminalTarget{}, http.StatusConflict, "task has no worktree"
	}
	worker, err := g.service.Worker(ctx, task.WorkerID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return taskTerminalTarget{}, http.StatusNotFound, "worker not found"
		}
		return taskTerminalTarget{}, http.StatusInternalServerError, err.Error()
	}
	if worker.Status != domain.WorkerOnline {
		return taskTerminalTarget{}, http.StatusConflict, "worker is not online"
	}
	host, port, err := terminalTarget(worker.Capabilities)
	if err != nil {
		return taskTerminalTarget{}, http.StatusConflict, err.Error()
	}
	tunnel := g.proxyTunnel(worker.Name)
	if tunnel == nil || tunnel.session == nil || tunnel.session.IsClosed() {
		return taskTerminalTarget{}, http.StatusServiceUnavailable, "worker proxy tunnel is not connected"
	}
	return taskTerminalTarget{task: task, worker: worker, host: host, port: port, tunnel: tunnel}, 0, ""
}

func (g *WorkerGateway) resolveWorkerTerminal(ctx context.Context, workerID string) (workerTerminalTarget, int, string) {
	worker, err := g.service.Worker(ctx, workerID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return workerTerminalTarget{}, http.StatusNotFound, "worker not found"
		}
		return workerTerminalTarget{}, http.StatusInternalServerError, err.Error()
	}
	if worker.Status != domain.WorkerOnline {
		return workerTerminalTarget{}, http.StatusConflict, "worker is not online"
	}
	host, port, err := terminalTarget(worker.Capabilities)
	if err != nil {
		return workerTerminalTarget{}, http.StatusConflict, err.Error()
	}
	tunnel := g.proxyTunnel(worker.Name)
	if tunnel == nil || tunnel.session == nil || tunnel.session.IsClosed() {
		return workerTerminalTarget{}, http.StatusServiceUnavailable, "worker proxy tunnel is not connected"
	}
	return workerTerminalTarget{worker: worker, host: host, port: port, tunnel: tunnel}, 0, ""
}

type proxyRequestTarget struct {
	workerName string
	host       string
	port       int
	path       string
}

func terminalTaskIDFromPath(path string) (string, bool) {
	const prefix = "/terminal/tasks/"
	const suffix = "/ws"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "/") {
		return "", false
	}
	taskID, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(taskID) == "" {
		return "", false
	}
	return strings.TrimSpace(taskID), true
}

func terminalTaskIDFromCheckPath(path string) (string, bool) {
	const prefix = "/terminal/tasks/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "/") {
		return "", false
	}
	taskID, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(taskID) == "" {
		return "", false
	}
	return strings.TrimSpace(taskID), true
}

func workerTerminalIDFromPath(path string) (string, bool) {
	const prefix = "/terminal/workers/"
	const suffix = "/ws"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "/") {
		return "", false
	}
	workerID, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(workerID) == "" {
		return "", false
	}
	return strings.TrimSpace(workerID), true
}

func workerTerminalIDFromCheckPath(path string) (string, bool) {
	const prefix = "/terminal/workers/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "/") {
		return "", false
	}
	workerID, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(workerID) == "" {
		return "", false
	}
	return strings.TrimSpace(workerID), true
}

func terminalTarget(capabilities map[string]string) (string, int, error) {
	if !strings.EqualFold(strings.TrimSpace(capabilities["terminal_enabled"]), "true") {
		return "", 0, fmt.Errorf("worker terminal is not enabled")
	}
	host := strings.TrimSpace(capabilities["terminal_host"])
	if host == "" {
		host = "127.0.0.1"
	}
	if _, err := frp.NormalizeProxyHost(host); err != nil {
		return "", 0, err
	}
	port, err := proxyPort(capabilities["terminal_port"])
	if err != nil {
		return "", 0, fmt.Errorf("worker terminal port is invalid")
	}
	return host, port, nil
}

func proxyTargetFromRequest(r *http.Request) (proxyRequestTarget, error) {
	if target, ok, err := proxyWebTargetFromPath(r.URL.Path); ok || err != nil {
		return target, err
	}
	workerName := strings.TrimSpace(r.Header.Get("worker"))
	host := strings.TrimSpace(r.Header.Get("worker_host"))
	portText := strings.TrimSpace(r.Header.Get("worker_port"))
	if workerName == "" || portText == "" {
		return proxyRequestTarget{}, fmt.Errorf("worker and worker_port headers are required")
	}
	port, err := proxyPort(portText)
	if err != nil {
		return proxyRequestTarget{}, err
	}
	if _, err := frp.NormalizeProxyHost(host); err != nil {
		return proxyRequestTarget{}, err
	}
	return proxyRequestTarget{
		workerName: workerName,
		host:       host,
		port:       port,
		path:       stripProxyPath(r.URL.Path),
	}, nil
}

func proxyWebTargetFromPath(path string) (proxyRequestTarget, bool, error) {
	if !strings.HasPrefix(path, "/proxy/web/") {
		return proxyRequestTarget{}, false, nil
	}
	rest := strings.TrimPrefix(path, "/proxy/web/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 3 {
		return proxyRequestTarget{}, true, fmt.Errorf("proxy web route must include worker, host, and port")
	}
	workerName, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(workerName) == "" {
		return proxyRequestTarget{}, true, fmt.Errorf("proxy web worker is invalid")
	}
	host, err := url.PathUnescape(parts[1])
	if err != nil {
		return proxyRequestTarget{}, true, fmt.Errorf("proxy web host is invalid")
	}
	if _, err := frp.NormalizeProxyHost(host); err != nil {
		return proxyRequestTarget{}, true, err
	}
	port, err := proxyPort(parts[2])
	if err != nil {
		return proxyRequestTarget{}, true, err
	}
	targetPath := "/"
	if len(parts) == 4 && parts[3] != "" {
		targetPath = "/" + parts[3]
	}
	return proxyRequestTarget{
		workerName: strings.TrimSpace(workerName),
		host:       strings.TrimSpace(host),
		port:       port,
		path:       targetPath,
	}, true, nil
}

func proxyPort(portText string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("worker_port must be a TCP port between 1 and 65535")
	}
	return port, nil
}

func (g *WorkerGateway) trackConnection(workerID string, conn *workerConnection) {
	if workerID == "" {
		return
	}
	g.mu.Lock()
	previous := g.connections[workerID]
	g.connections[workerID] = conn
	g.mu.Unlock()
	if previous != nil && previous != conn {
		_ = previous.conn.Close()
	}
}

func (g *WorkerGateway) untrackConnection(workerID string, conn *workerConnection) bool {
	if workerID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.connections[workerID] != conn {
		return false
	}
	delete(g.connections, workerID)
	return true
}

func (g *WorkerGateway) ensureProxyTunnelNameAvailable(workerName, workerID string) error {
	g.mu.RLock()
	existing := g.proxyByName[workerName]
	g.mu.RUnlock()
	if existing != nil && existing.workerID != workerID {
		return fmt.Errorf("worker proxy name %q is already connected by worker %s", workerName, existing.workerID)
	}
	return nil
}

func (g *WorkerGateway) trackProxyTunnel(tunnel *workerProxyTunnel) error {
	if tunnel == nil || tunnel.workerName == "" {
		return fmt.Errorf("worker proxy tunnel is required")
	}
	g.mu.Lock()
	previous := g.proxyByName[tunnel.workerName]
	if previous != nil && previous.workerID != tunnel.workerID {
		g.mu.Unlock()
		return fmt.Errorf("worker proxy name %q is already connected by worker %s", tunnel.workerName, previous.workerID)
	}
	g.proxyByName[tunnel.workerName] = tunnel
	g.mu.Unlock()
	if previous != nil && previous != tunnel {
		previous.close()
	}
	return nil
}

func (g *WorkerGateway) untrackProxyTunnel(workerName string, tunnel *workerProxyTunnel) {
	if workerName == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.proxyByName[workerName] == tunnel {
		delete(g.proxyByName, workerName)
	}
}

func (g *WorkerGateway) proxyTunnel(workerName string) *workerProxyTunnel {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.proxyByName[workerName]
}

func stripProxyPath(path string) string {
	switch {
	case path == "" || path == "/proxy" || path == "/proxy/":
		return "/"
	case strings.HasPrefix(path, "/proxy/"):
		return strings.TrimPrefix(path, "/proxy")
	default:
		return path
	}
}

type rawEnvelope struct {
	MessageID string               `json:"messageId"`
	Type      protocol.MessageType `json:"type"`
	WorkerID  string               `json:"workerId,omitempty"`
	TaskID    string               `json:"taskId,omitempty"`
	Timestamp time.Time            `json:"timestamp"`
	Payload   json.RawMessage      `json:"payload,omitempty"`
}

func (g *WorkerGateway) apply(ctx context.Context, envelope rawEnvelope, fallbackWorkerID string) error {
	workerID := envelope.WorkerID
	if workerID == "" {
		workerID = fallbackWorkerID
	}
	switch envelope.Type {
	case protocol.MessageWorkerRegister:
		var input app.RegisterWorkerInput
		if len(envelope.Payload) > 0 {
			if err := json.Unmarshal(envelope.Payload, &input); err != nil {
				return err
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(envelope.Payload, &raw); err == nil {
				_, input.ReplaceAgentRuntimeEnv = raw["agentRuntimeEnv"]
			}
		}
		if input.ID == "" {
			input.ID = workerID
		}
		worker, err := g.service.RegisterWorker(ctx, input)
		if err != nil {
			return err
		}
		_, err = g.service.WorkerConnected(ctx, worker.ID)
		return err
	case protocol.MessageWorkerHeartbeat:
		_, err := g.service.WorkerHeartbeat(ctx, workerID)
		return err
	case protocol.MessageTaskAccepted:
		_, err := g.service.ApplyWorkerTaskAccepted(ctx, envelope.MessageID, envelope.TaskID)
		return err
	case protocol.MessageTaskStarted:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		worktree := event.Content
		if worktree == "" {
			worktree = event.Result
		}
		_, err := g.service.ApplyWorkerTaskStarted(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), worktree)
		return err
	case protocol.MessageTaskLog:
		var event protocol.WorkerEvent
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			return err
		}
		_, err := g.service.ApplyWorkerTaskLog(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), event.Stream, event.Content)
		return err
	case protocol.MessageTaskConversation:
		var event protocol.WorkerEvent
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			return err
		}
		metadata := cloneMetadata(event.Metadata)
		if event.AgentSessionID != "" {
			metadata["agentSessionId"] = event.AgentSessionID
		}
		role := metadata["role"]
		if role == "" {
			role = "assistant"
		}
		_, err := g.service.ApplyWorkerConversationWithMetadata(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), role, event.Content, metadata)
		return err
	case protocol.MessageTaskWaitingInput:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		_, err := g.service.ApplyWorkerWaitingInput(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), event.Content)
		return err
	case protocol.MessageTaskInteractionRequest:
		var payload protocol.TaskInteractionRequestPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return err
		}
		if payload.TaskID == "" {
			payload.TaskID = envelope.TaskID
		}
		_, err := g.service.ApplyWorkerTaskInteractionRequest(ctx, envelope.MessageID, app.TaskInteractionRequestInput{
			InteractionID:  payload.InteractionID,
			TaskID:         payload.TaskID,
			Kind:           payload.Kind,
			Title:          payload.Title,
			Body:           payload.Body,
			RawPayload:     payload.RawPayload,
			AgentSessionID: payload.AgentSessionID,
		})
		return err
	case protocol.MessageTaskInteractionResolved:
		var payload protocol.TaskInteractionResolvedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return err
		}
		if payload.TaskID == "" {
			payload.TaskID = envelope.TaskID
		}
		_, err := g.service.ApplyWorkerTaskInteractionResolved(ctx, envelope.MessageID, app.TaskInteractionResolvedInput{
			InteractionID: payload.InteractionID,
			TaskID:        payload.TaskID,
			Responded:     payload.Responded,
			Decision:      payload.Decision,
			Message:       payload.Message,
			Payload:       payload.Payload,
		})
		return err
	case protocol.MessageTaskResult:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		result := event.Result
		if result == "" {
			result = event.Content
		}
		_, err := g.service.ApplyWorkerTaskResult(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), result, event.AgentSessionID)
		return err
	case protocol.MessageTaskCompleted:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		_, err := g.service.ApplyWorkerTaskCompleted(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), event.Result, event.AgentSessionID)
		return err
	case protocol.MessageTaskFailed:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		_, err := g.service.ApplyWorkerTaskFailed(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), event.Result)
		return err
	case protocol.MessageTaskInterrupted:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		result := event.Result
		if result == "" {
			result = event.Content
		}
		_, err := g.service.ApplyWorkerTaskInterrupted(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), result)
		return err
	default:
		return nil
	}
}

func taskIDFromEnvelope(envelope rawEnvelope, event protocol.WorkerEvent) string {
	if envelope.TaskID != "" {
		return envelope.TaskID
	}
	return event.TaskID
}

func cloneMetadata(metadata map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func (g *WorkerGateway) runWatchPump(ctx context.Context, workerID string, wsConn *workerConnection) {
	if workerID == "" {
		return
	}
	events, unsubscribe := g.service.SubscribeDomainEvents(ctx, domain.EventFilter{})
	defer unsubscribe()
	tasks, err := g.service.AssignedTasks(ctx, workerID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		g.logger.Warn("watch pump list failed", "workerId", workerID, "error", err)
	}
	for _, task := range tasks {
		if err := g.dispatchTaskState(ctx, wsConn, task.ID); err != nil {
			g.logger.Warn("watch pump initial dispatch failed", "workerId", workerID, "taskId", task.ID, "error", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if !watchEventForWorker(event, workerID) {
				continue
			}
			taskID := event.AggregateID
			if event.AggregateType != "Task" || taskID == "" {
				continue
			}
			if err := g.dispatchTaskState(ctx, wsConn, taskID); err != nil {
				if !errors.Is(err, domain.ErrNotFound) {
					g.logger.Warn("watch pump dispatch failed", "workerId", workerID, "taskId", taskID, "error", err)
				}
			}
		}
	}
}

func watchEventForWorker(event domain.DomainEvent, workerID string) bool {
	if event.AggregateType != "Task" {
		return false
	}
	switch event.EventType {
	case "TaskAssigned", "TaskStartRequested", "TaskContinueRequested", "TaskInterruptRequested", "TaskDirectiveQueued", "TaskDirectiveAcked":
	default:
		return false
	}
	var payload map[string]any
	if len(event.Payload) > 0 {
		_ = json.Unmarshal(event.Payload, &payload)
	}
	if id, ok := payload["workerId"].(string); ok && id != "" {
		return id == workerID
	}
	return true
}

func (g *WorkerGateway) dispatchTaskState(ctx context.Context, wsConn *workerConnection, taskID string) error {
	task, project, worker, err := g.service.AssignedTaskBundle(ctx, taskID)
	if err != nil {
		return err
	}
	switch task.Status {
	case domain.TaskStarting:
		if task.PendingDirective != nil && task.PendingDirective.Kind == domain.TaskDirectiveContinue {
			payload := g.service.BuildContinuePayload(task, project, worker, task.PendingDirective.Message)
			return wsConn.writeJSON(protocol.Envelope{
				MessageID: "msg_" + uuid.NewString(),
				Type:      protocol.MessageTaskContinue,
				WorkerID:  worker.ID,
				TaskID:    task.ID,
				Timestamp: time.Now().UTC(),
				Payload:   payload,
			})
		}
		payload := g.service.BuildStartPayload(task, project, worker)
		return wsConn.writeJSON(protocol.Envelope{
			MessageID: "msg_" + uuid.NewString(),
			Type:      protocol.MessageTaskStart,
			WorkerID:  worker.ID,
			TaskID:    task.ID,
			Timestamp: time.Now().UTC(),
			Payload:   payload,
		})
	case domain.TaskInterrupting:
		return wsConn.writeJSON(protocol.Envelope{
			MessageID: "msg_" + uuid.NewString(),
			Type:      protocol.MessageTaskInterrupt,
			WorkerID:  worker.ID,
			TaskID:    task.ID,
			Timestamp: time.Now().UTC(),
		})
	case domain.TaskRunning, domain.TaskWaitingInput:
		if task.PendingDirective != nil && task.PendingDirective.Kind == domain.TaskDirectiveInteraction {
			payload := protocol.TaskInteractionResponsePayload{
				InteractionID: task.PendingDirective.InteractionID,
				TaskID:        task.ID,
				Decision:      task.PendingDirective.Decision,
				Message:       task.PendingDirective.Message,
				Payload:       task.PendingDirective.Payload,
			}
			return wsConn.writeJSON(protocol.Envelope{
				MessageID: "msg_" + uuid.NewString(),
				Type:      protocol.MessageTaskInteractionResponse,
				WorkerID:  worker.ID,
				TaskID:    task.ID,
				Timestamp: time.Now().UTC(),
				Payload:   payload,
			})
		}
	}
	return nil
}
