package a2aadapter

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

const authenticationOutputWindowBytes = 64 * 1024

// ErrEventTooLarge 表示 CLI 输出了超过 execution v1 上限的单条事件。
var ErrEventTooLarge = errors.New("Agent 单条事件超过协议上限")

// ErrAuthRequired 表示 CLI 未登录、凭据过期或服务端拒绝认证。
var ErrAuthRequired = errors.New("Agent CLI 需要认证")

// ErrInteractionCanceled 表示用户通过交互回复明确取消当前 execution。
var ErrInteractionCanceled = errors.New("用户取消 Agent 交互")

// EventType 表示 Adapter 向 TaskRuntime 上报的规范化事件类型。
type EventType string

const (
	// EventStdout 表示 Agent 标准输出。
	EventStdout EventType = "stdout"
	// EventStderr 表示 Agent 标准错误。
	EventStderr EventType = "stderr"
	// EventConversation 表示 Agent 的规范化助手消息。
	EventConversation EventType = "conversation"
	// EventCompleted 表示 Agent 成功结束并携带最终结果。
	EventCompleted EventType = "completed"
	// EventFailed 表示 Agent 主动报告失败。
	EventFailed EventType = "failed"
)

// Event 是 CLI 协议与 TaskRuntime 之间的稳定事件。
type Event struct {
	Type           EventType
	Content        string
	AgentSessionID string
	Metadata       map[string]string
}

// InteractionRequest 描述 CLI 在运行中发起的用户交互。
type InteractionRequest struct {
	ID             string
	Kind           a2aext.InteractionKind
	Title          string
	Body           string
	RawPayload     string
	AgentSessionID string
}

// InteractionResponse 描述 Manager 对 CLI 交互请求的回复。
type InteractionResponse struct {
	ID       string
	Decision a2aext.InteractionDecision
	Message  string
	Payload  string
}

// Input 保存一次 Adapter turn 所需且与传输无关的输入。
type Input struct {
	Request            *a2aext.ExecutionRequest
	Message            string
	WorktreePath       string
	AgentSessionID     string
	Environment        map[string]string
	RequestInteraction func(context.Context, InteractionRequest) (InteractionResponse, error)
}

// Info 保存 CLI readiness 探测结果。
type Info struct {
	Binary  string
	Version string
}

// Adapter 定义 Codex 与 Claude Code 的统一 turn 执行接口。
type Adapter interface {
	// Probe 检查 CLI 是否存在且能够返回版本。
	// 参数：ctx 控制探测时限。
	// 返回：解析后的可执行文件路径与版本文本。
	// 错误：CLI 缺失、启动失败或版本为空时返回错误。
	Probe(context.Context) (Info, error)
	// Run 执行首次、重试或续接 turn，并同步回调规范化事件。
	// 参数：ctx 控制底层进程组，input 提供 execution 请求与运行时绑定，emit 接收事件。
	// 返回：turn 正常结束时返回 nil。
	// 错误：输入非法、CLI 协议、交互或进程执行失败时返回错误。
	Run(context.Context, Input, func(Event)) error
}

type cliAgent interface {
	run(context.Context, Input, func(Event)) error
}

type cliAdapter struct {
	binary string
	agent  cliAgent
}

// NewCodex 创建使用 `codex app-server --listen stdio://` 的 Adapter。
// 参数：binary 是 Codex CLI 路径或名称；空值使用 codex。
// 返回：可执行 Codex turn 的 Adapter。
// 错误：本函数不返回错误；readiness 由 Probe 返回。
func NewCodex(binary string) Adapter {
	binary = defaultBinary(binary, "codex")
	return &cliAdapter{binary: binary, agent: &codexAppServerAgent{binary: binary}}
}

// NewClaude 创建使用 Claude stream-json 协议的 Adapter。
// 参数：binary 是 Claude CLI 路径或名称；空值使用 claude。
// 返回：可执行 Claude turn 的 Adapter。
// 错误：本函数不返回错误；readiness 由 Probe 返回。
func NewClaude(binary string) Adapter {
	binary = defaultBinary(binary, "claude")
	return &cliAdapter{binary: binary, agent: newClaudeAgent(binary, nil)}
}

func defaultBinary(binary, fallback string) string {
	if strings.TrimSpace(binary) == "" {
		return fallback
	}
	return binary
}

