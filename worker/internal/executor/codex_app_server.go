package executor

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

type CodexAppServerAgent struct {
	binary string
}

func newCodexAppServerAgent(binary string) *CodexAppServerAgent {
	return &CodexAppServerAgent{binary: binary}
}

func (a *CodexAppServerAgent) Run(ctx context.Context, input AgentInput, emit func(AgentEvent)) error {
	return a.runTurn(ctx, input.Task, input.WorktreeDir, input.Env, promptForTask(input.Task), "", input.RequestInteraction, emit)
}

func (a *CodexAppServerAgent) Continue(ctx context.Context, input AgentContinuationInput, emit func(AgentEvent)) error {
	if strings.TrimSpace(input.AgentSessionID) == "" {
		return fmt.Errorf("agent session id is required")
	}
	if strings.TrimSpace(input.Message) == "" {
		return fmt.Errorf("message is required")
	}
	return a.runTurn(ctx, input.Task, input.WorktreeDir, input.Env, input.Message, input.AgentSessionID, input.RequestInteraction, emit)
}

func (a *CodexAppServerAgent) runTurn(ctx context.Context, task protocol.TaskPayload, worktreeDir string, env map[string]string, message string, agentSessionID string, requestInteraction func(context.Context, AgentInteractionRequest) (AgentInteractionResponse, error), emit func(AgentEvent)) error {
	rpc, err := startCodexAppServer(ctx, a.binary, worktreeDir, env, task, requestInteraction, emit)
	if err != nil {
		return err
	}
	defer rpc.close()

	if err := rpc.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "block-play-table-worker",
			"title":   "Block Play Table Worker",
			"version": "0",
		},
		"capabilities": map[string]any{"experimentalApi": true},
	}, nil); err != nil {
		return err
	}

	threadID := agentSessionID
	if threadID == "" {
		var response struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := rpc.call(ctx, "thread/start", codexThreadStartParams(task, worktreeDir), &response); err != nil {
			return err
		}
		threadID = response.Thread.ID
	} else {
		var response struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := rpc.call(ctx, "thread/resume", codexThreadResumeParams(task, worktreeDir, threadID), &response); err != nil {
			return err
		}
		if response.Thread.ID != "" {
			threadID = response.Thread.ID
		}
	}
	if threadID == "" {
		return fmt.Errorf("codex app-server did not report thread id")
	}
	rpc.state.setThread(threadID)

	var turnResponse struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := rpc.call(ctx, "turn/start", codexTurnStartParams(task, worktreeDir, threadID, message), &turnResponse); err != nil {
		return err
	}
	rpc.state.setTurn(turnResponse.Turn.ID)

	result, err := rpc.state.wait(ctx)
	if err != nil {
		return err
	}
	emit(AgentEvent{Type: AgentEventCompleted, Content: result, AgentSessionID: threadID})
	return nil
}

type codexRPC struct {
	cmd                *exec.Cmd
	stdin              io.WriteCloser
	writeMu            sync.Mutex
	pendingMu          sync.Mutex
	pending            map[string]chan codexRPCMessage
	nextID             int
	done               chan error
	state              *codexRunState
	task               protocol.TaskPayload
	requestInteraction func(context.Context, AgentInteractionRequest) (AgentInteractionResponse, error)
	emit               func(AgentEvent)
}

type codexRPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func startCodexAppServer(ctx context.Context, binary, dir string, env map[string]string, task protocol.TaskPayload, requestInteraction func(context.Context, AgentInteractionRequest) (AgentInteractionResponse, error), emit func(AgentEvent)) (*codexRPC, error) {
	cmd := exec.Command(binary, "app-server", "--listen", "stdio://")
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	configureCommandForCancel(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	rpc := &codexRPC{
		cmd:                cmd,
		stdin:              stdin,
		pending:            map[string]chan codexRPCMessage{},
		done:               make(chan error, 1),
		state:              newCodexRunState(),
		task:               task,
		requestInteraction: requestInteraction,
		emit:               emit,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go rpc.readStdout(ctx, stdout)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			emit(AgentEvent{Type: AgentEventStderr, Content: scanner.Text()})
		}
	}()
	go func() {
		rpc.done <- cmd.Wait()
	}()
	return rpc, nil
}

