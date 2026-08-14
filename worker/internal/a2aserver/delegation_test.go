package a2aserver

import (
	"context"
	"errors"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestIdempotentHandlerDelegatesStandardReadCancelAndPushMethods(t *testing.T) {
	handler := &idempotentHandler{next: fuzzA2ARequestHandler{}}
	ctx := context.Background()
	if _, err := handler.GetTask(ctx, &a2a.GetTaskRequest{}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("GetTask 错误=%v", err)
	}
	if result, err := handler.ListTasks(ctx, &a2a.ListTasksRequest{}); err != nil || result == nil {
		t.Fatalf("ListTasks=%+v err=%v", result, err)
	}
	if _, err := handler.CancelTask(ctx, &a2a.CancelTaskRequest{}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("CancelTask 错误=%v", err)
	}
	var subscribeErr error
	for _, err := range handler.SubscribeToTask(ctx, &a2a.SubscribeToTaskRequest{}) {
		subscribeErr = err
	}
	if !errors.Is(subscribeErr, a2a.ErrTaskNotFound) {
		t.Fatalf("SubscribeToTask 错误=%v", subscribeErr)
	}
	if _, err := handler.GetTaskPushConfig(ctx, &a2a.GetTaskPushConfigRequest{}); !errors.Is(err, a2a.ErrPushNotificationNotSupported) {
		t.Fatalf("GetTaskPushConfig 错误=%v", err)
	}
	if _, err := handler.ListTaskPushConfigs(ctx, &a2a.ListTaskPushConfigRequest{}); !errors.Is(err, a2a.ErrPushNotificationNotSupported) {
		t.Fatalf("ListTaskPushConfigs 错误=%v", err)
	}
	if _, err := handler.CreateTaskPushConfig(ctx, &a2a.PushConfig{}); !errors.Is(err, a2a.ErrPushNotificationNotSupported) {
		t.Fatalf("CreateTaskPushConfig 错误=%v", err)
	}
	if err := handler.DeleteTaskPushConfig(ctx, &a2a.DeleteTaskPushConfigRequest{}); !errors.Is(err, a2a.ErrPushNotificationNotSupported) {
		t.Fatalf("DeleteTaskPushConfig 错误=%v", err)
	}
	if _, err := handler.GetExtendedAgentCard(ctx, &a2a.GetExtendedAgentCardRequest{}); !errors.Is(err, a2a.ErrMethodNotFound) {
		t.Fatalf("GetExtendedAgentCard 错误=%v", err)
	}
}

func TestServerCardAndErrorsReturnSafeSnapshots(t *testing.T) {
	var nilServer *Server
	if nilServer.Errors() != nil || nilServer.Card() != nil {
		t.Fatal("nil Server 应返回 nil")
	}
	server := &Server{errCh: make(chan error, 1), card: &a2a.AgentCard{Name: "worker"}}
	if server.Errors() == nil {
		t.Fatal("Errors 通道不能为空")
	}
	card := server.Card()
	if card == nil || card.Name != "worker" || card == server.card {
		t.Fatalf("Card 快照=%+v", card)
	}
	card.Name = "mutated"
	if server.card.Name != "worker" {
		t.Fatal("Card 返回值修改了 Server 内部状态")
	}
	server.card = nil
	if server.Card() != nil {
		t.Fatal("空内部 Card 应返回 nil")
	}
}
