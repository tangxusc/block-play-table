package a2aadapter

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

type codexAppServerAgent struct {
	binary string
}

func (a *codexAppServerAgent) run(ctx context.Context, input Input, emit func(Event)) error {
	message := promptForRequest(input.Request)
	sessionID := ""
	switch input.Request.Command.Operation {
	case a2aext.OperationStart, a2aext.OperationRetry:
	case a2aext.OperationContinue:
		message = strings.TrimSpace(input.Message)
		sessionID = strings.TrimSpace(input.AgentSessionID)
		if message == "" || sessionID == "" {
			return errors.New("CONTINUE 缺少 message 或 agentSessionId")
		}
	default:
		return fmt.Errorf("Codex Adapter 不支持 operation %s", input.Request.Command.Operation)
	}
	return a.runTurn(ctx, input.Request, input.WorktreePath, input.Environment, message, sessionID, input.RequestInteraction, emit)
}

func (a *codexAppServerAgent) runTurn(ctx context.Context, request *a2aext.ExecutionRequest, worktreeDir string, env map[string]string, message string, agentSessionID string, requestInteraction func(context.Context, InteractionRequest) (InteractionResponse, error), emit func(Event)) error {
	rpc, err := startCodexAppServer(ctx, a.binary, worktreeDir, env, request, requestInteraction, emit)
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
		if err := rpc.call(ctx, "thread/start", codexThreadStartParams(request, worktreeDir), &response); err != nil {
			return err
		}
		threadID = response.Thread.ID
	} else {
		var response struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := rpc.call(ctx, "thread/resume", codexThreadResumeParams(request, worktreeDir, threadID), &response); err != nil {
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
	if err := rpc.call(ctx, "turn/start", codexTurnStartParams(request, worktreeDir, threadID, message), &turnResponse); err != nil {
		return err
	}
	rpc.state.setTurn(turnResponse.Turn.ID)

	result, err := rpc.state.wait(ctx)
	if err != nil {
		return err
	}
	emit(Event{Type: EventCompleted, Content: result, AgentSessionID: threadID})
	return nil
}

type codexRPC struct {
	cmd                *exec.Cmd
	stdin              io.WriteCloser
	writeMu            sync.Mutex
	pendingMu          sync.Mutex
	pending            map[string]chan codexRPCMessage
	nextID             int
	done               chan struct{}
	processDone        chan struct{}
	stdoutDone         chan struct{}
	stderrDone         chan struct{}
	requestWG          sync.WaitGroup
	processMu          sync.Mutex
	processErr         error
	state              *codexRunState
	requestInteraction func(context.Context, InteractionRequest) (InteractionResponse, error)
	emit               func(Event)
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

func startCodexAppServer(ctx context.Context, binary, dir string, env map[string]string, request *a2aext.ExecutionRequest, requestInteraction func(context.Context, InteractionRequest) (InteractionResponse, error), emit func(Event)) (*codexRPC, error) {
	cmd := exec.Command(binary, "app-server", "--listen", "stdio://")
	cmd.Dir = dir
	cmd.Env = mergeEnvironment(env)
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
		done:               make(chan struct{}),
		processDone:        make(chan struct{}),
		stdoutDone:         make(chan struct{}),
		stderrDone:         make(chan struct{}),
		state:              newCodexRunState(),
		requestInteraction: requestInteraction,
		emit:               emit,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go rpc.readStdout(ctx, stdout)
	go rpc.readStderr(stderr)
	go func() {
		<-rpc.stdoutDone
		<-rpc.stderrDone
		waitErr := cmd.Wait()
		if waitErr == nil {
			waitErr = io.ErrUnexpectedEOF
		}
		rpc.processMu.Lock()
		rpc.processErr = waitErr
		rpc.processMu.Unlock()
		close(rpc.processDone)
		rpc.requestWG.Wait()
		rpc.state.fail(fmt.Errorf("codex app-server 进程退出: %w", waitErr))
		close(rpc.done)
	}()
	return rpc, nil
}

func (r *codexRPC) readStderr(stderr io.Reader) {
	defer close(r.stderrDone)
	scanner := newAgentEventScanner(stderr)
	for scanner.Scan() {
		r.emit(Event{Type: EventStderr, Content: scanner.Text()})
	}
	if err := scanner.Err(); err != nil {
		r.state.fail(fmt.Errorf("codex app-server stderr 结束: %w", err))
		killCommandProcessGroup(r.cmd)
	}
}

func (r *codexRPC) readStdout(ctx context.Context, stdout io.Reader) {
	defer close(r.stdoutDone)
	scanner := newAgentEventScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		var message codexRPCMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			r.emit(Event{Type: EventStderr, Content: line})
			protocolErr := fmt.Errorf("codex app-server 输出畸形 JSON: %w", err)
			r.state.fail(protocolErr)
			killCommandProcessGroup(r.cmd)
			return
		}
		if len(message.ID) > 0 && message.Method == "" {
			r.resolveCall(message)
			continue
		}
		if len(message.ID) > 0 && message.Method != "" {
			r.requestWG.Add(1)
			go r.processServerRequest(ctx, message)
			continue
		}
		r.handleNotification(message)
	}
	err := scanner.Err()
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	r.state.fail(fmt.Errorf("codex app-server stdout 结束: %w", err))
	killCommandProcessGroup(r.cmd)
}