func (r *codexRPC) readStdout(ctx context.Context, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		var message codexRPCMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			r.emit(AgentEvent{Type: AgentEventStderr, Content: line})
			continue
		}
		if len(message.ID) > 0 && message.Method == "" {
			r.resolveCall(message)
			continue
		}
		if len(message.ID) > 0 && message.Method != "" {
			if err := r.handleServerRequest(ctx, message); err != nil {
				r.emit(AgentEvent{Type: AgentEventStderr, Content: err.Error()})
				r.state.fail(err)
				_ = r.write(map[string]any{"id": message.ID, "error": map[string]any{"message": err.Error()}})
			}
			continue
		}
		r.handleNotification(message)
	}
	if err := scanner.Err(); err != nil {
		r.state.fail(err)
	}
}

func (r *codexRPC) resolveCall(message codexRPCMessage) {
	key := string(message.ID)
	r.pendingMu.Lock()
	ch := r.pending[key]
	delete(r.pending, key)
	r.pendingMu.Unlock()
	if ch != nil {
		ch <- message
	}
}

func (r *codexRPC) call(ctx context.Context, method string, params any, out any) error {
	r.pendingMu.Lock()
	r.nextID++
	id := r.nextID
	key := fmt.Sprintf("%d", id)
	ch := make(chan codexRPCMessage, 1)
	r.pending[key] = ch
	r.pendingMu.Unlock()
	if err := r.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		r.pendingMu.Lock()
		delete(r.pending, key)
		r.pendingMu.Unlock()
		return err
	}
	select {
	case message := <-ch:
		if message.Error != nil {
			return fmt.Errorf("codex app-server %s failed: %s", method, message.Error.Message)
		}
		if out != nil && len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, out); err != nil {
				return err
			}
		}
		return nil
	case err := <-r.done:
		return fmt.Errorf("codex app-server exited during %s: %w", method, err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *codexRPC) sendResult(id json.RawMessage, result any) error {
	return r.write(map[string]any{"id": id, "result": result})
}

func (r *codexRPC) write(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if _, err := r.stdin.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (r *codexRPC) close() {
	_ = r.stdin.Close()
	if r.cmd.Process != nil {
		killCommandProcessGroup(r.cmd)
	}
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
	}
}

func (r *codexRPC) handleNotification(message codexRPCMessage) {
	switch message.Method {
	case "thread/started":
		var params struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.Thread.ID != "" {
			r.state.setThread(params.Thread.ID)
		}
	case "item/completed":
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(message.Params, &params) != nil {
			return
		}
		if params.Item.Type == "agentMessage" && params.Item.Text != "" {
			r.state.recordMessage(params.Item.Text)
			r.emit(AgentEvent{Type: AgentEventConversation, Content: params.Item.Text, AgentSessionID: params.ThreadID, Metadata: map[string]string{"agentSessionId": params.ThreadID}})
		}
	case "turn/completed":
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &params) != nil {
			return
		}
		if params.Turn.Status == "failed" {
			reason := "codex turn failed"
			if params.Turn.Error != nil && params.Turn.Error.Message != "" {
				reason = params.Turn.Error.Message
			}
			r.state.fail(fmt.Errorf("%s", reason))
			return
		}
		if params.Turn.Status == "interrupted" {
			r.state.fail(context.Canceled)
			return
		}
		r.state.complete()
	case "error":
		var params struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.Message != "" {
			r.state.fail(fmt.Errorf("%s", params.Message))
		}
	}
}

