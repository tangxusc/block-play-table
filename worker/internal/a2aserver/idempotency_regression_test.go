package a2aserver

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

type failingSetupHandler struct {
	a2asrv.RequestHandler
	err error
}

func (h failingSetupHandler) SendMessage(context.Context, *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return nil, h.err
}

func (h failingSetupHandler) SendStreamingMessage(context.Context, *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, h.err)
	}
}

func TestIdempotentHandlerAbandonsReservationAfterSynchronousSetupError(t *testing.T) {
	store := newExecutorTestStore(t)
	wantErr := errors.New("SDK setup 失败")
	handler := &idempotentHandler{next: failingSetupHandler{err: wantErr}, store: store, workerID: "worker-integration"}
	request := integrationExecutionRequest("START")
	request.Command.ID = "command-sync-setup-error"
	message := &a2a.SendMessageRequest{Message: integrationExecutionMessage(request)}

	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := handler.SendMessage(ctx, message)
		cancel()
		if !errors.Is(err, wantErr) {
			t.Fatalf("第 %d 次同步 setup 错误 = %v，期望 %v", attempt+1, err, wantErr)
		}
	}
	if _, err := store.LookupCommand(context.Background(), request.Command.ID); err == nil {
		t.Fatal("同步 setup 失败后仍保留 pending command")
	}
}

func TestIdempotentHandlerAbandonsReservationAfterStreamingSetupError(t *testing.T) {
	store := newExecutorTestStore(t)
	wantErr := errors.New("SDK streaming setup 失败")
	handler := &idempotentHandler{next: failingSetupHandler{err: wantErr}, store: store, workerID: "worker-integration"}
	request := integrationExecutionRequest("START")
	request.Command.ID = "command-stream-setup-error"
	message := &a2a.SendMessageRequest{Message: integrationExecutionMessage(request)}

	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		var gotErr error
		for _, streamErr := range handler.SendStreamingMessage(ctx, message) {
			gotErr = streamErr
		}
		cancel()
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("第 %d 次流式 setup 错误 = %v，期望 %v", attempt+1, gotErr, wantErr)
		}
	}
	if _, err := store.LookupCommand(context.Background(), request.Command.ID); err == nil {
		t.Fatal("流式 setup 失败后仍保留 pending command")
	}
}
