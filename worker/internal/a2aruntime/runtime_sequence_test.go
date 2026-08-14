package a2aruntime

import (
	"context"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestRuntimeLastSequenceDoesNotReferenceDroppedCanceledUpdate(t *testing.T) {
	harness := newRuntimeHarness(t, runtimeAdapterFunc(nil), nil)
	request := runtimeTestRequest()
	request.Project.GitURL = harness.repository
	turn, err := harness.runtime.Begin(context.Background(), request, "取消竞态", "a2a-task-sequence", "a2a-context-sequence")
	if err != nil {
		t.Fatal(err)
	}
	harness.persistEvent(t, turn.Accepted)
	item := harness.runtime.lookup(request.Scope.ExecutionID)
	item.mu.Lock()
	item.startRequested = true
	item.mu.Unlock()

	// 填满更新缓冲区，确保取消后的下一条事件无法发送。
	for index := 0; index < runtimeUpdateBuffer; index++ {
		harness.runtime.emitEvent(item, a2aext.EventConversationMessage, a2aext.ArtifactConversation, "", map[string]any{
			"role": "assistant", "content": "buffered",
		}, nil)
	}

	cancelResult := make(chan error, 1)
	cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		cancelResult <- harness.runtime.Cancel(cancelCtx, request.Scope.ExecutionID)
	}()
	select {
	case <-item.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Runtime Cancel 未取消 execution context")
	}

	harness.runtime.emitEvent(item, a2aext.EventExecutionDiagnostic, a2aext.ArtifactDiagnostic, a2a.TaskStateWorking, map[string]any{
		"errorCode": "", "message": "取消收尾", "retryable": false,
	}, nil)
	item.finish()
	if err := <-cancelResult; err != nil {
		t.Fatalf("Runtime Cancel 返回错误: %v", err)
	}

	for update := range turn.Updates {
		harness.persistEvent(t, update.Event)
	}
	binding, err := harness.store.GetBinding(context.Background(), request.Scope.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	lastSequence, err := harness.runtime.LastSequence(request.Scope.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if lastSequence != binding.LastSequence {
		t.Fatalf("Runtime LastSequence=%d，但可 drain 并持久化的 sequence=%d", lastSequence, binding.LastSequence)
	}
}
