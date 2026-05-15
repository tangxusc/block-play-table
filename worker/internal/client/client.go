package client

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/frp"
	"github.com/tangxusc/block-play-table/pkg/protocol"
	"github.com/tangxusc/block-play-table/worker/internal/executor"
)

type Config struct {
	ManagerWSURL    string
	ManagerWSURLs   []string
	WorkerID        string
	WorkerToken     string
	Name            string
	WorkDir         string
	StartupCommand  string
	SupportedAgents []domain.AgentType
	BindingMode     domain.WorkerProjectBindingMode
	BoundProjectIDs []string
	Capabilities    map[string]string
	HeartbeatEvery  time.Duration
	Logger          *slog.Logger
	ReviewRecorder  executor.ReviewRecorder
}

type Client struct {
	config   Config
	executor *executor.Executor
	logger   *slog.Logger

	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn
}

func New(config Config) *Client {
	if config.HeartbeatEvery == 0 {
		config.HeartbeatEvery = 10 * time.Second
	}
	if config.BindingMode == "" {
		config.BindingMode = domain.WorkerAllProjects
	}
	if len(config.SupportedAgents) == 0 {
		config.SupportedAgents = []domain.AgentType{domain.AgentCodex, domain.AgentClaude}
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	c := &Client{config: config, logger: config.Logger}
	c.executor = executor.NewExecutor(executor.Config{
		WorkerID:       config.WorkerID,
		WorkDir:        config.WorkDir,
		ReviewRecorder: config.ReviewRecorder,
		Reporter: executor.ReporterFunc(func(ctx context.Context, event protocol.WorkerEvent) error {
			return c.SendWorkerEvent(ctx, event)
		}),
	})
	return c
}

func (c *Client) Run(ctx context.Context) error {
	managerURLs := c.managerWSURLs()
	if len(managerURLs) == 0 {
		return errors.New("manager websocket url is required")
	}
	if len(managerURLs) > 1 {
		return c.runMultipleManagers(ctx, managerURLs)
	}
	c.config.ManagerWSURL = managerURLs[0]
	return c.runSingleManager(ctx)
}

func (c *Client) runSingleManager(ctx context.Context) error {
	for {
		if err := c.connectAndServe(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.logger.Warn("worker connection lost; retrying", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (c *Client) runMultipleManagers(ctx context.Context, managerURLs []string) error {
	errCh := make(chan error, len(managerURLs))
	for _, managerURL := range managerURLs {
		childConfig := c.config
		childConfig.ManagerWSURL = managerURL
		childConfig.ManagerWSURLs = nil
		if c.logger != nil {
			childConfig.Logger = c.logger.With("manager", managerURL)
		}
		go func() {
			errCh <- New(childConfig).Run(ctx)
		}()
	}
	for i := 0; i < len(managerURLs); i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) connectAndServe(ctx context.Context) error {
	wsURL, err := url.Parse(c.config.ManagerWSURL)
	if err != nil {
		return err
	}
	q := wsURL.Query()
	q.Set("worker_id", c.config.WorkerID)
	if c.config.WorkerToken != "" {
		q.Set("token", c.config.WorkerToken)
	}
	wsURL.RawQuery = q.Encode()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL.String(), nil)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		_ = conn.Close()
	}()

	if err := c.send(ctx, protocol.Envelope{
		MessageID: "msg_" + uuid.NewString(),
		Type:      protocol.MessageWorkerRegister,
		WorkerID:  c.config.WorkerID,
		Timestamp: time.Now().UTC(),
		Payload: registerPayload{
			ID:                 c.config.WorkerID,
			Name:               c.config.Name,
			SupportedAgents:    c.config.SupportedAgents,
			WorkDir:            c.config.WorkDir,
			StartupCommand:     c.config.StartupCommand,
			ProjectBindingMode: c.config.BindingMode,
			BoundProjectIDs:    c.config.BoundProjectIDs,
			Capabilities:       c.config.Capabilities,
		},
	}); err != nil {
		return err
	}

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.frpLoop(connCtx, c.config.ManagerWSURL)

	errCh := make(chan error, 2)
	go func() { errCh <- c.heartbeatLoop(connCtx) }()
	go func() { errCh <- c.readLoop(connCtx) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (c *Client) frpLoop(ctx context.Context, managerWSURL string) {
	for {
		if err := c.connectFRP(ctx, managerWSURL); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Warn("worker frp connection lost; retrying", "manager", managerWSURL, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) connectFRP(ctx context.Context, managerWSURL string) error {
	frpURL, err := c.frpURL(managerWSURL)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, frpURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	session, err := yamux.Client(frp.NewWebSocketConn(conn), nil)
	if err != nil {
		return err
	}
	defer session.Close()
	return frp.ServeWorkerProxy(ctx, session)
}

func (c *Client) frpURL(managerWSURL string) (string, error) {
	wsURL, err := url.Parse(managerWSURL)
	if err != nil {
		return "", err
	}
	if wsURL.Path == "" || wsURL.Path == "/" {
		wsURL.Path = "/worker/frp"
	} else {
		wsURL.Path = strings.TrimSuffix(wsURL.Path, "/worker/ws") + "/worker/frp"
	}
	q := wsURL.Query()
	q.Set("worker_id", c.config.WorkerID)
	q.Set("worker_name", c.config.Name)
	if c.config.WorkerToken != "" {
		q.Set("token", c.config.WorkerToken)
	}
	wsURL.RawQuery = q.Encode()
	return wsURL.String(), nil
}

func (c *Client) heartbeatLoop(ctx context.Context) error {
	ticker := time.NewTicker(c.config.HeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.send(ctx, protocol.Envelope{MessageID: "msg_" + uuid.NewString(), Type: protocol.MessageWorkerHeartbeat, WorkerID: c.config.WorkerID, Timestamp: time.Now().UTC()}); err != nil {
				return err
			}
		}
	}
}

func (c *Client) readLoop(ctx context.Context) error {
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return errors.New("websocket is not connected")
		}
		var envelope rawEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return err
		}
		switch envelope.Type {
		case protocol.MessageTaskStart:
			var payload protocol.TaskStartPayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return err
			}
			if err := c.send(ctx, protocol.Envelope{MessageID: "msg_" + uuid.NewString(), Type: protocol.MessageTaskAccepted, WorkerID: c.config.WorkerID, TaskID: envelope.TaskID, Timestamp: time.Now().UTC()}); err != nil {
				return err
			}
			go func() {
				if err := c.executor.Execute(ctx, payload); err != nil {
					c.logger.Warn("task execution finished with error", "taskId", payload.Task.ID, "error", err)
				}
			}()
		case protocol.MessageTaskContinue:
			var payload protocol.TaskContinuePayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return err
			}
			if err := c.send(ctx, protocol.Envelope{MessageID: "msg_" + uuid.NewString(), Type: protocol.MessageTaskAccepted, WorkerID: c.config.WorkerID, TaskID: envelope.TaskID, Timestamp: time.Now().UTC()}); err != nil {
				return err
			}
			go func() {
				if err := c.executor.Continue(ctx, payload); err != nil {
					c.logger.Warn("task continuation finished with error", "taskId", payload.Task.ID, "error", err)
				}
			}()
		case protocol.MessageTaskInterrupt:
			c.executor.Interrupt(envelope.TaskID)
		case protocol.MessageTaskInteractionResponse:
			var payload protocol.TaskInteractionResponsePayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return err
			}
			if payload.TaskID == "" {
				payload.TaskID = envelope.TaskID
			}
			if err := c.executor.HandleTaskInteractionResponse(ctx, payload); err != nil {
				c.logger.Warn("task interaction response was not routed", "taskId", payload.TaskID, "interactionId", payload.InteractionID, "error", err)
			}
		case protocol.MessagePing:
			_ = c.send(ctx, protocol.Envelope{MessageID: "msg_" + uuid.NewString(), Type: protocol.MessageWorkerHeartbeat, WorkerID: c.config.WorkerID, Timestamp: time.Now().UTC()})
		}
	}
}

