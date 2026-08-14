package a2aruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aadapter"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestRuntimeStartThenContinuePreservesSessionAndSequence(t *testing.T) {
	const secret = "top-secret"
	var inputsMu sync.Mutex
	var inputs []a2aadapter.Input
	review := &recordingReview{}
	adapter := runtimeAdapterFunc(func(_ context.Context, input a2aadapter.Input, emit func(a2aadapter.Event)) error {
		inputsMu.Lock()
		inputs = append(inputs, input)
		inputsMu.Unlock()
		switch input.Request.Command.Operation {
		case a2aext.OperationStart:
			emit(a2aadapter.Event{Type: a2aadapter.EventStdout, Content: "prefix-top", AgentSessionID: "session-1"})
			emit(a2aadapter.Event{Type: a2aadapter.EventStdout, Content: "-secret-suffix"})
			emit(a2aadapter.Event{Type: a2aadapter.EventConversation, Content: "answer " + secret})
			emit(a2aadapter.Event{Type: a2aadapter.EventCompleted, Content: "result " + secret})
		case a2aext.OperationContinue:
			if input.AgentSessionID != "session-1" || input.Message != "继续执行" {
				return fmt.Errorf("续接输入不一致: session=%q message=%q", input.AgentSessionID, input.Message)
			}
			emit(a2aadapter.Event{Type: a2aadapter.EventCompleted, Content: "continued", AgentSessionID: "session-1"})
		default:
			return fmt.Errorf("意外 operation: %s", input.Request.Command.Operation)
		}
		return nil
	})
	harness := newRuntimeHarness(t, adapter, review)
	start := runtimeTestRequest()
	start.Project.GitURL = harness.repository
	start.Environment.Variables = []a2aext.EnvironmentVariable{{Key: "TOKEN", Value: secret, Sensitive: true}}
	start.Commands.Pre = []string{"printf pre-command"}
	start.Commands.Post = []string{"printf post-command"}
	first := harness.runTurn(t, start, "a2a-task-start", "a2a-context", "首次执行")
	assertContinuousRuntimeEvents(t, first, 1)
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || !strings.Contains(string(encoded), a2aext.SensitivePlaceholder) {
		t.Fatalf("Runtime 事件脱敏失败: %s", encoded)
	}
	if first[len(first)-1].Event.Type != a2aext.EventExecutionTerminal || first[len(first)-1].Payload["status"] != string(a2aext.TerminalCompleted) {
		t.Fatalf("START 终态 = %+v", first[len(first)-1])
	}
	binding, err := harness.store.GetBinding(context.Background(), start.Scope.ExecutionID)
	if err != nil || binding.State != "COMPLETED" || binding.AgentSessionID != "session-1" || binding.WorktreePath == "" {
		t.Fatalf("START binding = %+v, err %v", binding, err)
	}

	continued := runtimeTestRequest()
	continued.Project.GitURL = harness.repository
	continued.Command = a2aext.Command{ID: "command-continue", Operation: a2aext.OperationContinue, IssuedAt: time.Now().UTC()}
	continued.Scope.Turn = 2
	continued.Worktree.Mode = a2aext.WorktreeResume
	continued.Resume = &a2aext.Resume{AgentSessionID: binding.AgentSessionID, WorktreePath: binding.WorktreePath}
	second := harness.runTurn(t, continued, "a2a-task-continue", "a2a-context", "继续执行")
	assertContinuousRuntimeEvents(t, second, first[len(first)-1].Event.Sequence+1)
	if second[len(second)-1].Payload["status"] != string(a2aext.TerminalCompleted) {
		t.Fatalf("CONTINUE 终态 = %+v", second[len(second)-1])
	}
	continuedBinding, err := harness.store.GetBinding(context.Background(), continued.Scope.ExecutionID)
	if err != nil || continuedBinding.TaskID != "a2a-task-continue" || continuedBinding.Turn != 2 || continuedBinding.ContextID != "a2a-context" || continuedBinding.AgentSessionID != "session-1" || continuedBinding.WorktreePath != binding.WorktreePath {
		t.Fatalf("CONTINUE binding = %+v, err %v", continuedBinding, err)
	}
	inputsMu.Lock()
	inputCount := len(inputs)
	inputsMu.Unlock()
	if inputCount != 2 || review.begins.Load() != 2 || review.ends.Load() != 2 {
		t.Fatalf("turn 调用次数 inputs=%d review=%d/%d", inputCount, review.begins.Load(), review.ends.Load())
	}
}

