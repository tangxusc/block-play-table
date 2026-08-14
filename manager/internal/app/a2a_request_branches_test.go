package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestPrepareA2ADispatchRequestRejectsBrokenPersistentAssociations(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	seed := func(t *testing.T, operation domain.TaskA2AOperation, payload []byte, bind bool) (*Service, domain.TaskA2ADispatchIntent) {
		t.Helper()
		storage := store.NewMemoryStore()
		round := &domain.TaskA2ARound{
			ID: "round", TaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1,
			Operation: domain.TaskA2AOperationStart, WorkerID: "worker", CommandID: "command",
			Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if bind {
			round.A2ATaskID, round.ContextID = "remote-task", "context"
		}
		intent := &domain.TaskA2ADispatchIntent{
			ID: "intent", RoundID: round.ID, TaskID: round.TaskID, ExecutionID: round.ExecutionID,
			WorkerID: round.WorkerID, CommandID: "command", Operation: operation, Payload: payload,
			Status: domain.A2ADispatchPending, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := storage.CommitA2ACommand(ctx, store.A2ACommandCommit{Round: round, Intent: intent, CreateRound: true}); err != nil {
			t.Fatal(err)
		}
		return NewService(storage), *intent
	}

	service, intent := seed(t, domain.TaskA2AOperationStart, nil, false)
	missing := intent
	missing.RoundID = "missing"
	if _, err := service.PrepareA2ADispatchRequest(ctx, missing); !errors.Is(err, ErrA2AProjection) {
		t.Fatalf("round 缺失错误 = %v", err)
	}
	for _, mutate := range []func(*domain.TaskA2ADispatchIntent){
		func(value *domain.TaskA2ADispatchIntent) { value.TaskID = "other" },
		func(value *domain.TaskA2ADispatchIntent) { value.ExecutionID = "other" },
		func(value *domain.TaskA2ADispatchIntent) { value.WorkerID = "other" },
	} {
		changed := intent
		mutate(&changed)
		if _, err := service.PrepareA2ADispatchRequest(ctx, changed); !errors.Is(err, ErrA2AProtocolConflict) {
			t.Fatalf("关联漂移错误 = %v", err)
		}
	}

	cancelService, cancel := seed(t, domain.TaskA2AOperationCancel, nil, false)
	if _, err := cancelService.PrepareA2ADispatchRequest(ctx, cancel); !errors.Is(err, ErrA2AProtocolConflict) {
		t.Fatalf("未绑定 cancel 错误 = %v", err)
	}
	boundService, boundCancel := seed(t, domain.TaskA2AOperationCancel, nil, true)
	request, err := boundService.PrepareA2ADispatchRequest(ctx, boundCancel)
	if err != nil || request.Round.A2ATaskID != "remote-task" {
		t.Fatalf("绑定 cancel = %+v, %v", request, err)
	}

	malformedService, malformed := seed(t, domain.TaskA2AOperationStart, []byte("{"), false)
	if _, err := malformedService.PrepareA2ADispatchRequest(ctx, malformed); !errors.Is(err, ErrA2AProtocolConflict) {
		t.Fatalf("畸形 payload 错误 = %v", err)
	}
}

func TestPrepareA2ADispatchRequestRestoresOnlyAvailableSensitiveValues(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 20, 30, 0, 0, time.UTC)
	project := &domain.Project{ID: "project", GitURL: "https://example.test/repo.git", DefaultBranch: "main", WorktreeNamePrefix: "task"}
	task := &domain.Task{ID: "task", Title: "Task", AgentType: domain.AgentCodex}
	round := &domain.TaskA2ARound{
		ID: "round", TaskID: task.ID, ExecutionID: "execution", Attempt: 1, Turn: 1,
		Operation: domain.TaskA2AOperationStart, WorkerID: "worker", CommandID: "command",
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	worker := &domain.Worker{
		ID: "worker", AgentRuntimeEnv: []domain.WorkerAgentRuntimeEnv{{AgentType: domain.AgentCodex, Vars: []domain.AgentRuntimeEnvVar{
			{Key: "PUBLIC", Value: "visible", Enabled: true},
			{Key: "TOKEN", Value: "secret", Enabled: true, Sensitive: true},
		}}},
	}
	request, err := buildA2ARequest(task, project, worker, round, "command", domain.TaskA2AOperationStart, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := marshalA2AIntentPayload(request, "start")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(t *testing.T, savedWorker *domain.Worker) (*Service, domain.TaskA2ADispatchIntent) {
		t.Helper()
		storage := store.NewMemoryStore()
		intent := &domain.TaskA2ADispatchIntent{
			ID: "intent", RoundID: round.ID, TaskID: round.TaskID, ExecutionID: round.ExecutionID,
			WorkerID: round.WorkerID, CommandID: round.CommandID, Operation: domain.TaskA2AOperationStart,
			Payload: payload, Status: domain.A2ADispatchPending, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		copyRound := *round
		if err := storage.CommitA2ACommand(ctx, store.A2ACommandCommit{Round: &copyRound, Intent: intent, CreateRound: true}); err != nil {
			t.Fatal(err)
		}
		if savedWorker != nil {
			if err := storage.SaveWorker(ctx, savedWorker); err != nil {
				t.Fatal(err)
			}
		}
		return NewService(storage), *intent
	}

	missingWorkerService, missingWorkerIntent := seed(t, nil)
	if _, err := missingWorkerService.PrepareA2ADispatchRequest(ctx, missingWorkerIntent); !errors.Is(err, ErrA2AProjection) {
		t.Fatalf("Worker 缺失错误 = %v", err)
	}
	withoutSecret := *worker
	withoutSecret.AgentRuntimeEnv = []domain.WorkerAgentRuntimeEnv{{AgentType: domain.AgentCodex, Vars: []domain.AgentRuntimeEnvVar{{Key: "PUBLIC", Value: "visible", Enabled: true}}}}
	missingSecretService, missingSecretIntent := seed(t, &withoutSecret)
	if _, err := missingSecretService.PrepareA2ADispatchRequest(ctx, missingSecretIntent); !errors.Is(err, ErrA2AProtocolConflict) {
		t.Fatalf("敏感变量缺失错误 = %v", err)
	}
	emptySecret := *worker
	emptySecret.AgentRuntimeEnv = []domain.WorkerAgentRuntimeEnv{{AgentType: domain.AgentCodex, Vars: []domain.AgentRuntimeEnvVar{{Key: "TOKEN", Enabled: true, Sensitive: true}}}}
	emptySecretService, emptySecretIntent := seed(t, &emptySecret)
	if _, err := emptySecretService.PrepareA2ADispatchRequest(ctx, emptySecretIntent); !errors.Is(err, ErrA2AProtocolConflict) {
		t.Fatalf("敏感变量空值错误 = %v", err)
	}
	validService, validIntent := seed(t, worker)
	prepared, err := validService.PrepareA2ADispatchRequest(ctx, validIntent)
	if err != nil || len(prepared.Request.Environment.Variables) != 2 {
		t.Fatalf("恢复请求 = %+v, %v", prepared, err)
	}
	if prepared.Request.Environment.Variables[0].Value != "visible" || prepared.Request.Environment.Variables[1].Value != "secret" {
		t.Fatalf("环境变量恢复错误: %+v", prepared.Request.Environment.Variables)
	}
}

func TestA2ARequestInteractionAndTextFallbackBranches(t *testing.T) {
	now := time.Date(2026, 8, 14, 21, 0, 0, 0, time.UTC)
	project := &domain.Project{ID: "project", GitURL: "https://example.test/repo.git", DefaultBranch: "main", WorktreeNamePrefix: "task"}
	worker := &domain.Worker{ID: "worker"}
	round := &domain.TaskA2ARound{ExecutionID: "execution", Attempt: 1, Turn: 2, CreatedAt: now}
	task := &domain.Task{ID: "task", Title: "Task", AgentType: domain.AgentCodex, AgentSessionID: "session", WorktreePath: "/tmp/worktree"}
	interaction := &domain.TaskInteraction{ID: "interaction", ResponsePayload: `{"answer":"yes"}`}
	request, err := buildA2ARequest(task, project, worker, round, "command", domain.TaskA2AOperationInteractionResponse, interaction)
	if err != nil || request.Interaction == nil || request.Interaction.Decision != a2aext.DecisionRespond {
		t.Fatalf("payload-only interaction = %+v, %v", request, err)
	}
	interaction.ResponseDecision = domain.TaskInteractionDecision("INVALID")
	if _, err := buildA2ARequest(task, project, worker, round, "command", domain.TaskA2AOperationInteractionResponse, interaction); err == nil {
		t.Fatal("非法交互决策未失败")
	}

	if got := a2aStartText(nil); got != "Start task" {
		t.Fatalf("nil start text = %q", got)
	}
	if got := a2aStartText(&domain.Task{Title: " title "}); got != "title" {
		t.Fatalf("title start text = %q", got)
	}
	if got := a2aStartText(&domain.Task{}); got != "Start task" {
		t.Fatalf("empty start text = %q", got)
	}
	if got := a2aInteractionText(&domain.TaskInteraction{}); got != "Interaction response" {
		t.Fatalf("empty interaction text = %q", got)
	}
	if _, err := a2aInteractionDecision("", "", ""); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("空交互决策错误 = %v", err)
	}
}