func (r *codexRPC) processServerRequest(ctx context.Context, message codexRPCMessage) {
	defer r.requestWG.Done()
	if err := r.handleServerRequest(ctx, message); err != nil {
		r.emit(Event{Type: EventStderr, Content: err.Error()})
		r.state.fail(err)
		_ = r.write(map[string]any{"id": message.ID, "error": map[string]any{"message": err.Error()}})
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
	handleResponse := func(message codexRPCMessage) error {
		if message.Error != nil {
			return fmt.Errorf("codex app-server %s failed: %s", method, message.Error.Message)
		}
		if out != nil && len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, out); err != nil {
				return err
			}
		}
		return nil
	}
	select {
	case message := <-ch:
		return handleResponse(message)
	case <-r.done:
		select {
		case message := <-ch:
			return handleResponse(message)
		default:
		}
		return fmt.Errorf("codex app-server exited during %s: %w", method, r.processError())
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *codexRPC) processError() error {
	r.processMu.Lock()
	defer r.processMu.Unlock()
	if r.processErr == nil {
		return io.ErrUnexpectedEOF
	}
	return r.processErr
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
			r.emit(Event{Type: EventConversation, Content: params.Item.Text, AgentSessionID: params.ThreadID, Metadata: map[string]string{"agentSessionId": params.ThreadID}})
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
	type interactionResult struct {
		response InteractionResponse
		err      error
	}
	interactionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan interactionResult, 1)
	go func() {
		response, err := r.requestInteraction(interactionCtx, request)
		result <- interactionResult{response: response, err: err}
	}()
	var response InteractionResponse
	select {
	case interaction := <-result:
		if interaction.err != nil {
			return interaction.err
		}
		response = interaction.response
	case <-r.processDone:
		return fmt.Errorf("codex app-server 在处理 %s 时退出: %w", message.Method, r.processError())
	case <-ctx.Done():
		return ctx.Err()
	}
	rpcResult, err := codexInteractionRPCResult(message.Method, params, response)
	if err != nil {
		return err
	}
	if err := r.sendResult(message.ID, rpcResult); err != nil {
		return err
	}
	if response.Decision == a2aext.DecisionCancel {
		r.state.fail(ErrInteractionCanceled)
		killCommandProcessGroup(r.cmd)
	}
	return nil
}

func codexInteractionRequest(method string, params map[string]any, raw string) InteractionRequest {
	kind := a2aext.InteractionUserInput
	title := "Agent input required"
	body := raw
	switch method {
	case "item/commandExecution/requestApproval":
		kind = a2aext.InteractionCommandApproval
		title = "Command approval"
		body = firstNonEmptyString(mapString(params, "reason"), mapString(params, "command"), raw)
	case "item/fileChange/requestApproval":
		kind = a2aext.InteractionFileApproval
		title = "File change approval"
		body = firstNonEmptyString(mapString(params, "reason"), mapString(params, "grantRoot"), raw)
	case "item/permissions/requestApproval":
		kind = a2aext.InteractionPermissionApproval
		title = "Permission approval"
		body = firstNonEmptyString(mapString(params, "reason"), raw)
	case "item/tool/requestUserInput":
		kind = a2aext.InteractionUserInput
		title = "User input"
		body = raw
		if questions, ok := params["questions"].([]any); ok && len(questions) > 0 {
			if first, ok := questions[0].(map[string]any); ok {
				title = firstNonEmptyString(mapString(first, "header"), title)
				body = firstNonEmptyString(mapString(first, "question"), body)
			}
		}
	}
	return InteractionRequest{
		ID:             codexInteractionID(method, params),
		Kind:           kind,
		Title:          title,
		Body:           body,
		RawPayload:     raw,
		AgentSessionID: mapString(params, "threadId"),
	}
}