func TestRuntimeWrappedAgentEventOverflowEndsFailed(t *testing.T) {
	agentCanceled := make(chan struct{})
	adapter := runtimeAdapterFunc(func(ctx context.Context, _ a2aadapter.Input, emit func(a2aadapter.Event)) error {
		emit(a2aadapter.Event{Type: a2aadapter.EventConversation, Content: strings.Repeat("x", a2aext.MaxAgentEventBytes)})
		<-ctx.Done()
		close(agentCanceled)
		return ctx.Err()
	})
	harness := newRuntimeHarness(t, adapter, nil)
	request := runtimeTestRequest()
	request.Project.GitURL = harness.repository
	turn := harness.beginTurn(t, request, "task-event-overflow", "context-event-overflow", "overflow")

	var diagnostic, terminal Update
	for update := range turn.Updates {
		switch update.Event.Event.Type {
		case a2aext.EventExecutionDiagnostic:
			diagnostic = update
		case a2aext.EventExecutionTerminal:
			terminal = update
		}
	}
	select {
	case <-agentCanceled:
	default:
		t.Fatal("事件包装超限后未取消 Agent 子 context")
	}
	if diagnostic.Event == nil || diagnostic.Event.Payload["errorCode"] != string(a2aext.ErrorAgentEventTooLarge) {
		t.Fatalf("超限 diagnostic = %+v", diagnostic.Event)
	}
	if terminal.Event == nil || terminal.State != a2a.TaskStateFailed || terminal.Event.Payload["status"] != string(a2aext.TerminalFailed) {
		t.Fatalf("超限 terminal = %+v", terminal)
	}
	binding, err := harness.store.GetBinding(context.Background(), request.Scope.ExecutionID)
	if err != nil || binding.State != "FAILED" {
		t.Fatalf("超限 binding = %+v, err=%v", binding, err)
	}
}

func TestRuntimeRetryRecreatesExistingWorktree(t *testing.T) {
	adapter := runtimeAdapterFunc(func(_ context.Context, input a2aadapter.Input, emit func(a2aadapter.Event)) error {
		if input.Request.Command.Operation != a2aext.OperationRetry {
			return fmt.Errorf("意外 operation: %s", input.Request.Command.Operation)
		}
		emit(a2aadapter.Event{Type: a2aadapter.EventCompleted, Content: "retried"})
		return nil
	})
	harness := newRuntimeHarness(t, adapter, nil)
	request := runtimeTestRequest()
	request.Project.GitURL = harness.repository
	request.Command = a2aext.Command{ID: "command-retry", Operation: a2aext.OperationRetry, IssuedAt: time.Now().UTC()}
	request.Scope.Attempt = 2
	request.Worktree.Mode = a2aext.WorktreeRecreate
	target := filepath.Join(harness.workDir, worktreeName(request))
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "stale.txt")
	if err := os.WriteFile(marker, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := harness.runTurn(t, request, "a2a-task-retry", "a2a-context-retry", "重试")
	assertContinuousRuntimeEvents(t, events, 1)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RETRY 未移除旧 worktree 内容: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("RETRY 未创建新 worktree: %v", err)
	}
}

func TestRuntimeFailureCodesAndPreCommandFailure(t *testing.T) {
	tests := []struct {
		name     string
		runError error
		code     a2aext.ErrorCode
		pre      []string
	}{
		{name: "Agent 普通失败", runError: errors.New("agent failed")},
		{name: "Agent 事件超限", runError: a2aadapter.ErrEventTooLarge, code: a2aext.ErrorAgentEventTooLarge},
		{name: "前置命令失败", pre: []string{"exit 7"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var adapterCalls atomic.Int32
			adapter := runtimeAdapterFunc(func(context.Context, a2aadapter.Input, func(a2aadapter.Event)) error {
				adapterCalls.Add(1)
				return testCase.runError
			})
			harness := newRuntimeHarness(t, adapter, nil)
			request := runtimeTestRequest()
			request.Project.GitURL = harness.repository
			request.Commands.Pre = testCase.pre
			events := harness.runTurn(t, request, "a2a-task-failed", "a2a-context-failed", "失败路径")
			assertContinuousRuntimeEvents(t, events, 1)
			terminal := events[len(events)-1]
			if terminal.Event.Type != a2aext.EventExecutionTerminal || terminal.Payload["status"] != string(a2aext.TerminalFailed) || terminal.Payload["errorCode"] != string(testCase.code) {
				t.Fatalf("失败终态 = %+v", terminal)
			}
			binding, err := harness.store.GetBinding(context.Background(), request.Scope.ExecutionID)
			if err != nil || binding.State != "FAILED" {
				t.Fatalf("失败 binding = %+v, err %v", binding, err)
			}
			if len(testCase.pre) > 0 && adapterCalls.Load() != 0 {
				t.Fatalf("前置命令失败后仍调用 Adapter: %d", adapterCalls.Load())
			}
		})
	}
}

