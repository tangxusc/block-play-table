package a2aext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidationError 描述 execution 契约中不合法字段。
type ValidationError struct {
	Field   string
	Message string
}

// Error 返回带字段路径的校验错误文本。
// 参数：无。
// 返回：可用于日志和 A2A 错误响应的文本。
// 错误：本方法自身不返回错误。
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidateRequest 校验请求是否符合 execution v1 契约及目标 Worker 约束。
// 参数：req 为待校验请求，workerID 为当前 Worker 的稳定 ID。
// 返回：校验成功返回 nil。
// 错误：字段缺失、枚举非法、操作与 resume 不一致或目标 Worker 不匹配时返回 ValidationError。
func ValidateRequest(req *ExecutionRequest, workerID string) error {
	if req == nil {
		return invalid("request", "不能为空")
	}
	if req.Kind != RequestKind || req.Version != Version {
		return invalid("request", "kind 或 version 不受支持")
	}
	if err := identifier("command.id", req.Command.ID); err != nil {
		return err
	}
	if req.Command.IssuedAt.IsZero() {
		return invalid("command.issuedAt", "不能为空")
	}
	if !req.Command.Operation.Valid() {
		return invalid("command.operation", "不受支持")
	}
	for field, value := range map[string]string{
		"scope.localTaskId":      req.Scope.LocalTaskID,
		"scope.executionId":      req.Scope.ExecutionID,
		"scope.expectedWorkerId": req.Scope.ExpectedWorkerID,
		"project.id":             req.Project.ID,
	} {
		if err := identifier(field, value); err != nil {
			return err
		}
	}
	if workerID != "" && req.Scope.ExpectedWorkerID != workerID {
		return invalid("scope.expectedWorkerId", "与当前 Worker 不匹配")
	}
	if req.Scope.Attempt < 1 || req.Scope.Turn < 1 {
		return invalid("scope", "attempt 和 turn 必须大于零")
	}
	if err := boundedRequired("task.title", req.Task.Title, 4096); err != nil {
		return err
	}
	if err := bounded("task.description", req.Task.Description, 16*1024*1024); err != nil {
		return err
	}
	if err := boundedRequired("task.baseBranch", req.Task.BaseBranch, 1024); err != nil {
		return err
	}
	if err := validateAgent(req.Agent); err != nil {
		return err
	}
	if err := validateProject(req.Project); err != nil {
		return err
	}
	if err := validateCommands(req.Commands); err != nil {
		return err
	}
	if err := validateEnvironment(req.Environment); err != nil {
		return err
	}
	return validateOperation(req)
}

// ValidateEvent 校验 Worker 事件是否符合 execution v1 契约。
// 参数：event 为待校验事件。
// 返回：校验成功返回 nil。
// 错误：固定字段、身份、序号、类型、时间或 runtime 字段非法时返回 ValidationError。
func ValidateEvent(event *ExecutionEvent) error {
	if event == nil {
		return invalid("event", "不能为空")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return invalid("event", "必须能编码为 JSON")
	}
	if len(raw) > MaxAgentEventBytes {
		return invalid("event", fmt.Sprintf("JSON 大小不能超过 %d 字节", MaxAgentEventBytes))
	}
	if event.Kind != EventKind || event.Version != Version {
		return invalid("event", "kind 或 version 不受支持")
	}
	if err := uuidV7("event.id", event.Event.ID); err != nil {
		return err
	}
	for field, value := range map[string]string{
		"scope.localTaskId": event.Scope.LocalTaskID,
		"scope.executionId": event.Scope.ExecutionID,
		"scope.workerId":    event.Scope.WorkerID,
	} {
		if err := identifier(field, value); err != nil {
			return err
		}
	}
	if event.Event.Sequence < 1 || event.Scope.Attempt < 1 || event.Scope.Turn < 1 {
		return invalid("event", "sequence、attempt 和 turn 必须大于零")
	}
	if !event.Event.Type.Valid() || event.Event.OccurredAt.IsZero() {
		return invalid("event", "type 或 occurredAt 非法")
	}
	if event.Payload == nil {
		return invalid("payload", "不能为空")
	}
	if event.Runtime != nil {
		if err := bounded("runtime.agentSessionId", event.Runtime.AgentSessionID, 256); err != nil {
			return err
		}
		if err := bounded("runtime.worktreePath", event.Runtime.WorktreePath, 8192); err != nil {
			return err
		}
		if err := bounded("runtime.branch", event.Runtime.Branch, 1024); err != nil {
			return err
		}
		if err := bounded("runtime.head", event.Runtime.Head, 256); err != nil {
			return err
		}
	}
	return validateEventPayload(event)
}

