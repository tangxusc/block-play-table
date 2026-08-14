// Package client 提供 Worker 到单个 Manager 的注册、心跳和 FRP 控制连接。
package client

import (
	"context"
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
)

// Config 描述 Worker 控制连接所需的 Manager 地址、身份、能力和心跳配置。
// 创建 Client 时传入该配置；ManagerWSURL 和 WorkerID 由调用方负责提供。
type Config struct {
	ManagerWSURL    string
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
}

// Client 负责维护一个 Worker 与一个 Manager 之间的控制连接和 FRP 隧道。
// Client 可通过 New 创建，并由 Run 阻塞运行到上下文取消或发生不可恢复错误。
type Client struct {
	config Config
	logger *slog.Logger

	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn
}

// New 创建单 Manager Worker 客户端。
// 参数：config 提供连接地址、Worker 身份、能力和心跳周期。
// 返回：已应用默认值的 Client；该函数不会建立网络连接。
// 错误：本函数不返回错误。
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
	return &Client{config: config, logger: config.Logger}
}

// Run 持续维护 Worker 注册、心跳和 FRP 连接。
// 参数：ctx 控制客户端完整生命周期。
// 返回：连接正常结束时返回 nil。
// 错误：context 取消、Manager 地址校验或连接循环不可恢复时返回错误。
func (c *Client) Run(ctx context.Context) error {
	if strings.TrimSpace(c.config.ManagerWSURL) == "" {
		return errors.New("manager websocket url is required")
	}
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
		case protocol.MessagePing:
			if err := c.send(ctx, protocol.Envelope{MessageID: "msg_" + uuid.NewString(), Type: protocol.MessageWorkerHeartbeat, WorkerID: c.config.WorkerID, Timestamp: time.Now().UTC()}); err != nil {
				return err
			}
		}
	}
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
}

// ParseProjectBindingMode 解析 Worker 的项目绑定模式。
// 参数：value 是项目绑定模式文本。
// 返回：显式的指定项目模式，其他输入返回全部项目模式。
// 错误：本函数不返回错误。
func ParseProjectBindingMode(value string) domain.WorkerProjectBindingMode {
	switch domain.WorkerProjectBindingMode(strings.TrimSpace(value)) {
	case domain.WorkerSpecificProjects:
		return domain.WorkerSpecificProjects
	default:
		return domain.WorkerAllProjects
	}
}

// ParseCSV 解析逗号分隔文本并丢弃空白项。
// 参数：value 是配置文件或环境变量中的逗号分隔文本。
// 返回：保持输入顺序的非空字符串。
// 错误：本函数不返回错误。
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