func TestRuntimeInteractionResponseAndCancel(t *testing.T) {
	for _, decision := range []a2aext.InteractionDecision{a2aext.DecisionRespond, a2aext.DecisionCancel} {
		t.Run(string(decision), func(t *testing.T) {
			adapter := runtimeAdapterFunc(func(ctx context.Context, input a2aadapter.Input, emit func(a2aadapter.Event)) error {
				emit(a2aadapter.Event{Type: a2aadapter.EventStdout, Content: "waiting"})
				interactionID := "interaction-1"
				if decision == a2aext.DecisionRespond {
					interactionID = ""
				}
				response, err := input.RequestInteraction(ctx, a2aadapter.InteractionRequest{
					ID: interactionID, Kind: a2aext.InteractionUserInput, Title: "输入", Body: "请回复", AgentSessionID: "session-interaction",
				})
				if err != nil {
					return err
				}
				if response.Decision == a2aext.DecisionCancel {
					return a2aadapter.ErrInteractionCanceled
				}
				emit(a2aadapter.Event{Type: a2aadapter.EventCompleted, Content: response.Message})
				return nil
			})
			harness := newRuntimeHarness(t, adapter, nil)
			request := runtimeTestRequest()
			request.Project.GitURL = harness.repository
			turn := harness.beginTurn(t, request, "a2a-task-interaction", "a2a-context-interaction", "交互")
			events := []*a2aext.ExecutionEvent{turn.Accepted}
			interactionID := ""
			for {
				update := harness.nextUpdate(t, turn.Updates)
				harness.persistEvent(t, update.Event)
				events = append(events, update.Event)
				if update.Event.Event.Type == a2aext.EventInteractionRequested {
					interactionID, _ = update.Event.Payload["interactionId"].(string)
					break
				}
			}
			if interactionID == "" {
				t.Fatal("交互事件缺少 interactionId")
			}
			if !slices.ContainsFunc(events, func(event *a2aext.ExecutionEvent) bool {
				return event.Event.Type == a2aext.EventAgentSessionStarted
			}) {
				t.Fatal("交互前未发布 Agent Session 绑定事件")
			}
			binding, err := harness.store.GetBinding(context.Background(), request.Scope.ExecutionID)
			if err != nil || binding.AgentSessionID != "session-interaction" {
				t.Fatalf("交互会话绑定=%+v, err=%v", binding, err)
			}
			response := runtimeTestRequest()
			response.Project.GitURL = harness.repository
			response.Command = a2aext.Command{ID: "command-interaction", Operation: a2aext.OperationInteractionResponse, IssuedAt: time.Now().UTC()}
			response.Worktree.Mode = a2aext.WorktreeResume
			response.Resume = &a2aext.Resume{AgentSessionID: binding.AgentSessionID, WorktreePath: binding.WorktreePath}
			response.Interaction = &a2aext.Interaction{ID: interactionID, Decision: decision, Message: "用户回复"}
			if _, err := harness.runtime.ResumeInteraction(context.Background(), response); err != nil {
				t.Fatal(err)
			}
			for update := range turn.Updates {
				harness.persistEvent(t, update.Event)
				events = append(events, update.Event)
			}
			assertContinuousRuntimeEvents(t, events, 1)
			terminalCount := 0
			for _, event := range events {
				if event.Event.Type == a2aext.EventExecutionTerminal {
					terminalCount++
					want := a2aext.TerminalCompleted
					if decision == a2aext.DecisionCancel {
						want = a2aext.TerminalCanceled
					}
					if event.Payload["status"] != string(want) {
						t.Fatalf("交互终态 = %+v", event)
					}
				}
			}
			if terminalCount != 1 {
				t.Fatalf("交互终态数量 = %d", terminalCount)
			}
		})
	}
}

func TestSplitUTF8PreservesRuneBoundaries(t *testing.T) {
	value := strings.Repeat("界", 20) + "tail"
	chunks := splitUTF8(value, 7)
	if strings.Join(chunks, "") != value {
		t.Fatalf("UTF-8 分块未保持原文: %#v", chunks)
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) || len(chunk) > 7 {
			t.Fatalf("UTF-8 分块非法: %q (%d)", chunk, len(chunk))
		}
	}
}