// ValidateArtifact 校验 execution Artifact metadata。
// 参数：metadata 为待校验 metadata。
// 返回：校验成功返回 nil。
// 错误：固定字段、role、序号或日志专属字段非法时返回 ValidationError。
func ValidateArtifact(metadata *ArtifactMetadata) error {
	if metadata == nil {
		return invalid("artifact", "不能为空")
	}
	if metadata.Kind != ArtifactKind || metadata.Version != Version || !metadata.Role.Valid() {
		return invalid("artifact", "kind、version 或 role 不受支持")
	}
	if err := identifier("executionId", metadata.ExecutionID); err != nil {
		return err
	}
	if err := uuidV7("eventId", metadata.EventID); err != nil {
		return err
	}
	if metadata.Attempt < 1 || metadata.Turn < 1 || metadata.Sequence < 1 || metadata.CreatedAt.IsZero() {
		return invalid("artifact", "attempt、turn、sequence 和 createdAt 必须有效")
	}
	if metadata.Role == ArtifactLog {
		if !metadata.Stream.Valid() || metadata.ChunkIndex < 0 {
			return invalid("artifact", "日志 Artifact 必须包含合法 stream 和 chunkIndex")
		}
	} else if metadata.Stream != "" {
		return invalid("stream", "仅日志 Artifact 可以设置")
	}
	return bounded("mimeType", metadata.MIMEType, 1024)
}

// SanitizeRequest 返回可安全持久化的请求副本。
// 参数：req 为原始请求。
// 返回：敏感变量值已替换为固定占位符的深拷贝。
// 错误：请求无法进行 JSON 深拷贝时返回编码或解码错误。
func SanitizeRequest(req *ExecutionRequest) (*ExecutionRequest, error) {
	if req == nil {
		return nil, invalid("request", "不能为空")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("编码请求副本: %w", err)
	}
	var copy ExecutionRequest
	if err := json.Unmarshal(raw, &copy); err != nil {
		return nil, fmt.Errorf("解码请求副本: %w", err)
	}
	for i := range copy.Environment.Variables {
		if copy.Environment.Variables[i].Sensitive {
			copy.Environment.Variables[i].Value = SensitivePlaceholder
		}
	}
	return &copy, nil
}