func (r *codexRPC) handleServerRequest(ctx context.Context, message codexRPCMessage) error {
	if r.requestInteraction == nil {
		return fmt.Errorf("codex app-server requested %s but task interactions are not configured", message.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return err
	}
	request := codexInteractionRequest(message.Method, params, string(message.Params))
	response, err := r.requestInteraction(ctx, request)
	if err != nil {
		return err
	}
	return r.sendResult(message.ID, codexInteractionRPCResult(message.Method, params, response))
}

func codexInteractionRequest(method string, params map[string]any, raw string) AgentInteractionRequest {
	kind := domain.TaskInteractionUserInput
	title := "Agent input required"
	body := raw
	switch method {
	case "item/commandExecution/requestApproval":
		kind = domain.TaskInteractionCommandApproval
		title = "Command approval"
		body = firstNonEmptyString(mapString(params, "reason"), mapString(params, "command"), raw)
	case "item/fileChange/requestApproval":
		kind = domain.TaskInteractionFileApproval
		title = "File change approval"
		body = firstNonEmptyString(mapString(params, "reason"), mapString(params, "grantRoot"), raw)
	case "item/permissions/requestApproval":
		kind = domain.TaskInteractionPermissionApproval
		title = "Permission approval"
		body = firstNonEmptyString(mapString(params, "reason"), raw)
	case "item/tool/requestUserInput":
		kind = domain.TaskInteractionUserInput
		title = "User input"
		body = raw
		if questions, ok := params["questions"].([]any); ok && len(questions) > 0 {
			if first, ok := questions[0].(map[string]any); ok {
				title = firstNonEmptyString(mapString(first, "header"), title)
				body = firstNonEmptyString(mapString(first, "question"), body)
			}
		}
	}
	return AgentInteractionRequest{
		InteractionID:  codexInteractionID(method, params),
		Kind:           kind,
		Title:          title,
		Body:           body,
		RawPayload:     raw,
		AgentSessionID: mapString(params, "threadId"),
	}
}

func codexInteractionRPCResult(method string, params map[string]any, response AgentInteractionResponse) any {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return map[string]any{"decision": codexApprovalDecision(response.Decision)}
	case "item/permissions/requestApproval":
		if response.Decision == domain.TaskInteractionApprove || response.Decision == domain.TaskInteractionApproveForSession {
			return map[string]any{
				"permissions": codexGrantedPermissions(params["permissions"]),
				"scope":       codexPermissionScope(response.Decision),
			}
		}
		return map[string]any{"permissions": map[string]any{}, "scope": "turn"}
	case "item/tool/requestUserInput":
		if strings.TrimSpace(response.Payload) != "" {
			var payload any
			if json.Unmarshal([]byte(response.Payload), &payload) == nil {
				return payload
			}
		}
		answers := map[string]any{}
		if questions, ok := params["questions"].([]any); ok {
			for _, question := range questions {
				item, ok := question.(map[string]any)
				if !ok {
					continue
				}
				id := mapString(item, "id")
				if id == "" {
					continue
				}
				answers[id] = map[string]any{"answers": []string{response.Message}}
			}
		}
		return map[string]any{"answers": answers}
	default:
		return map[string]any{}
	}
}

func codexApprovalDecision(decision domain.TaskInteractionDecision) string {
	switch decision {
	case domain.TaskInteractionApproveForSession:
		return "acceptForSession"
	case domain.TaskInteractionDeny:
		return "decline"
	case domain.TaskInteractionCancel:
		return "cancel"
	default:
		return "accept"
	}
}

func codexPermissionScope(decision domain.TaskInteractionDecision) string {
	if decision == domain.TaskInteractionApproveForSession {
		return "session"
	}
	return "turn"
}

func codexGrantedPermissions(value any) map[string]any {
	requested, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	granted := map[string]any{}
	if network := requested["network"]; network != nil {
		granted["network"] = network
	}
	if fileSystem := requested["fileSystem"]; fileSystem != nil {
		granted["fileSystem"] = fileSystem
	}
	return granted
}