func FuzzSplitUTF8PreservesInput(f *testing.F) {
	f.Add("中文abc", uint8(7))
	f.Add("plain text", uint8(1))
	f.Fuzz(func(t *testing.T, value string, rawLimit uint8) {
		if !utf8.ValidString(value) {
			t.Skip()
		}
		limit := int(rawLimit%64) + utf8.UTFMax
		chunks := splitUTF8(value, limit)
		if strings.Join(chunks, "") != value {
			t.Fatal("分块重组后内容变化")
		}
		for _, chunk := range chunks {
			if !utf8.ValidString(chunk) || len(chunk) > limit {
				t.Fatalf("非法分块 size=%d limit=%d", len(chunk), limit)
			}
		}
	})
}

type runtimeHarness struct {
	runtime    *Runtime
	store      *a2astore.Store
	repository string
	workDir    string
}

func newRuntimeHarness(t *testing.T, adapter a2aadapter.Adapter, review ReviewRecorder) *runtimeHarness {
	t.Helper()
	store, err := a2astore.Open(context.Background(), a2astore.Config{
		Path: filepath.Join(t.TempDir(), "a2a.db"),
		Authenticator: func(context.Context) (string, error) {
			return "runtime-flow-test", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rootCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	workDir := t.TempDir()
	runtime, err := New(Config{
		Context: rootCtx, WorkerID: "worker-1", WorkDir: workDir, Store: store,
		Adapters: map[a2aext.AgentType]a2aadapter.Adapter{a2aext.AgentCodex: adapter}, Review: review,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &runtimeHarness{runtime: runtime, store: store, repository: createRuntimeTestRepository(t), workDir: workDir}
}

func (h *runtimeHarness) beginTurn(t *testing.T, request *a2aext.ExecutionRequest, taskID, contextID, message string) *Turn {
	t.Helper()
	turn, err := h.runtime.Begin(context.Background(), request, message, taskID, contextID)
	if err != nil {
		t.Fatal(err)
	}
	h.persistEvent(t, turn.Accepted)
	if _, err := h.store.Create(context.Background(), &a2a.Task{
		ID: a2a.TaskID(taskID), ContextID: contextID, Status: a2a.TaskStatus{State: a2a.TaskStateSubmitted},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.runtime.Start(turn.ExecutionID); err != nil {
		t.Fatal(err)
	}
	return turn
}

func (h *runtimeHarness) runTurn(t *testing.T, request *a2aext.ExecutionRequest, taskID, contextID, message string) []*a2aext.ExecutionEvent {
	t.Helper()
	turn := h.beginTurn(t, request, taskID, contextID, message)
	events := []*a2aext.ExecutionEvent{turn.Accepted}
	for {
		select {
		case update, open := <-turn.Updates:
			if !open {
				return events
			}
			h.persistEvent(t, update.Event)
			events = append(events, update.Event)
		case <-time.After(5 * time.Second):
			t.Fatal("等待 Runtime 结束超时")
		}
	}
}

func (h *runtimeHarness) nextUpdate(t *testing.T, updates <-chan Update) Update {
	t.Helper()
	select {
	case update, open := <-updates:
		if !open {
			t.Fatal("Runtime 更新流提前关闭")
		}
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("等待 Runtime 更新超时")
		return Update{}
	}
}

func (h *runtimeHarness) persistEvent(t *testing.T, event *a2aext.ExecutionEvent) {
	t.Helper()
	if _, err := h.store.AppendEvent(context.Background(), event); err != nil {
		t.Fatalf("持久化 Runtime event %s/%d: %v", event.Event.Type, event.Event.Sequence, err)
	}
}

type runtimeAdapterFunc func(context.Context, a2aadapter.Input, func(a2aadapter.Event)) error

// Probe 返回测试 Adapter 的固定 readiness 信息。
func (runtimeAdapterFunc) Probe(context.Context) (a2aadapter.Info, error) {
	return a2aadapter.Info{Binary: "test", Version: "test"}, nil
}

// Run 执行测试提供的 Runtime 场景。
func (fn runtimeAdapterFunc) Run(ctx context.Context, input a2aadapter.Input, emit func(a2aadapter.Event)) error {
	return fn(ctx, input, emit)
}

type recordingReview struct {
	begins atomic.Int32
	ends   atomic.Int32
}

// BeginTurn 记录测试中的 review 开始次数。
func (r *recordingReview) BeginTurn(context.Context, string, string, string, string) string {
	return fmt.Sprintf("review-%d", r.begins.Add(1))
}

// EndTurn 记录测试中的 review 结束次数。
func (r *recordingReview) EndTurn(context.Context, string, string) {
	r.ends.Add(1)
}

func assertContinuousRuntimeEvents(t *testing.T, events []*a2aext.ExecutionEvent, first int64) {
	t.Helper()
	for index, event := range events {
		if event.Event.Sequence != first+int64(index) {
			t.Fatalf("event sequence[%d] = %d，期望 %d", index, event.Event.Sequence, first+int64(index))
		}
	}
}
