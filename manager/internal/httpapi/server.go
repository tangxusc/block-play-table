package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/graph"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
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
	mux.Handle("/subscriptions", gqlHandler)
	return withCORS(mux)
}

func (s *Server) graphqlHandler() http.Handler {
	return handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: graph.NewResolver(s.service, s.gateway)}))
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-User-Role")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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

	mu          sync.RWMutex
	connections map[string]*workerConnection
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

func NewWorkerGateway(service *app.Service, logger *slog.Logger, workerToken string) *WorkerGateway {
	return &WorkerGateway{service: service, logger: logger, workerToken: workerToken, connections: map[string]*workerConnection{}}
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
		role := event.Metadata["role"]
		if role == "" {
			role = "assistant"
		}
		_, err := g.service.ApplyWorkerConversationWithMetadata(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), role, event.Content, event.Metadata)
		return err
	case protocol.MessageTaskWaitingInput:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		_, err := g.service.ApplyWorkerWaitingInput(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), event.Content)
		return err
	case protocol.MessageTaskResult:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		result := event.Result
		if result == "" {
			result = event.Content
		}
		_, err := g.service.ApplyWorkerTaskResult(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), result)
		return err
	case protocol.MessageTaskCompleted:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		_, err := g.service.ApplyWorkerTaskCompleted(ctx, envelope.MessageID, taskIDFromEnvelope(envelope, event), event.Result)
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
