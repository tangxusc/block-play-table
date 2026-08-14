package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

type a2aIntentPayload struct {
	Text    string                  `json:"text"`
	Request a2aext.ExecutionRequest `json:"request"`
}

// A2ADispatchRequest 描述 Reconciler 通过 A2A 客户端发送的一条已还原命令。
// 主要用法：由 PrepareA2ADispatchRequest 在网络发送前构造，再交给 A2ATransport.Dispatch 只读消费；其中可能包含仅驻留内存的敏感运行时变量。
type A2ADispatchRequest struct {
	Round   domain.TaskA2ARound
	Intent  domain.TaskA2ADispatchIntent
	Text    string
	Request a2aext.ExecutionRequest
}

// PrepareA2ADispatchRequest 读取意图并仅在发送前还原敏感运行时变量。
// 参数：ctx 用于取消查询，intent 是已由 reconciler 领取的持久化意图。
// 返回：包含 round、文本和完整 execution 请求的发送对象。
// 错误：关联数据缺失、payload 非法或请求未通过共享契约校验时返回错误。
func (s *Service) PrepareA2ADispatchRequest(ctx context.Context, intent domain.TaskA2ADispatchIntent) (*A2ADispatchRequest, error) {
	round, err := s.store.TaskA2ARound(ctx, intent.RoundID)
	if err != nil {
		return nil, a2aProjectionError(err)
	}
	if round.TaskID != intent.TaskID || round.ExecutionID != intent.ExecutionID || round.WorkerID != intent.WorkerID {
		return nil, a2aProtocolError(fmt.Errorf("%w: a2a dispatch association changed", domain.ErrConflict))
	}
	request := A2ADispatchRequest{Round: *round, Intent: intent}
	if intent.Operation == domain.TaskA2AOperationCancel {
		if round.A2ATaskID == "" || round.ContextID == "" {
			return nil, a2aProtocolError(fmt.Errorf("%w: cancel requires a bound a2a task", domain.ErrConflict))
		}
		return &request, nil
	}
	var payload a2aIntentPayload
	if err := json.Unmarshal(intent.Payload, &payload); err != nil {
		return nil, a2aProtocolError(fmt.Errorf("decode a2a dispatch payload: %w", err))
	}
	worker, err := s.store.Worker(ctx, intent.WorkerID)
	if err != nil {
		return nil, a2aProjectionError(err)
	}
	secrets := make(map[string]string)
	for _, variable := range worker.EnabledRuntimeEnv(domain.AgentType(payload.Request.Agent.Type)) {
		if variable.Sensitive {
			secrets[variable.Key] = variable.Value
		}
	}
	for index := range payload.Request.Environment.Variables {
		variable := &payload.Request.Environment.Variables[index]
		if variable.Sensitive {
			value, ok := secrets[variable.Key]
			if !ok || value == "" {
				return nil, a2aProtocolError(fmt.Errorf("%w: sensitive environment %s is unavailable", domain.ErrConflict, variable.Key))
			}
			variable.Value = value
		}
	}
	if err := a2aext.ValidateRequest(&payload.Request, worker.ID); err != nil {
		return nil, a2aProtocolError(fmt.Errorf("validate a2a dispatch payload: %w", err))
	}
	request.Text = payload.Text
	request.Request = payload.Request
	return &request, nil
}

