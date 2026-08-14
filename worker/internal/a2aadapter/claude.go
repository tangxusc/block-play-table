package a2aadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

const maxClaudeInteractionRounds = 10

type claudeAgent struct {
	binary       string
	newSessionID func() string
}

func newClaudeAgent(binary string, newSessionID func() string) *claudeAgent {
	if newSessionID == nil {
		newSessionID = uuid.NewString
	}
	return &claudeAgent{binary: binary, newSessionID: newSessionID}
}

func (a *claudeAgent) run(ctx context.Context, input Input, emit func(Event)) error {
	message := promptForRequest(input.Request)
	sessionID := ""
	resume := false
	switch input.Request.Command.Operation {
	case a2aext.OperationStart, a2aext.OperationRetry:
		sessionID = a.newSessionID()
	case a2aext.OperationContinue:
		message = strings.TrimSpace(input.Message)
		sessionID = strings.TrimSpace(input.AgentSessionID)
		resume = true
		if message == "" || sessionID == "" {
			return fmt.Errorf("CONTINUE 缺少 message 或 agentSessionId")
		}
	default:
		return fmt.Errorf("Claude Adapter 不支持 operation %s", input.Request.Command.Operation)
	}
	return a.runClaudeSession(ctx, input, sessionID, message, resume, emit)
}

func (a *claudeAgent) runClaudeSession(ctx context.Context, input Input, sessionID, message string, resume bool, emit func(Event)) error {
	approvedForSession := map[string]bool{}
	nextAllowedTools := []string(nil)
	for round := 0; round < maxClaudeInteractionRounds; round++ {
		args := claudeCommandArgs(input.Request.Agent.Config, sessionID, message, resume, append(approvedToolsList(approvedForSession), nextAllowedTools...))
		nextAllowedTools = nil
		result, err := a.runSessionOnce(ctx, input.WorktreePath, input.Environment, args, sessionID, emit)
		if err != nil {
			return err
		}
		if result.SessionID != "" {
			sessionID = result.SessionID
		}
		if len(result.PermissionDenials) == 0 {
			return emitSessionCompletion(result, emit)
		}
		if err := emitFallbackConversation(result, emit); err != nil {
			return err
		}
		if input.RequestInteraction == nil {
			return fmt.Errorf("Claude 请求权限，但 Runtime 未配置交互路由")
		}
		var resumeMessages []string
		for _, denial := range result.PermissionDenials {
			response, err := input.RequestInteraction(ctx, claudeInteractionRequest(denial, result.Content, sessionID))
			if err != nil {
				return err
			}
			switch response.Decision {
			case a2aext.DecisionApprove, a2aext.DecisionApproveForSession:
				if tool := denial.allowedTool(); tool != "" {
					if response.Decision == a2aext.DecisionApproveForSession {
						approvedForSession[tool] = true
					} else {
						nextAllowedTools = append(nextAllowedTools, tool)
					}
				}
				resumeMessages = append(resumeMessages, claudeApprovalResumeMessage(denial, response))
			case a2aext.DecisionDeny:
				resumeMessages = append(resumeMessages, claudeDenialResumeMessage(denial, response))
			case a2aext.DecisionCancel:
				return ErrInteractionCanceled
			default:
				return fmt.Errorf("不支持 Claude 交互决定 %q", response.Decision)
			}
		}
		message = strings.Join(resumeMessages, "\n\n")
		resume = true
	}
	return fmt.Errorf("Claude 权限交互超过 %d 轮", maxClaudeInteractionRounds)
}

