package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tangxusc/block-play-table/manager/internal/app"
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
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/graphql", s.graphql)
	mux.HandleFunc("/worker/ws", s.gateway.Handle)
	mux.HandleFunc("/subscriptions", s.subscriptions)
	return withCORS(mux)
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

type graphQLRequest struct {
	Query     string                     `json:"query"`
	Variables map[string]json.RawMessage `json:"variables"`
}

func (s *Server) graphql(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse("method not allowed"))
		return
	}
	var request graphQLRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}
	data, err := s.dispatchGraphQL(r.Context(), request)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"errors": []map[string]string{{"message": err.Error()}}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) dispatchGraphQL(ctx context.Context, request graphQLRequest) (map[string]any, error) {
	query := request.Query
	switch {
	case strings.Contains(query, "createProject"):
		var input app.CreateProjectInput
		if err := decodeVar(request, "input", &input); err != nil {
			return nil, err
		}
		project, err := s.service.CreateProject(ctx, input)
		return map[string]any{"createProject": project}, err
	case strings.Contains(query, "archiveProject"):
		var id string
		if err := decodeVar(request, "id", &id); err != nil {
			return nil, err
		}
		project, err := s.service.ArchiveProject(ctx, id)
		return map[string]any{"archiveProject": project}, err
	case strings.Contains(query, "projects"):
		projects, err := s.service.Projects(ctx)
		return map[string]any{"projects": projects}, err
	case strings.Contains(query, "registerWorker") || strings.Contains(query, "createWorker"):
		var input app.RegisterWorkerInput
		if err := decodeVar(request, "input", &input); err != nil {
			return nil, err
		}
		worker, err := s.service.RegisterWorker(ctx, input)
		if err != nil {
			return nil, err
		}
		worker, err = s.service.WorkerConnected(ctx, worker.ID)
		if strings.Contains(query, "createWorker") {
			return map[string]any{"createWorker": worker}, err
		}
		return map[string]any{"registerWorker": worker}, err
	case strings.Contains(query, "workers"):
		workers, err := s.service.Workers(ctx)
		return map[string]any{"workers": workers}, err
	case strings.Contains(query, "createTask"):
		var input app.CreateTaskInput
		if err := decodeVar(request, "input", &input); err != nil {
			return nil, err
		}
		task, err := s.service.CreateTask(ctx, input)
		return map[string]any{"createTask": task}, err
	case strings.Contains(query, "assignWorker"):
		var taskID, workerID string
		if err := decodeVar(request, "taskId", &taskID); err != nil {
			return nil, err
		}
		if err := decodeVar(request, "workerId", &workerID); err != nil {
			return nil, err
		}
		task, err := s.service.AssignWorker(ctx, taskID, workerID)
		return map[string]any{"assignWorker": task}, err
	case strings.Contains(query, "startTask"):
		var taskID string
		if err := decodeVarAny(request, []string{"taskId", "id"}, &taskID); err != nil {
			return nil, err
		}
		task, payload, err := s.service.StartTask(ctx, taskID)
		if err == nil {
			_ = s.gateway.SendTaskStart(task.WorkerID, task.ID, payload)
		}
		return map[string]any{"startTask": task}, err
	case strings.Contains(query, "interruptTask"):
		var taskID string
		if err := decodeVarAny(request, []string{"taskId", "id"}, &taskID); err != nil {
			return nil, err
		}
		task, workerID, err := s.service.InterruptTask(ctx, taskID)
		if err == nil {
			_ = s.gateway.SendTaskInterrupt(workerID, taskID)
		}
		return map[string]any{"interruptTask": task}, err
	case strings.Contains(query, "taskLogs"):
		var taskID string
		if err := decodeVar(request, "taskId", &taskID); err != nil {
			return nil, err
		}
		logs, err := s.service.Store().TaskLogs(ctx, taskID)
		return map[string]any{"taskLogs": logs}, err
	case strings.Contains(query, "taskConversations"):
		var taskID string
		if err := decodeVar(request, "taskId", &taskID); err != nil {
			return nil, err
		}
		messages, err := s.service.Store().TaskConversations(ctx, taskID)
		return map[string]any{"taskConversations": messages}, err
	case strings.Contains(query, "taskEvents"):
		var taskID string
		if err := decodeVar(request, "taskId", &taskID); err != nil {
			return nil, err
		}
		events, err := s.service.DomainEvents(ctx, domain.EventFilter{AggregateID: taskID})
		return map[string]any{"taskEvents": events}, err
	case strings.Contains(query, "domainEvents"):
		filter, err := decodeEventFilter(request)
		if err != nil {
			return nil, err
		}
		events, err := s.service.DomainEvents(ctx, filter)
		return map[string]any{"domainEvents": events}, err
	case strings.Contains(query, "outboxMessages"):
		includePublished := true
		if raw, ok := request.Variables["includePublished"]; ok {
			if err := json.Unmarshal(raw, &includePublished); err != nil {
				return nil, err
			}
		}
		messages, err := s.service.OutboxMessages(ctx, includePublished)
		return map[string]any{"outboxMessages": messages}, err
	case strings.Contains(query, "task("):
		var id string
		if err := decodeVar(request, "id", &id); err != nil {
			return nil, err
		}
		task, err := s.service.Task(ctx, id)
		return map[string]any{"task": task}, err
	case strings.Contains(query, "tasks"):
		tasks, err := s.service.Tasks(ctx)
		return map[string]any{"tasks": tasks}, err
	case strings.Contains(query, "updateAgentRuntimeEnvVars"):
		var input struct {
			Vars []domain.AgentRuntimeEnvVar `json:"vars"`
		}
		if err := decodeVar(request, "input", &input); err != nil {
			return nil, err
		}
		settings, err := s.service.UpdateAgentRuntimeEnvVars(ctx, input.Vars)
		if settings != nil {
			settings.AgentRuntimeEnvVars = settings.MaskedEnvVars()
		}
		return map[string]any{"updateAgentRuntimeEnvVars": settings}, err
	case strings.Contains(query, "settings"):
		settings, err := s.service.Settings(ctx)
		if settings != nil {
			settings.AgentRuntimeEnvVars = settings.MaskedEnvVars()
		}
		return map[string]any{"settings": settings}, err
	default:
		return nil, errors.New("unsupported GraphQL operation")
	}
}