// CanonicalPayloadHash 计算不包含敏感原值的稳定请求哈希。
// 参数：req 为待计算请求。
// 返回：小写十六进制 SHA-256 哈希。
// 错误：请求为空或无法规范化编码时返回错误。
func CanonicalPayloadHash(req *ExecutionRequest) (string, error) {
	copy, err := SanitizeRequest(req)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("编码规范化请求: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// NormalizeRequestDefaults 补齐兼容旧任务所需的非安全敏感默认值。
// 参数：req 为待规范化请求，函数会直接修改该对象。
// 返回：传入的请求，便于构造流程链式调用。
// 错误：本函数不返回错误；空请求原样返回。
func NormalizeRequestDefaults(req *ExecutionRequest) *ExecutionRequest {
	if req != nil && req.Agent.WorkMode == "" {
		req.Agent.WorkMode = WorkModeImplement
	}
	return req
}

// SensitiveValues 提取本轮需要内存脱敏的非空敏感值。
// 参数：req 为执行请求。
// 返回：去重后的敏感值列表，按长度从长到短排列。
// 错误：本函数不返回错误；空请求返回空列表。
func SensitiveValues(req *ExecutionRequest) []string {
	if req == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, variable := range req.Environment.Variables {
		if variable.Sensitive && variable.Value != "" && variable.Value != SensitivePlaceholder {
			seen[variable.Value] = struct{}{}
		}
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sortSecrets(values)
	return values
}

// ValidateWorktreePath 校验 worktree 真实路径位于 Worker 工作目录之内。
// 参数：workDir 为 Worker 根目录，candidate 为待恢复路径。
// 返回：校验成功时返回已解析符号链接的绝对路径。
// 错误：路径为空、无法解析、包含悬空符号链接或逃逸工作目录时返回错误。
func ValidateWorktreePath(workDir, candidate string) (string, error) {
	if strings.TrimSpace(workDir) == "" || strings.TrimSpace(candidate) == "" {
		return "", errors.New("工作目录和 worktree 路径不能为空")
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("解析 Worker 工作目录: %w", err)
	}
	root, err = resolveWorktreePath(root)
	if err != nil {
		return "", fmt.Errorf("解析 Worker 工作目录真实路径: %w", err)
	}
	path, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("解析 worktree 路径: %w", err)
	}
	path, err = resolveWorktreePath(path)
	if err != nil {
		return "", fmt.Errorf("解析 worktree 真实路径: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("比较 worktree 路径: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("worktree 路径必须位于 Worker 工作目录的子目录")
	}
	return path, nil
}

func resolveWorktreePath(path string) (string, error) {
	current := path
	missing := make([]string, 0, 2)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("路径包含悬空符号链接: %s", current)
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// Valid 报告 operation 是否属于 execution v1 闭集。
// 参数：接收者为待校验 operation。
// 返回：属于闭集返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (o Operation) Valid() bool {
	return o == OperationStart || o == OperationRetry || o == OperationContinue || o == OperationInteractionResponse
}

// Valid 报告 Agent 类型是否属于 execution v1 闭集。
// 参数：接收者为待校验 Agent 类型。
// 返回：Codex 或 Claude 返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (a AgentType) Valid() bool {
	return a == AgentCodex || a == AgentClaude
}

// Valid 报告事件类型是否属于 execution v1 闭集。
// 参数：接收者为待校验事件类型。
// 返回：属于闭集返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (t EventType) Valid() bool {
	switch t {
	case EventExecutionAccepted, EventWorkspaceReady, EventAgentSessionStarted, EventAgentSessionUpdated,
		EventLogChunk, EventConversationMessage, EventInteractionRequested, EventInteractionResolved,
		EventResultUpdated, EventExecutionDiagnostic, EventExecutionTerminal:
		return true
	default:
		return false
	}
}

// Valid 报告 Artifact role 是否属于 execution v1 闭集。
// 参数：接收者为待校验 Artifact role。
// 返回：属于闭集返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (r ArtifactRole) Valid() bool {
	switch r {
	case ArtifactManifest, ArtifactLog, ArtifactConversation, ArtifactInteraction, ArtifactResult, ArtifactOutput, ArtifactDiagnostic:
		return true
	default:
		return false
	}
}

// Valid 报告日志来源是否属于 execution v1 闭集。
// 参数：接收者为待校验日志来源。
// 返回：属于闭集返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (s LogStream) Valid() bool {
	return s == LogStdout || s == LogStderr || s == LogSystem
}

func validateOperation(req *ExecutionRequest) error {
	wantMode := map[Operation]WorktreeMode{
		OperationStart: WorktreeCreate, OperationRetry: WorktreeRecreate,
		OperationContinue: WorktreeResume, OperationInteractionResponse: WorktreeResume,
	}[req.Command.Operation]
	if req.Worktree.Mode != wantMode {
		return invalid("worktree.mode", fmt.Sprintf("操作 %s 要求 %s", req.Command.Operation, wantMode))
	}
	needsResume := req.Command.Operation == OperationContinue || req.Command.Operation == OperationInteractionResponse
	if needsResume != (req.Resume != nil) {
		return invalid("resume", "与操作不匹配")
	}
	if req.Resume != nil {
		if err := identifier("resume.agentSessionId", req.Resume.AgentSessionID); err != nil {
			return err
		}
		if err := boundedRequired("resume.worktreePath", req.Resume.WorktreePath, 8192); err != nil {
			return err
		}
	}
	needsInteraction := req.Command.Operation == OperationInteractionResponse
	if needsInteraction != (req.Interaction != nil) {
		return invalid("interaction", "与操作不匹配")
	}
	if req.Interaction != nil {
		if err := identifier("interaction.id", req.Interaction.ID); err != nil {
			return err
		}
		if !req.Interaction.Decision.Valid() {
			return invalid("interaction.decision", "不受支持")
		}
		if err := bounded("interaction.message", req.Interaction.Message, 16*1024*1024); err != nil {
			return err
		}
		if err := bounded("interaction.payload", req.Interaction.Payload, 16*1024*1024); err != nil {
			return err
		}
	}
	return nil
}