func (a *claudeAgent) runSessionOnce(ctx context.Context, dir string, environment map[string]string, args []string, initialSessionID string, emit func(Event)) (sessionCommandResult, error) {
	command := exec.Command(a.binary, args...)
	command.Dir = dir
	command.Env = mergeEnvironment(environment)
	configureCommandForCancel(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return sessionCommandResult{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return sessionCommandResult{}, err
	}
	if err := command.Start(); err != nil {
		return sessionCommandResult{}, err
	}
	var stopOnce sync.Once
	stopCommand := func() {
		stopOnce.Do(func() {
			killCommandProcessGroup(command)
			// 权限拒绝后不再消费本轮输出，主动关闭管道可避免后代进程持有 fd 导致 Wait 前死锁。
			_ = stdout.Close()
			_ = stderr.Close()
		})
	}

	var readers sync.WaitGroup
	var stateMu sync.Mutex
	sessionID := initialSessionID
	lastConversation := ""
	finalResult := ""
	var permissionDenials []claudePermissionDenial
	toolUses := map[string]claudePermissionDenial{}
	sawConversation := false
	var scanErr error
	readers.Add(2)
	go func() {
		defer readers.Done()
		scanner := newAgentEventScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			emit(Event{Type: EventStdout, Content: line})
			parsed := parseClaudeJSONLine(line)
			stateMu.Lock()
			if parsed.SessionID != "" {
				sessionID = parsed.SessionID
			}
			currentSessionID := sessionID
			if parsed.Conversation != "" {
				lastConversation = parsed.Conversation
				sawConversation = true
			}
			if parsed.FinalResult != "" {
				finalResult = parsed.FinalResult
			}
			for _, toolUse := range parsed.ToolUses {
				if toolUse.ToolUseID != "" {
					toolUses[toolUse.ToolUseID] = toolUse
				}
			}
			if len(parsed.PermissionDenials) > 0 {
				permissionDenials = enrichClaudePermissionDenials(parsed.PermissionDenials, toolUses)
			}
			stateMu.Unlock()
			if parsed.Conversation != "" {
				emit(Event{Type: EventConversation, Content: parsed.Conversation, AgentSessionID: currentSessionID, Metadata: map[string]string{"agentSessionId": currentSessionID}})
			}
			if parsed.StopForPermission {
				// 非交互 CLI 可能在拒绝后继续推理或重试；交互必须尽快交还 Manager。
				stopCommand()
			}
		}
		if err := scanner.Err(); err != nil {
			stateMu.Lock()
			scanErr = normalizeAgentScannerError(err)
			stateMu.Unlock()
			stopCommand()
		}
	}()
	go func() {
		defer readers.Done()
		scanner := newAgentEventScanner(stderr)
		for scanner.Scan() {
			emit(Event{Type: EventStderr, Content: scanner.Text()})
		}
		if err := scanner.Err(); err != nil {
			stateMu.Lock()
			if scanErr == nil {
				scanErr = normalizeAgentScannerError(err)
			}
			stateMu.Unlock()
			stopCommand()
		}
	}()

	done := make(chan error, 1)
	go func() {
		readers.Wait()
		done <- command.Wait()
	}()
	select {
	case commandErr := <-done:
		stateMu.Lock()
		readerErr := scanErr
		hasPermissionDenials := len(permissionDenials) > 0
		stateMu.Unlock()
		if readerErr != nil && !hasPermissionDenials {
			return sessionCommandResult{}, readerErr
		}
		if commandErr != nil && !hasPermissionDenials {
			return sessionCommandResult{}, commandErr
		}
	case <-ctx.Done():
		stopCommand()
		<-done
		return sessionCommandResult{}, ctx.Err()
	}

	stateMu.Lock()
	completedContent := finalResult
	if completedContent == "" {
		completedContent = lastConversation
	}
	result := sessionCommandResult{
		SessionID:                      sessionID,
		Content:                        completedContent,
		ShouldEmitFallbackConversation: !sawConversation && finalResult != "",
		PermissionDenials:              append([]claudePermissionDenial(nil), permissionDenials...),
	}
	stateMu.Unlock()
	if result.SessionID == "" {
		return sessionCommandResult{}, fmt.Errorf("Claude 未报告 agent session id")
	}
	return result, nil
}

type sessionCommandResult struct {
	SessionID                      string
	Content                        string
	ShouldEmitFallbackConversation bool
	PermissionDenials              []claudePermissionDenial
}

type claudePermissionDenial struct {
	ToolName   string
	ToolUseID  string
	ToolInput  map[string]any
	RawPayload string
}

func emitSessionCompletion(result sessionCommandResult, emit func(Event)) error {
	if err := emitFallbackConversation(result, emit); err != nil {
		return err
	}
	emit(Event{Type: EventCompleted, Content: result.Content, AgentSessionID: result.SessionID})
	return nil
}

func emitFallbackConversation(result sessionCommandResult, emit func(Event)) error {
	if result.ShouldEmitFallbackConversation {
		emit(Event{Type: EventConversation, Content: result.Content, AgentSessionID: result.SessionID, Metadata: map[string]string{"agentSessionId": result.SessionID}})
	}
	return nil
}

