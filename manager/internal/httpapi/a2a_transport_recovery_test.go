package httpapi

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

// TestReconcileA2ATaskRecoversWhenExecutionFinishesBeforeSubscribe 验证 GetTask 与订阅之间的终态竞态会重新对账。
func TestReconcileA2ATaskRecoversWhenExecutionFinishesBeforeSubscribe(t *testing.T) {
	round := domain.TaskA2ARound{
		TaskID: "task-subscribe-race", ExecutionID: "execution-subscribe-race",
		Attempt: 1, Turn: 1, WorkerID: "worker-subscribe-race",
		A2ATaskID: "remote-subscribe-race", ContextID: "context-subscribe-race",
	}
	terminalEvent := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, testA2ATime(), map[string]any{
		"status": string(a2aext.TerminalCompleted), "result": "done",
	})
	terminalMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(terminalEvent))
	terminalMessage.TaskID = a2a.TaskID(round.A2ATaskID)
	terminalMessage.ContextID = round.ContextID
	terminalMessage.Extensions = []string{a2aext.ExtensionURI}
	transport := &subscribeCompletionRaceTransport{
		working: &a2a.Task{
			ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		},
		terminal: &a2a.Task{
			ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: terminalMessage},
			Artifacts: []*a2a.Artifact{validA2ATestArtifact(t, round, terminalEvent, a2aext.ArtifactManifest)},
		},
	}
	client := newTerminalPairA2AClient(t, round, transport)

	var updates []app.A2ARemoteUpdate
	err := reconcileA2ATask(t.Context(), client, round, func(_ context.Context, update app.A2ARemoteUpdate) error {
		updates = append(updates, update)
		return nil
	})
	if err != nil {
		t.Fatalf("订阅竞态恢复失败: %v", err)
	}
	if transport.getCalls != 2 || transport.subscribeCalls != 1 {
		t.Fatalf("GetTask 调用 %d 次，Subscribe 调用 %d 次", transport.getCalls, transport.subscribeCalls)
	}
	if len(updates) != 2 || updates[0].Status != domain.TaskA2ARemoteStatusWorking ||
		updates[1].Status != domain.TaskA2ARemoteStatusCompleted || updates[1].Event == nil ||
		updates[1].Event.Event.ID != terminalEvent.Event.ID {
		t.Fatalf("订阅竞态恢复投影 = %+v", updates)
	}
}

// TestReconcileA2AAuthRequiredDoesNotResubscribe 验证等待外部认证时停止无效订阅与轮询。
func TestReconcileA2AAuthRequiredDoesNotResubscribe(t *testing.T) {
	round := domain.TaskA2ARound{
		TaskID: "task-auth-required", ExecutionID: "execution-auth-required",
		Attempt: 1, Turn: 1, WorkerID: "worker-auth-required",
		A2ATaskID: "remote-auth-required", ContextID: "context-auth-required",
	}
	transport := &authRequiredA2ATransport{task: &a2a.Task{
		ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateAuthRequired},
	}}
	client := newTerminalPairA2AClient(t, round, transport)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	var updates []app.A2ARemoteUpdate
	err := reconcileA2ATask(ctx, client, round, func(_ context.Context, update app.A2ARemoteUpdate) error {
		updates = append(updates, update)
		return nil
	})
	if err != nil {
		t.Fatalf("AUTH_REQUIRED 对账未收敛: %v", err)
	}
	if transport.getCalls != 1 || transport.subscribeCalls != 0 {
		t.Fatalf("GetTask 调用 %d 次，Subscribe 调用 %d 次", transport.getCalls, transport.subscribeCalls)
	}
	if len(updates) != 1 || updates[0].Status != domain.TaskA2ARemoteStatusAuthRequired {
		t.Fatalf("AUTH_REQUIRED 投影 = %+v", updates)
	}
}

type authRequiredA2ATransport struct {
	a2aclient.Transport
	task           *a2a.Task
	getCalls       int
	subscribeCalls int
}

func (t *authRequiredA2ATransport) GetTask(context.Context, a2aclient.ServiceParams, *a2a.GetTaskRequest) (*a2a.Task, error) {
	t.getCalls++
	return t.task, nil
}

func (t *authRequiredA2ATransport) SubscribeToTask(context.Context, a2aclient.ServiceParams, *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	t.subscribeCalls++
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, a2a.ErrTaskNotFound)
	}
}

type subscribeCompletionRaceTransport struct {
	a2aclient.Transport
	working        *a2a.Task
	terminal       *a2a.Task
	getCalls       int
	subscribeCalls int
}

func (t *subscribeCompletionRaceTransport) GetTask(context.Context, a2aclient.ServiceParams, *a2a.GetTaskRequest) (*a2a.Task, error) {
	t.getCalls++
	if t.getCalls == 1 {
		return t.working, nil
	}
	return t.terminal, nil
}

func (t *subscribeCompletionRaceTransport) SubscribeToTask(context.Context, a2aclient.ServiceParams, *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	t.subscribeCalls++
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, a2a.ErrTaskNotFound)
	}
}

// TestValidateWorkerAgentCardRejectsAmbiguousInterfaces 验证 SDK 只能看到注册能力声明的唯一接口。
func TestValidateWorkerAgentCardRejectsAmbiguousInterfaces(t *testing.T) {
	target := workerA2ATarget{
		worker: &domain.Worker{ID: "worker-card-interface"}, host: "127.0.0.1", port: 39126,
		endpointPath: "/a2a", cardPath: "/.well-known/agent-card.json",
	}
	tests := map[string]func(*a2a.AgentCard){
		"nil interface": func(card *a2a.AgentCard) {
			card.SupportedInterfaces = append([]*a2a.AgentInterface{nil}, card.SupportedInterfaces...)
		},
		"additional external interface": func(card *a2a.AgentCard) {
			external := a2a.NewAgentInterface("http://192.0.2.10:39126/a2a", a2a.TransportProtocolJSONRPC)
			card.SupportedInterfaces = append([]*a2a.AgentInterface{external}, card.SupportedInterfaces...)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			card := validA2ATestCard(target)
			mutate(card)
			if err := validateWorkerAgentCard(card, target); err == nil {
				t.Fatal("含歧义接口的 Agent Card 未被拒绝")
			}
		})
	}
}