func validateAgent(agent AgentSpec) error {
	if agent.Type != AgentCodex && agent.Type != AgentClaude {
		return invalid("agent.type", "不受支持")
	}
	if agent.WorkMode != WorkModePlan && agent.WorkMode != WorkModeImplement && agent.WorkMode != WorkModeReview {
		return invalid("agent.workMode", "不受支持")
	}
	if err := bounded("agent.config.model", agent.Config.Model, 1024); err != nil {
		return err
	}
	if agent.Type == AgentCodex {
		if agent.Config.Effort != "" || agent.Config.PermissionMode != "" {
			return invalid("agent.config", "Codex 请求不能包含 Claude 配置")
		}
		if !oneOf(agent.Config.ReasoningEffort, "", "minimal", "low", "medium", "high", "xhigh") ||
			!oneOf(agent.Config.SandboxMode, "", "read-only", "workspace-write", "danger-full-access") ||
			!oneOf(agent.Config.ApprovalPolicy, "", "untrusted", "on-failure", "on-request", "never") {
			return invalid("agent.config", "Codex 配置枚举不受支持")
		}
		return nil
	}
	if agent.Config.ReasoningEffort != "" || agent.Config.SandboxMode != "" || agent.Config.ApprovalPolicy != "" ||
		agent.Config.FullAuto || agent.Config.BypassApprovalsAndSandbox {
		return invalid("agent.config", "Claude 请求不能包含 Codex 配置")
	}
	if !oneOf(agent.Config.Effort, "", "low", "medium", "high", "xhigh", "max") ||
		!oneOf(agent.Config.PermissionMode, "", "acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan") {
		return invalid("agent.config", "Claude 配置枚举不受支持")
	}
	return nil
}

func validateProject(project ProjectSpec) error {
	if err := boundedRequired("project.gitUrl", project.GitURL, 8192); err != nil {
		return err
	}
	parsed, err := url.Parse(project.GitURL)
	if err != nil {
		return invalid("project.gitUrl", "格式非法")
	}
	if parsed.User != nil {
		return invalid("project.gitUrl", "禁止内嵌凭据")
	}
	if err := boundedRequired("project.defaultBranch", project.DefaultBranch, 1024); err != nil {
		return err
	}
	return boundedRequired("project.worktreeNamePrefix", project.WorktreeNamePrefix, 1024)
}