func claudeConfigArgs(config a2aext.AgentConfig) []string {
	var args []string
	if config.Model != "" {
		args = append(args, "--model", config.Model)
	}
	if config.Effort != "" {
		args = append(args, "--effort", config.Effort)
	}
	if config.PermissionMode != "" && config.PermissionMode != "default" {
		args = append(args, "--permission-mode", config.PermissionMode)
	}
	return args
}

func claudeCommandArgs(config a2aext.AgentConfig, sessionID, message string, resume bool, allowedTools []string) []string {
	args := append([]string{"-p"}, claudeConfigArgs(config)...)
	if tools := uniqueNonEmptyStrings(allowedTools); len(tools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, tools...)
	}
	args = append(args, "--output-format=stream-json", "--verbose")
	if resume {
		return append(args, "--resume", sessionID, message)
	}
	return append(args, "--session-id", sessionID, message)
}

func approvedToolsList(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for tool := range values {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

type claudeLineParse struct {
	SessionID         string
	Conversation      string
	FinalResult       string
	ToolUses          []claudePermissionDenial
	PermissionDenials []claudePermissionDenial
	StopForPermission bool
}

func parseClaudeJSONLine(line string) claudeLineParse {
	var value map[string]any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return claudeLineParse{}
	}
	parsed := claudeLineParse{SessionID: directSessionID(value)}
	switch stringField(value, "type") {
	case "system":
		if stringField(value, "subtype") == "permission_denied" {
			parsed.PermissionDenials = []claudePermissionDenial{claudeSystemPermissionDenial(value)}
			parsed.StopForPermission = true
		}
		return parsed
	case "assistant":
		parsed.Conversation = claudeAssistantText(value)
		parsed.ToolUses = claudeAssistantToolUses(value)
	case "result":
		parsed.FinalResult = stringField(value, "result")
		parsed.PermissionDenials = claudePermissionDenials(value)
	case "thread.started":
		if parsed.SessionID == "" {
			parsed.SessionID = stringField(value, "thread_id")
		}
	case "item.completed":
		if item, ok := objectField(value, "item"); ok && stringField(item, "type") == "agent_message" {
			parsed.Conversation = stringField(item, "text")
		}
	case "":
		parsed.Conversation = stringField(value, "message")
	}
	return parsed
}

func claudeSystemPermissionDenial(value map[string]any) claudePermissionDenial {
	return claudePermissionDenial{
		ToolName:   stringField(value, "tool_name"),
		ToolUseID:  stringField(value, "tool_use_id"),
		ToolInput:  map[string]any{},
		RawPayload: jsonString(value),
	}
}

func enrichClaudePermissionDenials(denials []claudePermissionDenial, toolUses map[string]claudePermissionDenial) []claudePermissionDenial {
	out := make([]claudePermissionDenial, 0, len(denials))
	for _, denial := range denials {
		if toolUse, ok := toolUses[denial.ToolUseID]; ok {
			if denial.ToolName == "" {
				denial.ToolName = toolUse.ToolName
			}
			if len(denial.ToolInput) == 0 {
				denial.ToolInput = toolUse.ToolInput
			}
		}
		out = append(out, denial)
	}
	return out
}

func claudePermissionDenials(value map[string]any) []claudePermissionDenial {
	items, ok := value["permission_denials"].([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]claudePermissionDenial, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		toolInput, _ := objectField(object, "tool_input")
		if toolInput == nil {
			toolInput = map[string]any{}
		}
		toolName := stringField(object, "tool_name")
		key := toolName + "\x00" + jsonString(toolInput)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, claudePermissionDenial{
			ToolName: toolName, ToolUseID: stringField(object, "tool_use_id"),
			ToolInput: toolInput, RawPayload: jsonString(object),
		})
	}
	return out
}

func claudeInteractionRequest(denial claudePermissionDenial, finalResult, sessionID string) InteractionRequest {
	summary := denial.summary()
	body := strings.TrimSpace(strings.Join(nonEmptyStrings(finalResult, summary), "\n\n"))
	if body == "" {
		body = denial.RawPayload
	}
	return InteractionRequest{
		Kind: denial.kind(), Title: denial.title(), Body: body,
		RawPayload: denial.RawPayload, AgentSessionID: sessionID,
	}
}