func (c *Client) SendWorkerEvent(ctx context.Context, event protocol.WorkerEvent) error {
	if event.MessageID == "" {
		event.MessageID = "msg_" + uuid.NewString()
	}
	if event.WorkerID == "" {
		event.WorkerID = c.config.WorkerID
	}
	return c.send(ctx, protocol.Envelope{
		MessageID: event.MessageID,
		Type:      event.Type,
		WorkerID:  event.WorkerID,
		TaskID:    event.TaskID,
		Timestamp: time.Now().UTC(),
		Payload:   event,
	})
}

func (c *Client) managerWSURLs() []string {
	if len(c.config.ManagerWSURLs) > 0 {
		out := make([]string, 0, len(c.config.ManagerWSURLs))
		for _, item := range c.config.ManagerWSURLs {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	return ParseManagerWSURLs("", c.config.ManagerWSURL)
}

func (c *Client) send(ctx context.Context, envelope protocol.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("websocket is not connected")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return conn.WriteJSON(envelope)
}

type registerPayload struct {
	ID                 string                          `json:"id"`
	Name               string                          `json:"name"`
	SupportedAgents    []domain.AgentType              `json:"supportedAgents"`
	WorkDir            string                          `json:"workDir"`
	StartupCommand     string                          `json:"startupCommand,omitempty"`
	ProjectBindingMode domain.WorkerProjectBindingMode `json:"projectBindingMode"`
	BoundProjectIDs    []string                        `json:"boundProjectIds"`
	Capabilities       map[string]string               `json:"capabilities,omitempty"`
}

type rawEnvelope struct {
	MessageID string               `json:"messageId"`
	Type      protocol.MessageType `json:"type"`
	WorkerID  string               `json:"workerId,omitempty"`
	TaskID    string               `json:"taskId,omitempty"`
	Payload   json.RawMessage      `json:"payload,omitempty"`
}

func ParseAgents(value string) []domain.AgentType {
	var agents []domain.AgentType
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		agent := domain.AgentType(item)
		if agent.Valid() {
			agents = append(agents, agent)
		}
	}
	return agents
}

func ParseProjectBindingMode(value string) domain.WorkerProjectBindingMode {
	switch domain.WorkerProjectBindingMode(strings.TrimSpace(value)) {
	case domain.WorkerSpecificProjects:
		return domain.WorkerSpecificProjects
	default:
		return domain.WorkerAllProjects
	}
}

func ParseCSV(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func ParseManagerWSURLs(primary, legacy string) []string {
	if strings.TrimSpace(primary) != "" {
		return ParseCSV(primary)
	}
	return ParseCSV(legacy)
}