func validateCommands(commands Commands) error {
	if len(commands.Pre) > 1024 || len(commands.Post) > 1024 {
		return invalid("commands", "命令数量超过 1024")
	}
	for _, group := range [][]string{commands.Pre, commands.Post} {
		for _, command := range group {
			if err := bounded("commands", command, 1024*1024); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateEnvironment(environment Environment) error {
	if len(environment.Variables) > 4096 {
		return invalid("environment.variables", "数量超过 4096")
	}
	seen := map[string]struct{}{}
	for i, variable := range environment.Variables {
		field := fmt.Sprintf("environment.variables[%d]", i)
		if !envKeyPattern.MatchString(variable.Key) || utf8.RuneCountInString(variable.Key) > 1024 {
			return invalid(field+".key", "格式非法")
		}
		if _, ok := seen[variable.Key]; ok {
			return invalid(field+".key", "重复")
		}
		seen[variable.Key] = struct{}{}
		if err := bounded(field+".value", variable.Value, 16*1024*1024); err != nil {
			return err
		}
	}
	return nil
}

// Valid 报告交互决定是否属于 execution v1 闭集。
// 参数：接收者为待校验交互决定。
// 返回：属于闭集返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (d InteractionDecision) Valid() bool {
	return d == DecisionApprove || d == DecisionApproveForSession || d == DecisionDeny || d == DecisionCancel || d == DecisionRespond
}

// Valid 报告交互类型是否属于 execution v1 闭集。
// 参数：接收者为待校验交互类型。
// 返回：属于闭集返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (k InteractionKind) Valid() bool {
	return k == InteractionUserInput || k == InteractionCommandApproval || k == InteractionFileApproval || k == InteractionPermissionApproval
}

// Valid 报告终态 payload 状态是否属于 execution v1 闭集。
// 参数：接收者为待校验终态状态。
// 返回：属于闭集返回 true，否则返回 false。
// 错误：本方法不返回错误。
func (s TerminalStatus) Valid() bool {
	return s == TerminalCompleted || s == TerminalFailed || s == TerminalRejected || s == TerminalCanceled
}

func validateEventPayload(event *ExecutionEvent) error {
	switch event.Event.Type {
	case EventExecutionAccepted:
		return payloadAllows(event.Payload)
	case EventWorkspaceReady:
		if event.Runtime == nil || strings.TrimSpace(event.Runtime.WorktreePath) == "" {
			return invalid("runtime.worktreePath", "workspace.ready 必须包含")
		}
		return payloadAllows(event.Payload)
	case EventAgentSessionStarted, EventAgentSessionUpdated:
		if event.Runtime == nil || strings.TrimSpace(event.Runtime.AgentSessionID) == "" {
			return invalid("runtime.agentSessionId", "Agent session 事件必须包含")
		}
		return payloadAllows(event.Payload)
	case EventLogChunk:
		if err := payloadAllows(event.Payload, "stream", "content", "chunkIndex", "finalChunk"); err != nil {
			return err
		}
		stream, err := payloadRequiredString(event.Payload, "stream", 32)
		if err != nil {
			return err
		}
		if !LogStream(stream).Valid() {
			return invalid("payload.stream", "不受支持")
		}
		content, err := payloadRequiredString(event.Payload, "content", MaxLogChunkBytes)
		if err != nil {
			return err
		}
		if len(content) > MaxLogChunkBytes {
			return invalid("payload.content", fmt.Sprintf("不能超过 %d 字节", MaxLogChunkBytes))
		}
		if err := payloadOptionalNonNegativeInteger(event.Payload, "chunkIndex"); err != nil {
			return err
		}
		if value, ok := event.Payload["finalChunk"]; ok {
			if _, valid := value.(bool); !valid {
				return invalid("payload.finalChunk", "必须是布尔值")
			}
		}
		return nil
	case EventConversationMessage:
		if err := payloadAllows(event.Payload, "role", "content"); err != nil {
			return err
		}
		if _, err := payloadRequiredString(event.Payload, "content", MaxAgentEventBytes); err != nil {
			return err
		}
		if role, ok := event.Payload["role"]; ok {
			text, ok := role.(string)
			if !ok || !oneOf(text, "assistant", "user", "system") {
				return invalid("payload.role", "不受支持")
			}
		}
		return nil
	case EventInteractionRequested, EventInteractionResolved:
		if err := payloadAllows(event.Payload, "interactionId", "kind", "title", "body", "message", "rawPayload", "payload", "decision"); err != nil {
			return err
		}
		if _, err := payloadIdentifier(event.Payload, "interactionId"); err != nil {
			return err
		}
		kind, err := payloadRequiredString(event.Payload, "kind", 64)
		if err != nil {
			return err
		}
		if !InteractionKind(kind).Valid() {
			return invalid("payload.kind", "不受支持")
		}
		for _, field := range []struct {
			name string
			max  int
		}{{"title", 4096}, {"body", MaxAgentEventBytes}, {"message", MaxAgentEventBytes}, {"rawPayload", MaxAgentEventBytes}, {"payload", MaxAgentEventBytes}} {
			if err := payloadOptionalString(event.Payload, field.name, field.max); err != nil {
				return err
			}
		}
		if decision, ok := event.Payload["decision"]; ok {
			text, ok := decision.(string)
			if !ok || !InteractionDecision(text).Valid() {
				return invalid("payload.decision", "不受支持")
			}
		}
		return nil
	case EventResultUpdated:
		if err := payloadAllows(event.Payload, "result"); err != nil {
			return err
		}
		_, err := payloadRequiredString(event.Payload, "result", MaxAgentEventBytes)
		return err
	case EventExecutionDiagnostic:
		if err := payloadAllows(event.Payload, "errorCode", "message", "retryable"); err != nil {
			return err
		}
		_, hasErrorCode := event.Payload["errorCode"]
		_, hasMessage := event.Payload["message"]
		if !hasErrorCode && !hasMessage {
			return invalid("payload", "execution.diagnostic 必须包含 errorCode 或 message")
		}
		if err := payloadOptionalString(event.Payload, "errorCode", 256); err != nil {
			return err
		}
		if err := payloadOptionalString(event.Payload, "message", MaxAgentEventBytes); err != nil {
			return err
		}
		if value, ok := event.Payload["retryable"]; ok {
			if _, ok := value.(bool); !ok {
				return invalid("payload.retryable", "必须是布尔值")
			}
		}
		return nil
	case EventExecutionTerminal:
		if err := payloadAllows(event.Payload, "status", "result", "errorCode", "message"); err != nil {
			return err
		}
		status, err := payloadRequiredString(event.Payload, "status", 32)
		if err != nil {
			return err
		}
		if !TerminalStatus(status).Valid() {
			return invalid("payload.status", "不受支持")
		}
		if err := payloadOptionalString(event.Payload, "result", MaxAgentEventBytes); err != nil {
			return err
		}
		if err := payloadOptionalString(event.Payload, "errorCode", 256); err != nil {
			return err
		}
		if err := payloadOptionalString(event.Payload, "message", MaxAgentEventBytes); err != nil {
			return err
		}
		return nil
	default:
		return invalid("event.type", "不受支持")
	}
}

func payloadAllows(payload map[string]any, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range payload {
		if _, ok := set[key]; !ok {
			return invalid("payload."+key, "不允许未知字段")
		}
	}
	return nil
}

func payloadOptionalNonNegativeInteger(payload map[string]any, field string) error {
	value, ok := payload[field]
	if !ok {
		return nil
	}
	valid := false
	switch number := value.(type) {
	case int:
		valid = number >= 0
	case int8:
		valid = number >= 0
	case int16:
		valid = number >= 0
	case int32:
		valid = number >= 0
	case int64:
		valid = number >= 0
	case uint, uint8, uint16, uint32, uint64:
		valid = true
	case float64:
		valid = number >= 0 && number == float64(int64(number))
	case json.Number:
		integer, err := number.Int64()
		valid = err == nil && integer >= 0
	}
	if !valid {
		return invalid("payload."+field, "必须是非负整数")
	}
	return nil
}

func payloadIdentifier(payload map[string]any, field string) (string, error) {
	value, err := payloadRequiredString(payload, field, 256)
	if err != nil {
		return "", err
	}
	if err := identifier("payload."+field, value); err != nil {
		return "", err
	}
	return value, nil
}

func payloadRequiredString(payload map[string]any, field string, max int) (string, error) {
	value, ok := payload[field]
	if !ok {
		return "", invalid("payload."+field, "不能为空")
	}
	text, ok := value.(string)
	if !ok {
		return "", invalid("payload."+field, "必须是字符串")
	}
	if err := boundedRequired("payload."+field, text, max); err != nil {
		return "", err
	}
	return text, nil
}

func payloadOptionalString(payload map[string]any, field string, max int) error {
	value, ok := payload[field]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return invalid("payload."+field, "必须是字符串")
	}
	return bounded("payload."+field, text, max)
}

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

func identifier(field, value string) error {
	return boundedRequired(field, value, 256)
}

func uuidV7(field, value string) error {
	id, err := uuid.Parse(value)
	if err != nil || id.Version() != 7 {
		return invalid(field, "必须是 UUIDv7")
	}
	return nil
}

func boundedRequired(field, value string, max int) error {
	if value == "" {
		return invalid(field, "不能为空")
	}
	return bounded(field, value, max)
}

func bounded(field, value string, max int) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return invalid(field, fmt.Sprintf("长度不能超过 %d", max))
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