func buildA2ARequest(task *domain.Task, project *domain.Project, worker *domain.Worker, round *domain.TaskA2ARound, commandID string, operation domain.TaskA2AOperation, interaction *domain.TaskInteraction) (*a2aext.ExecutionRequest, error) {
	if task == nil || project == nil || worker == nil || round == nil {
		return nil, fmt.Errorf("task, project, worker and round are required")
	}
	baseBranch := strings.TrimSpace(task.BaseBranch)
	if baseBranch == "" {
		baseBranch = strings.TrimSpace(project.DefaultBranch)
	}
	request := &a2aext.ExecutionRequest{
		Kind:    a2aext.RequestKind,
		Version: a2aext.Version,
		Command: a2aext.Command{ID: commandID, Operation: a2aext.Operation(operation), IssuedAt: round.CreatedAt},
		Scope: a2aext.RequestScope{
			LocalTaskID: task.ID, ExecutionID: round.ExecutionID, Attempt: round.Attempt, Turn: round.Turn, ExpectedWorkerID: worker.ID,
		},
		Task:     a2aext.TaskSpec{Title: task.Title, Description: task.Description, BaseBranch: baseBranch},
		Agent:    a2aext.AgentSpec{Type: a2aext.AgentType(task.AgentType), WorkMode: a2aext.WorkMode(task.AgentConfig.WorkMode)},
		Project:  a2aext.ProjectSpec{ID: project.ID, GitURL: project.GitURL, DefaultBranch: project.DefaultBranch, WorktreeNamePrefix: project.WorktreeNamePrefix},
		Commands: a2aext.Commands{Pre: append([]string(nil), task.PreCommands...), Post: append([]string(nil), task.PostCommands...)},
	}
	switch task.AgentType {
	case domain.AgentCodex:
		request.Agent.Config = a2aext.AgentConfig{
			Model: task.AgentConfig.Codex.Model, ReasoningEffort: string(task.AgentConfig.Codex.ReasoningEffort),
			SandboxMode: string(task.AgentConfig.Codex.SandboxMode), ApprovalPolicy: string(task.AgentConfig.Codex.ApprovalPolicy),
			FullAuto: task.AgentConfig.Codex.FullAuto, BypassApprovalsAndSandbox: task.AgentConfig.Codex.BypassApprovalsAndSandbox,
		}
	case domain.AgentClaude:
		request.Agent.Config = a2aext.AgentConfig{
			Model: task.AgentConfig.Claude.Model, Effort: string(task.AgentConfig.Claude.Effort), PermissionMode: string(task.AgentConfig.Claude.PermissionMode),
		}
	default:
		return nil, fmt.Errorf("unsupported agent type %q", task.AgentType)
	}
	switch operation {
	case domain.TaskA2AOperationStart:
		request.Worktree.Mode = a2aext.WorktreeCreate
	case domain.TaskA2AOperationRetry:
		request.Worktree.Mode = a2aext.WorktreeRecreate
	case domain.TaskA2AOperationContinue, domain.TaskA2AOperationInteractionResponse:
		request.Worktree.Mode = a2aext.WorktreeResume
		request.Resume = &a2aext.Resume{AgentSessionID: task.AgentSessionID, WorktreePath: task.WorktreePath}
	default:
		return nil, fmt.Errorf("unsupported request operation %q", operation)
	}
	for _, variable := range worker.EnabledRuntimeEnv(task.AgentType) {
		request.Environment.Variables = append(request.Environment.Variables, a2aext.EnvironmentVariable{Key: variable.Key, Value: variable.Value, Sensitive: variable.Sensitive})
	}
	if interaction != nil {
		decision, err := a2aInteractionDecision(interaction.ResponseDecision, interaction.ResponseMessage, interaction.ResponsePayload)
		if err != nil {
			return nil, err
		}
		request.Interaction = &a2aext.Interaction{ID: interaction.ID, Decision: decision, Message: interaction.ResponseMessage, Payload: interaction.ResponsePayload}
	}
	a2aext.NormalizeRequestDefaults(request)
	if err := a2aext.ValidateRequest(request, worker.ID); err != nil {
		return nil, err
	}
	return request, nil
}

func marshalA2AIntentPayload(request *a2aext.ExecutionRequest, text string) ([]byte, error) {
	safe, err := a2aext.SanitizeRequest(request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(a2aIntentPayload{Text: text, Request: *safe})
}

func a2aStartText(task *domain.Task) string {
	if task == nil {
		return "Start task"
	}
	if text := strings.TrimSpace(task.Description); text != "" {
		return text
	}
	if text := strings.TrimSpace(task.Title); text != "" {
		return text
	}
	return "Start task"
}

func a2aInteractionText(interaction *domain.TaskInteraction) string {
	if interaction == nil {
		return "Interaction response"
	}
	if text := strings.TrimSpace(interaction.ResponseMessage); text != "" {
		return text
	}
	if interaction.ResponseDecision != "" {
		return "Interaction decision: " + string(interaction.ResponseDecision)
	}
	return "Interaction response"
}

func a2aInteractionDecision(decision domain.TaskInteractionDecision, message, payload string) (a2aext.InteractionDecision, error) {
	switch decision {
	case domain.TaskInteractionApprove:
		return a2aext.DecisionApprove, nil
	case domain.TaskInteractionApproveForSession:
		return a2aext.DecisionApproveForSession, nil
	case domain.TaskInteractionDeny:
		return a2aext.DecisionDeny, nil
	case domain.TaskInteractionCancel:
		return a2aext.DecisionCancel, nil
	case "":
		if strings.TrimSpace(message) != "" || strings.TrimSpace(payload) != "" {
			return a2aext.DecisionRespond, nil
		}
	}
	return "", fmt.Errorf("unsupported interaction decision %q", decision)
}

func newA2AIdentity(prefix string) string {
	return prefix + "_" + uuid.Must(uuid.NewV7()).String()
}

func nextA2AStartIdentity(rounds []domain.TaskA2ARound) (domain.TaskA2AOperation, string, int) {
	if len(rounds) == 0 {
		return domain.TaskA2AOperationStart, newA2AIdentity("execution"), 1
	}
	attempt := 0
	for _, round := range rounds {
		if round.Attempt > attempt {
			attempt = round.Attempt
		}
	}
	return domain.TaskA2AOperationRetry, newA2AIdentity("execution"), attempt + 1
}

func newA2ARound(task *domain.Task, worker *domain.Worker, operation domain.TaskA2AOperation, executionID string, attempt, turn int, parentRoundID, contextID string, lastSequence int64, now time.Time) (*domain.TaskA2ARound, string, error) {
	commandID := newA2AIdentity("command")
	round, err := domain.NewTaskA2ARound(domain.NewTaskA2ARoundInput{
		ID: newA2AIdentity("round"), TaskID: task.ID, ExecutionID: executionID, Attempt: attempt, Turn: turn,
		Operation: operation, WorkerID: worker.ID, CommandID: commandID, ParentRoundID: parentRoundID,
		ContextID: contextID, LastSequence: lastSequence, Now: now,
	})
	if err != nil {
		return nil, "", err
	}
	return round, commandID, nil
}