func codexInteractionRPCResult(method string, params map[string]any, response InteractionResponse) (any, error) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		decision, err := codexApprovalDecision(response.Decision)
		if err != nil {
			return nil, err
		}
		return map[string]any{"decision": decision}, nil
	case "item/permissions/requestApproval":
		if response.Decision == a2aext.DecisionApprove || response.Decision == a2aext.DecisionApproveForSession {
			return map[string]any{
				"permissions": codexGrantedPermissions(params["permissions"]),
				"scope":       codexPermissionScope(response.Decision),
			}, nil
		}
		if response.Decision != a2aext.DecisionDeny && response.Decision != a2aext.DecisionCancel {
			return nil, fmt.Errorf("Codex permission approval decision %q is unsupported", response.Decision)
		}
		return map[string]any{"permissions": map[string]any{}, "scope": "turn"}, nil
	case "item/tool/requestUserInput":
		if response.Decision != a2aext.DecisionRespond && response.Decision != a2aext.DecisionCancel {
			return nil, fmt.Errorf("Codex user input decision %q is unsupported", response.Decision)
		}
		if strings.TrimSpace(response.Payload) != "" {
			var payload any
			if json.Unmarshal([]byte(response.Payload), &payload) == nil {
				return payload, nil
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
		return map[string]any{"answers": answers}, nil
	default:
		return map[string]any{}, nil
	}
}

func codexApprovalDecision(decision a2aext.InteractionDecision) (string, error) {
	switch decision {
	case a2aext.DecisionApprove:
		return "accept", nil
	case a2aext.DecisionApproveForSession:
		return "acceptForSession", nil
	case a2aext.DecisionDeny:
		return "decline", nil
	case a2aext.DecisionCancel:
		return "cancel", nil
	default:
		return "", fmt.Errorf("Codex approval decision %q is unsupported", decision)
	}
}

func codexPermissionScope(decision a2aext.InteractionDecision) string {
	if decision == a2aext.DecisionApproveForSession {
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

func codexThreadStartParams(request *a2aext.ExecutionRequest, cwd string) map[string]any {
	params := codexCommonThreadParams(request, cwd)
	params["experimentalRawEvents"] = false
	params["persistExtendedHistory"] = true
	return params
}

func codexThreadResumeParams(request *a2aext.ExecutionRequest, cwd, threadID string) map[string]any {
	params := codexCommonThreadParams(request, cwd)
	params["threadId"] = threadID
	params["persistExtendedHistory"] = true
	return params
}

func codexTurnStartParams(request *a2aext.ExecutionRequest, cwd, threadID, message string) map[string]any {
	params := map[string]any{
		"threadId":          threadID,
		"cwd":               cwd,
		"approvalsReviewer": "user",
		"input": []map[string]any{{
			"type":          "text",
			"text":          message,
			"text_elements": []any{},
		}},
	}
	config := request.Agent.Config
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

func codexCommonThreadParams(request *a2aext.ExecutionRequest, cwd string) map[string]any {
	config := request.Agent.Config
	// A2A 的交互闭环必须由 Manager 审批，不能被 Codex 本机的自动审查器截获。
	params := map[string]any{"cwd": cwd, "approvalsReviewer": "user"}
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

func codexApprovalPolicy(config a2aext.AgentConfig) string {
	if config.BypassApprovalsAndSandbox {
		return "never"
	}
	if config.ApprovalPolicy != "" {
		return string(config.ApprovalPolicy)
	}
	if config.FullAuto {
		return "on-failure"
	}
	return ""
}

func codexSandboxMode(config a2aext.AgentConfig) string {
	if config.BypassApprovalsAndSandbox {
		return "danger-full-access"
	}
	if config.SandboxMode != "" {
		return string(config.SandboxMode)
	}
	if config.FullAuto {
		return "workspace-write"
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
