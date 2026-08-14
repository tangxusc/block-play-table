package a2aserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

const (
	// EndpointPath 是 Worker A2A JSON-RPC/SSE 的固定路径。
	EndpointPath = "/a2a"
	// KeepAliveInterval 是流式响应发送 SSE comment 的固定间隔。
	KeepAliveInterval = 15 * time.Second
	maxRequestBytes   = 32 * 1024 * 1024
)

// Config 描述 Worker A2A 服务的身份、监听地址和 SDK 依赖。
type Config struct {
	Host          string
	Port          int
	WorkerID      string
	WorkerToken   string
	AgentTypes    []a2aext.AgentType
	AgentExecutor a2asrv.AgentExecutor
	TaskStore     *a2astore.Store
	Logger        *slog.Logger
}

// Server 管理 loopback A2A HTTP 服务及其公开发现信息。
type Server struct {
	config Config

	mu       sync.RWMutex
	listener net.Listener
	http     *http.Server
	card     *a2a.AgentCard
	errCh    chan error
}

// New 创建尚未监听的 Worker A2A Server。
// 参数：config 提供 Worker 身份、鉴权、执行器和 TaskStore。
// 返回：可调用 Start 的 Server。
// 错误：本函数不返回错误；配置在 Start 时统一校验。
func New(config Config) *Server {
	if strings.TrimSpace(config.Host) == "" {
		config.Host = "127.0.0.1"
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if len(config.AgentTypes) == 0 {
		config.AgentTypes = []a2aext.AgentType{a2aext.AgentCodex, a2aext.AgentClaude}
	}
	return &Server{config: config, errCh: make(chan error, 1)}
}

// Start 在 loopback 地址启动 Agent Card 与 JSON-RPC/SSE 服务。
// 参数：ctx 是 Worker 生命周期；取消后服务会优雅关闭。
// 返回：监听器建立且路由就绪后返回 nil。
// 错误：配置非法、非 loopback 地址、监听或 Agent Card 构造失败时返回错误。
func (s *Server) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("A2A Server 不能为空")
	}
	if ctx == nil {
		return errors.New("A2A Server context 不能为空")
	}
	if strings.TrimSpace(s.config.WorkerID) == "" {
		return errors.New("A2A Worker ID 不能为空")
	}
	if s.config.AgentExecutor == nil || s.config.TaskStore == nil {
		return errors.New("A2A AgentExecutor 和 TaskStore 不能为空")
	}
	seenAgentTypes := map[a2aext.AgentType]bool{}
	for _, agentType := range s.config.AgentTypes {
		if !agentType.Valid() || seenAgentTypes[agentType] {
			return fmt.Errorf("A2A Agent 类型非法或重复: %q", agentType)
		}
		seenAgentTypes[agentType] = true
	}
	if err := validateLoopbackHost(s.config.Host); err != nil {
		return err
	}
	if s.config.Port < 0 || s.config.Port > 65535 {
		return errors.New("A2A 监听端口必须在 0 到 65535 之间")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return errors.New("A2A Server 已启动")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port)))
	if err != nil {
		return fmt.Errorf("监听 A2A loopback 地址: %w", err)
	}
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("解析 A2A 监听地址: %w", err)
	}
	endpointURL := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: EndpointPath}).String()
	card := buildAgentCard(s.config, endpointURL)
	capabilities := card.Capabilities
	auth := &bearerInterceptor{token: s.config.WorkerToken, principal: "manager@" + s.config.WorkerID}
	requestHandler := a2asrv.NewHandler(
		s.config.AgentExecutor,
		a2asrv.WithTaskStore(s.config.TaskStore),
		a2asrv.WithCapabilityChecks(&capabilities),
		a2asrv.WithCallInterceptors(auth),
		a2asrv.WithLogger(s.config.Logger),
	)
	intercepted, ok := requestHandler.(*a2asrv.InterceptedHandler)
	if !ok {
		_ = listener.Close()
		return errors.New("A2A SDK handler 类型不受支持")
	}
	intercepted.Handler = &idempotentHandler{
		next:     intercepted.Handler,
		store:    s.config.TaskStore,
		workerID: s.config.WorkerID,
	}
	jsonRPC := a2asrv.NewJSONRPCHandler(requestHandler, a2asrv.WithTransportKeepAlive(KeepAliveInterval))
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle(EndpointPath, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(response, request.Body, maxRequestBytes)
		jsonRPC.ServeHTTP(response, request)
	}))
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	s.listener, s.http, s.card = listener, httpServer, card
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case s.errCh <- err:
			default:
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			s.config.Logger.Warn("A2A Server 关闭失败", "error", err)
		}
	}()
	return nil
}

// Close 立即关闭 A2A 监听器和活动连接。
// 参数：无。
// 返回：关闭成功返回 nil。
// 错误：底层 HTTP Server 关闭失败时返回错误。
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	httpServer := s.http
	s.mu.RUnlock()
	if httpServer == nil {
		return nil
	}
	return httpServer.Close()
}

// Errors 返回异步 Serve 错误通道。
// 参数：无。
// 返回：只在 HTTP Serve 异常退出时产生错误的只读通道。
// 错误：本方法不返回错误。
func (s *Server) Errors() <-chan error {
	if s == nil {
		return nil
	}
	return s.errCh
}