func codexInteractionID(method string, params map[string]any) string {
	seed := strings.Join([]string{
		method,
		mapString(params, "threadId"),
		mapString(params, "turnId"),
		mapString(params, "itemId"),
		mapString(params, "approvalId"),
	}, "\x00")
	sum := sha1.Sum([]byte(seed))
	return "codex_" + hex.EncodeToString(sum[:])[:20]
}

func codexThreadStartParams(task protocol.TaskPayload, cwd string) map[string]any {
	params := codexCommonThreadParams(task, cwd)
	params["experimentalRawEvents"] = false
	params["persistExtendedHistory"] = true
	return params
}

func codexThreadResumeParams(task protocol.TaskPayload, cwd, threadID string) map[string]any {
	params := codexCommonThreadParams(task, cwd)
	params["threadId"] = threadID
	params["persistExtendedHistory"] = true
	return params
}

func codexTurnStartParams(task protocol.TaskPayload, cwd, threadID, message string) map[string]any {
	params := map[string]any{
		"threadId": threadID,
		"cwd":      cwd,
		"input": []map[string]any{{
			"type":          "text",
			"text":          message,
			"text_elements": []any{},
		}},
	}
	config := task.AgentConfig.Codex
	if config.Model != "" {
		params["model"] = config.Model
	}
	if config.ReasoningEffort != "" {
		params["effort"] = string(config.ReasoningEffort)
	}
	if approval := codexApprovalPolicy(config); approval != "" {
		params["approvalPolicy"] = approval
	}
	return params
}

func codexCommonThreadParams(task protocol.TaskPayload, cwd string) map[string]any {
	config := task.AgentConfig.Codex
	params := map[string]any{"cwd": cwd}
	if config.Model != "" {
		params["model"] = config.Model
	}
	if approval := codexApprovalPolicy(config); approval != "" {
		params["approvalPolicy"] = approval
	}
	if sandbox := codexSandboxMode(config); sandbox != "" {
		params["sandbox"] = sandbox
	}
	return params
}

func codexApprovalPolicy(config domain.CodexExecutionConfig) string {
	if config.BypassApprovalsAndSandbox {
		return string(domain.CodexApprovalNever)
	}
	if config.ApprovalPolicy != "" {
		return string(config.ApprovalPolicy)
	}
	if config.FullAuto {
		return string(domain.CodexApprovalOnFailure)
	}
	return ""
}

func codexSandboxMode(config domain.CodexExecutionConfig) string {
	if config.BypassApprovalsAndSandbox {
		return string(domain.CodexSandboxDangerFullAccess)
	}
	if config.SandboxMode != "" {
		return string(config.SandboxMode)
	}
	if config.FullAuto {
		return string(domain.CodexSandboxWorkspaceWrite)
	}
	return ""
}

type codexRunState struct {
	mu          sync.Mutex
	done        chan struct{}
	closed      bool
	threadID    string
	turnID      string
	lastMessage string
	err         error
}

func newCodexRunState() *codexRunState {
	return &codexRunState{done: make(chan struct{})}
}

func (s *codexRunState) setThread(threadID string) {
	s.mu.Lock()
	if threadID != "" {
		s.threadID = threadID
	}
	s.mu.Unlock()
}

func (s *codexRunState) setTurn(turnID string) {
	s.mu.Lock()
	if turnID != "" {
		s.turnID = turnID
	}
	s.mu.Unlock()
}

func (s *codexRunState) recordMessage(message string) {
	s.mu.Lock()
	s.lastMessage = message
	s.mu.Unlock()
}

func (s *codexRunState) complete() {
	s.finish(nil)
}

func (s *codexRunState) fail(err error) {
	s.finish(err)
}

func (s *codexRunState) finish(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.err = err
	s.closed = true
	close(s.done)
	s.mu.Unlock()
}

func (s *codexRunState) wait(ctx context.Context) (string, error) {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.err != nil {
			return "", s.err
		}
		if s.lastMessage == "" {
			return "completed", nil
		}
		return s.lastMessage, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func mapString(values map[string]any, key string) string {
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