// Probe 检查配置的 Agent CLI 是否存在并读取版本。
// 参数：ctx 控制探测进程的生命周期。
// 返回：解析后的 CLI 路径和版本信息。
// 错误：context 为空、CLI 不存在、执行失败或版本为空时返回错误。
func (a *cliAdapter) Probe(ctx context.Context) (Info, error) {
	if ctx == nil {
		return Info{}, errors.New("Adapter Probe context 不能为空")
	}
	path, err := exec.LookPath(a.binary)
	if err != nil {
		return Info{}, fmt.Errorf("查找 Agent CLI %s: %w", a.binary, err)
	}
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return Info{}, fmt.Errorf("探测 Agent CLI %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return Info{}, fmt.Errorf("Agent CLI %s 未返回版本", path)
	}
	return Info{Binary: path, Version: version}, nil
}

// Run 校验输入并执行一次 Agent turn，同时规范化认证错误。
// 参数：ctx 控制进程组，input 提供 execution 请求和运行环境，emit 串行接收规范化事件。
// 返回：Agent 正常结束时返回 nil。
// 错误：输入、请求校验、CLI 协议、认证、交互或进程执行失败时返回错误。
func (a *cliAdapter) Run(ctx context.Context, input Input, emit func(Event)) error {
	if ctx == nil || input.Request == nil || emit == nil {
		return errors.New("Adapter Run 缺少 context、request 或 emit")
	}
	if err := a2aext.ValidateRequest(input.Request, ""); err != nil {
		return err
	}
	if strings.TrimSpace(input.WorktreePath) == "" {
		return errors.New("Adapter Run 缺少 worktreePath")
	}
	authenticationOutput := newBoundedTextWindow(authenticationOutputWindowBytes)
	authenticationDetected := false
	var emitMu sync.Mutex
	capturingEmit := func(event Event) {
		emitMu.Lock()
		defer emitMu.Unlock()
		if event.Type == EventStdout || event.Type == EventStderr {
			authenticationOutput.Append(event.Content + "\n")
			authenticationDetected = authenticationDetected || authenticationRequired(authenticationOutput.String())
		}
		emit(event)
	}
	err := a.agent.run(ctx, input, capturingEmit)
	if errors.Is(err, ErrEventTooLarge) {
		return err
	}
	if err != nil && (authenticationDetected || authenticationRequired(authenticationOutput.String()+"\n"+err.Error())) {
		return fmt.Errorf("%w: %v", ErrAuthRequired, err)
	}
	return err
}

type boundedTextWindow struct {
	limit int
	data  []byte
}

func newBoundedTextWindow(limit int) *boundedTextWindow {
	return &boundedTextWindow{limit: limit, data: make([]byte, 0, limit)}
}

func (w *boundedTextWindow) Append(value string) {
	if w == nil || w.limit <= 0 || value == "" {
		return
	}
	if len(value) >= w.limit {
		w.data = append(w.data[:0], value[len(value)-w.limit:]...)
		return
	}
	overflow := len(w.data) + len(value) - w.limit
	if overflow > 0 {
		copy(w.data, w.data[overflow:])
		w.data = w.data[:len(w.data)-overflow]
	}
	w.data = append(w.data, value...)
}

func (w *boundedTextWindow) String() string {
	if w == nil {
		return ""
	}
	return string(w.data)
}

func authenticationRequired(output string) bool {
	normalized := strings.ToLower(output)
	for _, marker := range []string{
		"not logged in", "please run codex login", "please run /login", "authentication required",
		"authentication_error", "invalid api key", "invalid x-api-key", "unauthorized", "http 401", "status 401",
		"token has expired", "credentials have expired",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func promptForRequest(request *a2aext.ExecutionRequest) string {
	prompt := strings.TrimSpace(request.Task.Description)
	if prompt == "" {
		prompt = strings.TrimSpace(request.Task.Title)
	}
	if prompt == "" {
		prompt = "Complete task " + request.Scope.LocalTaskID
	}
	if prefix := workModePromptPrefix(request.Agent.WorkMode); prefix != "" {
		prompt = prefix + "\n\n" + prompt
	}
	return prompt
}

func workModePromptPrefix(mode a2aext.WorkMode) string {
	switch mode {
	case a2aext.WorkModePlan:
		return "Work mode: plan. Analyze the task and produce a concrete implementation plan before making changes."
	case a2aext.WorkModeImplement:
		return "Work mode: implement. Complete the requested implementation and verify the result."
	case a2aext.WorkModeReview:
		return "Work mode: review. Inspect the relevant code and report findings with evidence."
	default:
		return ""
	}
}
