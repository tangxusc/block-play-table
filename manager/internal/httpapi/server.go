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
	service *app.Service
	gateway *WorkerGateway
	logger  *slog.Logger
}

func NewServer(service *app.Service) *Server {
	server := &Server{
		service: service,
		logger:  slog.Default(),
	}
	server.gateway = NewWorkerGateway(service, server.logger)
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.health)
	mux.HandleFunc("/graphql", s.graphql)
	mux.HandleFunc("/worker/ws", s.gateway.Handle)
	mux.HandleFunc("/subscriptions", s.subscriptions)
	return withCORS(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
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
		_ = s.gateway.SendTaskInterrupt("", taskID)
		task, err := s.service.Task(ctx, taskID)
		return map[string]any{"interruptTask": task}, err
	case strings.Contains(query, "taskLogs"):
		var taskID string
		if err := decodeVar(request, "taskId", &taskID); err != nil {
			return nil, err
		}
		logs, err := s.service.Store().TaskLogs(ctx, taskID)
		return map[string]any{"taskLogs": logs}, err
	case strings.Contains(query, "taskEvents"):
		var taskID string
		if err := decodeVar(request, "taskId", &taskID); err != nil {
			return nil, err
		}
		events, err := s.service.Store().DomainEvents(ctx, domain.EventFilter{AggregateID: taskID})
		return map[string]any{"taskEvents": events}, err
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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
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
	service *app.Service
	logger  *slog.Logger

	mu          sync.RWMutex
	connections map[string]*websocket.Conn
}

func NewWorkerGateway(service *app.Service, logger *slog.Logger) *WorkerGateway {
	return &WorkerGateway{service: service, logger: logger, connections: map[string]*websocket.Conn{}}
}

func (g *WorkerGateway) Handle(w http.ResponseWriter, r *http.Request) {
	conn, err := defaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	workerID := r.URL.Query().Get("worker_id")
	defer func() {
		if workerID != "" {
			g.mu.Lock()
			delete(g.connections, workerID)
			g.mu.Unlock()
		}
		_ = conn.Close()
	}()
	for {
		var envelope rawEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return
		}
		if envelope.WorkerID != "" {
			workerID = envelope.WorkerID
		}
		if workerID != "" {
			g.mu.Lock()
			g.connections[workerID] = conn
			g.mu.Unlock()
		}
		if err := g.apply(r.Context(), envelope, workerID); err != nil {
			_ = conn.WriteJSON(protocol.Envelope{MessageID: "msg_" + uuid.NewString(), Type: protocol.MessageTaskFailed, WorkerID: workerID, Timestamp: time.Now().UTC(), Payload: map[string]string{"error": err.Error()}})
		}
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
		_, err := g.service.ApplyWorkerTaskStarted(ctx, envelope.MessageID, envelope.TaskID, worktree)
		return err
	case protocol.MessageTaskLog:
		var event protocol.WorkerEvent
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			return err
		}
		_, err := g.service.ApplyWorkerTaskLog(ctx, envelope.MessageID, envelope.TaskID, event.Stream, event.Content)
		return err
	case protocol.MessageTaskConversation:
		var event protocol.WorkerEvent
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			return err
		}
		_, err := g.service.ApplyWorkerConversation(ctx, envelope.MessageID, envelope.TaskID, "assistant", event.Content)
		return err
	case protocol.MessageTaskCompleted:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		_, err := g.service.ApplyWorkerTaskCompleted(ctx, envelope.MessageID, envelope.TaskID, event.Result)
		return err
	case protocol.MessageTaskFailed:
		var event protocol.WorkerEvent
		_ = json.Unmarshal(envelope.Payload, &event)
		_, err := g.service.ApplyWorkerTaskFailed(ctx, envelope.MessageID, envelope.TaskID, event.Result)
		return err
	default:
		return nil
	}
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
	return conn.WriteJSON(envelope)
}