// Address 返回当前监听地址。
// 参数：无。
// 返回：host、port 和 started 标志。
// 错误：本方法不返回错误。
func (s *Server) Address() (string, int, bool) {
	if s == nil {
		return "", 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return "", 0, false
	}
	host, portText, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, false
	}
	return host, port, true
}

// Card 返回当前公开 Agent Card 的只读快照。
// 参数：无。
// 返回：服务未启动时返回 nil，否则返回独立 JSON 深拷贝。
// 错误：内部拷贝失败时返回 nil。
func (s *Server) Card() *a2a.AgentCard {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.card == nil {
		return nil
	}
	raw, err := json.Marshal(s.card)
	if err != nil {
		return nil
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(raw, &card); err != nil {
		return nil
	}
	return &card
}

// Capabilities 返回注册给 Manager 的 A2A 发现参数。
// 参数：无。
// 返回：包含协议、transport、地址、路径和 required extension 的新 map。
// 错误：服务未启动时返回空 map。
func (s *Server) Capabilities() map[string]string {
	host, port, ok := s.Address()
	if !ok {
		return map[string]string{}
	}
	return map[string]string{
		"a2a_version":       string(a2a.Version),
		"a2a_transport":     string(a2a.TransportProtocolJSONRPC),
		"a2a_host":          host,
		"a2a_port":          strconv.Itoa(port),
		"a2a_card_path":     a2asrv.WellKnownAgentCardPath,
		"a2a_endpoint_path": EndpointPath,
		"a2a_extension":     a2aext.ExtensionURI,
		"a2a_agents":        agentTypeList(s.config.AgentTypes),
	}
}

type bearerInterceptor struct {
	a2asrv.PassthroughCallInterceptor
	token     string
	principal string
}

// Before 校验 Bearer 凭据并为 SDK 调用设置固定认证主体。
// 参数：ctx 是原请求上下文，callCtx 提供请求头与认证主体槽位；请求体无需参与鉴权。
// 返回：认证成功时返回原 context 和空状态。
// 错误：调用上下文、主体或 Bearer 凭据非法时返回未认证错误。
func (i *bearerInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	if callCtx == nil || strings.TrimSpace(i.principal) == "" {
		return ctx, nil, a2a.ErrUnauthenticated
	}
	if i.token != "" {
		values, ok := callCtx.ServiceParams().Get("authorization")
		if !ok || len(values) != 1 {
			return ctx, nil, a2a.ErrUnauthenticated
		}
		want := "Bearer " + i.token
		if len(values[0]) != len(want) || subtle.ConstantTimeCompare([]byte(values[0]), []byte(want)) != 1 {
			return ctx, nil, a2a.ErrUnauthenticated
		}
	}
	callCtx.User = a2asrv.NewAuthenticatedUser(i.principal, map[string]any{"trustedLocal": i.token == ""})
	return ctx, nil, nil
}

func buildAgentCard(config Config, endpointURL string) *a2a.AgentCard {
	agentNames := make([]string, 0, len(config.AgentTypes))
	for _, agentType := range config.AgentTypes {
		agentNames = append(agentNames, string(agentType))
	}
	// Agent Card 与 Worker readiness 都固定要求双 Adapter，避免发现能力和实际调度能力分裂。
	skills := make([]a2a.AgentSkill, 0, 2)
	for _, agentType := range []a2aext.AgentType{a2aext.AgentCodex, a2aext.AgentClaude} {
		skills = append(skills, a2a.AgentSkill{
			ID:          "block-play-table-" + string(agentType),
			Name:        "Execute with " + string(agentType),
			Description: "Runs, continues, cancels and resumes a coding task with the " + string(agentType) + " adapter",
			Tags:        []string{"coding", "execution", string(agentType)},
		})
	}
	extension := a2a.AgentExtension{
		URI: a2aext.ExtensionURI, Required: true,
		Description: "Block Play Table execution request, event and Artifact contract",
		Params: map[string]any{
			"version": a2aext.Version,
			"agents":  agentNames,
		},
	}
	card := &a2a.AgentCard{
		Name:        "Block Play Table Worker " + config.WorkerID,
		Description: "Executes Block Play Table tasks with Codex and Claude Code adapters",
		Version:     a2aext.Version,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(endpointURL, a2a.TransportProtocolJSONRPC),
		},
		Capabilities:       a2a.AgentCapabilities{Streaming: true, Extensions: []a2a.AgentExtension{extension}},
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain", "application/json", "application/octet-stream"},
		Skills:             skills,
	}
	if config.WorkerToken != "" {
		const schemeName a2a.SecuritySchemeName = "workerBearer"
		card.SecuritySchemes = a2a.NamedSecuritySchemes{
			schemeName: a2a.HTTPAuthSecurityScheme{Scheme: "Bearer", BearerFormat: "opaque"},
		}
		card.SecurityRequirements = a2a.SecurityRequirementsOptions{
			{schemeName: a2a.SecuritySchemeScopes{}},
		}
	}
	return card
}

func agentTypeList(agentTypes []a2aext.AgentType) string {
	values := make([]string, 0, len(agentTypes))
	for _, agentType := range agentTypes {
		values = append(values, string(agentType))
	}
	return strings.Join(values, ",")
}

func validateLoopbackHost(host string) error {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return nil
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil || !ip.IsLoopback() {
		return errors.New("A2A Server 只允许监听 loopback 地址")
	}
	return nil
}