func (s *Server) subscriptions(w http.ResponseWriter, r *http.Request) {
	conn, err := defaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	filter := domain.EventFilter{
		AggregateID:   r.URL.Query().Get("aggregateId"),
		AggregateType: r.URL.Query().Get("aggregateType"),
		EventType:     r.URL.Query().Get("eventType"),
	}
	events, unsubscribe := s.service.SubscribeDomainEvents(r.Context(), filter)
	defer unsubscribe()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := conn.WriteJSON(map[string]any{"type": "DOMAIN_EVENT", "event": event}); err != nil {
				return
			}
		case now := <-ticker.C:
			if err := conn.WriteJSON(map[string]any{"type": "KEEPALIVE", "timestamp": now}); err != nil {
				return
			}
		}
	}
}

func decodeVar(request graphQLRequest, name string, out any) error {
	raw, ok := request.Variables[name]
	if !ok {
		return errors.New("missing variable " + name)
	}
	return json.Unmarshal(raw, out)
}

func decodeVarAny(request graphQLRequest, names []string, out any) error {
	for _, name := range names {
		if raw, ok := request.Variables[name]; ok {
			return json.Unmarshal(raw, out)
		}
	}
	return errors.New("missing variable " + strings.Join(names, " or "))
}

func decodeEventFilter(request graphQLRequest) (domain.EventFilter, error) {
	var filter domain.EventFilter
	if raw, ok := request.Variables["filter"]; ok {
		if err := json.Unmarshal(raw, &filter); err != nil {
			return filter, err
		}
	}
	if raw, ok := request.Variables["aggregateId"]; ok {
		if err := json.Unmarshal(raw, &filter.AggregateID); err != nil {
			return filter, err
		}
	}
	if raw, ok := request.Variables["aggregateType"]; ok {
		if err := json.Unmarshal(raw, &filter.AggregateType); err != nil {
			return filter, err
		}
	}
	if raw, ok := request.Variables["eventType"]; ok {
		if err := json.Unmarshal(raw, &filter.EventType); err != nil {
			return filter, err
		}
	}
	return filter, nil
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

func (g *WorkerGateway) send(workerID string, envelope protocol.Envelope) error {
	g.mu.RLock()
	conn := g.connections[workerID]
	g.mu.RUnlock()
	if conn == nil {
		return nil
	}
	return conn.writeJSON(envelope)
}