func (d claudePermissionDenial) kind() a2aext.InteractionKind {
	switch strings.ToLower(d.ToolName) {
	case "bash":
		return a2aext.InteractionCommandApproval
	case "edit", "write", "multiedit":
		return a2aext.InteractionFileApproval
	default:
		return a2aext.InteractionPermissionApproval
	}
}

func (d claudePermissionDenial) title() string {
	switch d.kind() {
	case a2aext.InteractionCommandApproval:
		return "Approve Claude command"
	case a2aext.InteractionFileApproval:
		return "Approve Claude file change"
	default:
		if d.ToolName != "" {
			return "Approve Claude " + d.ToolName
		}
		return "Approve Claude tool"
	}
}

func (d claudePermissionDenial) summary() string {
	tool := firstNonEmptyString(d.ToolName, "tool")
	switch d.kind() {
	case a2aext.InteractionCommandApproval:
		return "Tool: " + tool + "\nCommand: " + firstNonEmptyString(mapString(d.ToolInput, "command"), "(not reported)")
	case a2aext.InteractionFileApproval:
		path := firstNonEmptyString(mapString(d.ToolInput, "file_path"), mapString(d.ToolInput, "path"), "(not reported)")
		return "Tool: " + tool + "\nFile: " + path
	default:
		return "Tool: " + tool
	}
}

func (d claudePermissionDenial) allowedTool() string {
	if d.kind() == a2aext.InteractionCommandApproval {
		if command := strings.TrimSpace(mapString(d.ToolInput, "command")); command != "" {
			return "Bash(" + command + ")"
		}
		return firstNonEmptyString(d.ToolName, "Bash")
	}
	return strings.TrimSpace(d.ToolName)
}

func claudeApprovalResumeMessage(denial claudePermissionDenial, response InteractionResponse) string {
	scope := "for this request"
	if response.Decision == a2aext.DecisionApproveForSession {
		scope = "for this task session"
	}
	parts := []string{"The user approved the Claude tool request " + scope + ". Continue the task.", denial.summary()}
	if strings.TrimSpace(response.Message) != "" {
		parts = append(parts, "User note: "+strings.TrimSpace(response.Message))
	}
	return strings.Join(parts, "\n\n")
}

func claudeDenialResumeMessage(denial claudePermissionDenial, response InteractionResponse) string {
	decision := "denied"
	if response.Decision == a2aext.DecisionCancel {
		decision = "canceled"
	}
	parts := []string{
		"The user " + decision + " the Claude tool request. Do not perform that operation. Continue with an alternative if possible; otherwise explain why the task is blocked.",
		denial.summary(),
	}
	if strings.TrimSpace(response.Message) != "" {
		parts = append(parts, "User note: "+strings.TrimSpace(response.Message))
	}
	return strings.Join(parts, "\n\n")
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func jsonString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func directSessionID(value map[string]any) string {
	for _, key := range []string{"session_id", "sessionId", "conversation_id", "conversationId", "thread_id", "threadId"} {
		if text := stringField(value, key); text != "" {
			return text
		}
	}
	return ""
}

func claudeAssistantText(value map[string]any) string {
	message, ok := objectField(value, "message")
	if !ok {
		return ""
	}
	content, ok := message["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range content {
		part, ok := item.(map[string]any)
		if ok && stringField(part, "type") == "text" {
			if text := stringField(part, "text"); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func claudeAssistantToolUses(value map[string]any) []claudePermissionDenial {
	message, ok := objectField(value, "message")
	if !ok {
		return nil
	}
	content, ok := message["content"].([]any)
	if !ok {
		return nil
	}
	var out []claudePermissionDenial
	for _, item := range content {
		part, ok := item.(map[string]any)
		if !ok || stringField(part, "type") != "tool_use" {
			continue
		}
		input, _ := objectField(part, "input")
		if input == nil {
			input = map[string]any{}
		}
		out = append(out, claudePermissionDenial{
			ToolName: stringField(part, "name"), ToolUseID: stringField(part, "id"),
			ToolInput: input, RawPayload: jsonString(part),
		})
	}
	return out
}

func objectField(value map[string]any, key string) (map[string]any, bool) {
	child, ok := value[key].(map[string]any)
	return child, ok
}

func stringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}
