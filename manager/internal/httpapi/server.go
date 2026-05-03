package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	heartbeatTimeout time.Duration
}

type Option func(*Server)

func WithWorkerToken(token string) Option {
	return func(s *Server) {
		s.workerToken = token
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
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, errorResponse("method not allowed"))
			return
		}
		gqlHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/worker/ws", s.gateway.Handle)
	mux.HandleFunc("/worker/frp", s.gateway.HandleFRP)
	mux.HandleFunc("/proxy", s.gateway.HandleProxy)
	mux.HandleFunc("/proxy/", s.gateway.HandleProxy)
	mux.Handle("/subscriptions", gqlHandler)
	return withCORS(mux)
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-User-Role, worker, worker_port")
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

type WorkerGateway struct {
	service     *app.Service
	logger      *slog.Logger
	workerToken string

	mu                 sync.RWMutex
	connections        map[string]*workerConnection
	proxyByName        map[string]*workerProxyTunnel
	interactionMu      sync.Mutex
	interactionWaiters map[string]chan error
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
		service:            service,
		logger:             logger,
		workerToken:        workerToken,
		connections:        map[string]*workerConnection{},
		proxyByName:        map[string]*workerProxyTunnel{},
		interactionWaiters: map[string]chan error{},
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
	if workerID != "" {
		g.trackConnection(workerID, wsConn)
	}
	defer func() {
		removed := g.untrackConnection(workerID, wsConn)
		if removed {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := g.service.WorkerDisconnected(ctx, workerID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				g.logger.Warn("mark worker offline failed", "workerId", workerID, "error", err)
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
			if workerID != "" {
				g.untrackConnection(workerID, wsConn)
			}
			workerID = envelope.WorkerID
			g.trackConnection(workerID, wsConn)
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
	workerName := strings.TrimSpace(r.Header.Get("worker"))
	portText := strings.TrimSpace(r.Header.Get("worker_port"))
	if workerName == "" || portText == "" {
		http.Error(w, "worker and worker_port headers are required", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "worker_port must be a TCP port between 1 and 65535", http.StatusBadRequest)
		return
	}
	tunnel := g.proxyTunnel(workerName)
	if tunnel == nil || tunnel.session == nil || tunnel.session.IsClosed() {
		http.Error(w, "worker proxy tunnel is not connected", http.StatusServiceUnavailable)
		return
	}
	resp, err := frp.ProxyHTTP(r.Context(), tunnel.session, r, frp.ProxyTarget{
		Port: port,
		Path: stripProxyPath(r.URL.Path),
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
		g.resolveInteractionWaiter(payload.InteractionID, err)
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

func (g *WorkerGateway) SendTaskStart(workerID, taskID string, payload protocol.TaskStartPayload) error {
	return g.send(workerID, protocol.Envelope{
		MessageID: "msg_" + uuid.NewString(),
		Type:      protocol.MessageTaskStart,
		WorkerID:  workerID,
		TaskID:    taskID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	})
}

func (g *WorkerGateway) SendTaskContinue(workerID, taskID string, payload protocol.TaskContinuePayload) error {
	return g.send(workerID, protocol.Envelope{
		MessageID: "msg_" + uuid.NewString(),
		Type:      protocol.MessageTaskContinue,
		WorkerID:  workerID,
		TaskID:    taskID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	})
}

func (g *WorkerGateway) SendTaskInteractionResponse(workerID, taskID string, payload protocol.TaskInteractionResponsePayload) error {
	if workerID == "" {
		return fmt.Errorf("worker id is required")
	}
	if payload.TaskID == "" {
		payload.TaskID = taskID
	}
	waiter := g.registerInteractionWaiter(payload.InteractionID)
	if err := g.send(workerID, protocol.Envelope{
		MessageID: "msg_" + uuid.NewString(),
		Type:      protocol.MessageTaskInteractionResponse,
		WorkerID:  workerID,
		TaskID:    taskID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}); err != nil {
		g.removeInteractionWaiter(payload.InteractionID)
		return err
	}
	select {
	case err := <-waiter:
		return err
	case <-time.After(10 * time.Second):
		g.removeInteractionWaiter(payload.InteractionID)
		return fmt.Errorf("worker %s did not resolve interaction %s", workerID, payload.InteractionID)
	}
}

func (g *WorkerGateway) SendTaskInterrupt(workerID, taskID string) error {
	if workerID == "" {
		return nil
	}
	return g.send(workerID, protocol.Envelope{
		MessageID: "msg_" + uuid.NewString(),
		Type:      protocol.MessageTaskInterrupt,
		WorkerID:  workerID,
		TaskID:    taskID,
		Timestamp: time.Now().UTC(),
	})
}

func (g *WorkerGateway) SendTaskCancel(workerID, taskID string) error {
	if workerID == "" {
		return nil
	}
	return g.send(workerID, protocol.Envelope{
		MessageID: "msg_" + uuid.NewString(),
		Type:      protocol.MessageTaskCancel,
		WorkerID:  workerID,
		TaskID:    taskID,
		Timestamp: time.Now().UTC(),
	})
}

func (g *WorkerGateway) send(workerID string, envelope protocol.Envelope) error {
	g.mu.RLock()
	conn := g.connections[workerID]
	g.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("worker %s is not connected", workerID)
	}
	return conn.writeJSON(envelope)
}

func (g *WorkerGateway) registerInteractionWaiter(interactionID string) chan error {
	waiter := make(chan error, 1)
	if interactionID == "" {
		waiter <- nil
		return waiter
	}
	g.interactionMu.Lock()
	g.interactionWaiters[interactionID] = waiter
	g.interactionMu.Unlock()
	return waiter
}

func (g *WorkerGateway) resolveInteractionWaiter(interactionID string, err error) {
	if interactionID == "" {
		return
	}
	g.interactionMu.Lock()
	waiter := g.interactionWaiters[interactionID]
	delete(g.interactionWaiters, interactionID)
	g.interactionMu.Unlock()
	if waiter != nil {
		waiter <- err
	}
}

func (g *WorkerGateway) removeInteractionWaiter(interactionID string) {
	if interactionID == "" {
		return
	}
	g.interactionMu.Lock()
	delete(g.interactionWaiters, interactionID)
	g.interactionMu.Unlock()
}

func cloneMetadata(metadata map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
