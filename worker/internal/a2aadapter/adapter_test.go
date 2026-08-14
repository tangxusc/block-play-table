package a2aadapter

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestAgentEventScannerEnforcesRawEventBoundary(t *testing.T) {
	exact := strings.Repeat("x", a2aext.MaxAgentEventBytes)
	scanner := newAgentEventScanner(strings.NewReader(exact + "\n"))
	if !scanner.Scan() || len(scanner.Text()) != a2aext.MaxAgentEventBytes || scanner.Err() != nil {
		t.Fatalf("16 MiB 边界事件失败: scan=%v size=%d err=%v", scanner.Scan(), len(scanner.Text()), scanner.Err())
	}
	scanner = newAgentEventScanner(strings.NewReader(exact + "x\n"))
	if scanner.Scan() || !errors.Is(scanner.Err(), ErrEventTooLarge) {
		t.Fatalf("16 MiB + 1 应明确失败: scan=%v err=%v", scanner.Scan(), scanner.Err())
	}
}

func TestCLIAdapterAuthenticationCaptureIsBounded(t *testing.T) {
	window := newBoundedTextWindow(16)
	window.Append("0123456789")
	window.Append("abcdefghij")
	if len(window.data) != 16 || window.String() != "456789abcdefghij" {
		t.Fatalf("滚动窗口 = %q (%d)", window.String(), len(window.data))
	}
	window.Append(strings.Repeat("z", 32))
	if len(window.data) != 16 || window.String() != strings.Repeat("z", 16) {
		t.Fatalf("超长追加后的滚动窗口 = %q (%d)", window.String(), len(window.data))
	}

	adapter := &cliAdapter{agent: cliAgentFunc(func(_ context.Context, _ Input, emit func(Event)) error {
		emit(Event{Type: EventStderr, Content: "authentication required"})
		for range 8 {
			emit(Event{Type: EventStdout, Content: strings.Repeat("x", authenticationOutputWindowBytes)})
		}
		return errors.New("agent exited")
	})}
	if err := adapter.Run(context.Background(), validAdapterInput(t, a2aext.AgentCodex), func(Event) {}); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("早期认证错误在滚动后丢失: %v", err)
	}
}

func TestCodexStderrScannerFailureStopsRun(t *testing.T) {
	rpc := &codexRPC{
		cmd:        &exec.Cmd{},
		stderrDone: make(chan struct{}),
		state:      newCodexRunState(),
		emit:       func(Event) {},
	}
	rpc.readStderr(strings.NewReader(strings.Repeat("x", a2aext.MaxAgentEventBytes+1) + "\n"))
	_, err := rpc.state.wait(context.Background())
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("Codex stderr 超限错误 = %v", err)
	}
}

func TestCLIAdapterMapsAuthenticationAndValidatesInput(t *testing.T) {
	adapter := &cliAdapter{binary: "unused", agent: cliAgentFunc(func(_ context.Context, _ Input, emit func(Event)) error {
		emit(Event{Type: EventStderr, Content: "Not logged in; please run codex login"})
		return errors.New("exit status 1")
	})}
	err := adapter.Run(context.Background(), validAdapterInput(t, a2aext.AgentCodex), func(Event) {})
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("认证错误映射 = %v", err)
	}
	if err := adapter.Run(nil, Input{}, nil); err == nil {
		t.Fatal("缺少 Adapter 参数应失败")
	}
}

func TestClaudeAdapterRunsStreamJSONAndMapsDefaultPermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 CLI 脚本仅用于 Unix 测试")
	}
	binary := writeScript(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "claude-test 1.0"
  exit 0
fi
printf '%s\n' '{"type":"system","session_id":"claude-session"}'
printf '%s\n' '{"type":"assistant","session_id":"claude-session","message":{"content":[{"type":"text","text":"assistant answer"}]}}'
printf '%s\n' '{"type":"result","session_id":"claude-session","result":"final answer"}'
`)
	adapter := NewClaude(binary)
	if info, err := adapter.Probe(context.Background()); err != nil || info.Version != "claude-test 1.0" {
		t.Fatalf("Claude Probe = %+v, %v", info, err)
	}
	input := validAdapterInput(t, a2aext.AgentClaude)
	input.Request.Agent.Config.PermissionMode = "default"
	var events []Event
	if err := adapter.Run(context.Background(), input, func(event Event) { events = append(events, event) }); err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, EventConversation, "assistant answer") || !hasEvent(events, EventCompleted, "final answer") {
		t.Fatalf("Claude events = %+v", events)
	}
	args := claudeCommandArgs(a2aext.AgentConfig{PermissionMode: "default"}, "session", "message", false, nil)
	if slices.Contains(args, "--permission-mode") || slices.Contains(args, "default") {
		t.Fatalf("Claude default permission 不应覆盖 CLI 默认值: %#v", args)
	}
	args = claudeCommandArgs(a2aext.AgentConfig{PermissionMode: "plan"}, "session", "message", false, nil)
	if !slices.Contains(args, "--permission-mode") || !slices.Contains(args, "plan") {
		t.Fatalf("Claude plan permission 参数 = %#v", args)
	}
}

func TestClaudeInteractionCancelStopsWithoutResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 CLI 脚本仅用于 Unix 测试")
	}
	binary := writeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"system","session_id":"claude-session"}'
printf '%s\n' '{"type":"result","session_id":"claude-session","result":"permission needed","permission_denials":[{"tool_name":"Bash","tool_use_id":"tool-1","tool_input":{"command":"make test"}}]}'
`)
	input := validAdapterInput(t, a2aext.AgentClaude)
	interactions := 0
	input.RequestInteraction = func(_ context.Context, request InteractionRequest) (InteractionResponse, error) {
		interactions++
		if request.Kind != a2aext.InteractionCommandApproval {
			t.Fatalf("interaction = %+v", request)
		}
		return InteractionResponse{Decision: a2aext.DecisionCancel}, nil
	}
	err := NewClaude(binary).Run(context.Background(), input, func(Event) {})
	if !errors.Is(err, ErrInteractionCanceled) || interactions != 1 {
		t.Fatalf("Claude CANCEL err=%v interactions=%d", err, interactions)
	}
}

func TestClaudeStreamingPermissionDenialStopsAndResumes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 CLI 脚本仅用于 Unix 测试")
	}
	binary := writeScript(t, `#!/bin/sh
case "$*" in
  *"The user approved"*)
    printf '%s\n' '{"type":"result","session_id":"claude-session","result":"completed after streaming approval"}'
    ;;
  *)
    printf '%s\n' '{"type":"system","subtype":"init","session_id":"claude-session"}'
    printf '%s\n' '{"type":"assistant","session_id":"claude-session","message":{"content":[{"type":"tool_use","id":"tool-stream","name":"Bash","input":{"command":"printf approved > result.txt"}}]}}'
    printf '%s\n' '{"type":"system","subtype":"permission_denied","session_id":"claude-session","tool_name":"Bash","tool_use_id":"tool-stream","message":"blocked"}'
    sleep 30
    ;;
esac
`)
	input := validAdapterInput(t, a2aext.AgentClaude)
	interactions := 0
	input.RequestInteraction = func(_ context.Context, request InteractionRequest) (InteractionResponse, error) {
		interactions++
		if request.Kind != a2aext.InteractionCommandApproval || !strings.Contains(request.Body, "printf approved > result.txt") {
			t.Fatalf("流式拒绝未关联 tool_use: %+v", request)
		}
		return InteractionResponse{Decision: a2aext.DecisionApprove}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var events []Event
	if err := NewClaude(binary).Run(ctx, input, func(event Event) { events = append(events, event) }); err != nil {
		t.Fatal(err)
	}
	if interactions != 1 || !hasEvent(events, EventCompleted, "completed after streaming approval") {
		t.Fatalf("流式审批 interactions=%d events=%+v", interactions, events)
	}
}

func TestCodexAdapterRunsAppServerAndFailsOnEarlyExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 CLI 脚本仅用于 Unix 测试")
	}
	t.Run("完成", func(t *testing.T) {
		binary := writeScript(t, codexScript(`printf '%s\n' '{"method":"item/completed","params":{"threadId":"codex-thread","turnId":"turn-1","item":{"type":"agentMessage","text":"codex answer"}}}'
printf '%s\n' '{"method":"turn/completed","params":{"threadId":"codex-thread","turn":{"id":"turn-1","status":"completed"}}}'`))
		var events []Event
		err := NewCodex(binary).Run(context.Background(), validAdapterInput(t, a2aext.AgentCodex), func(event Event) { events = append(events, event) })
		if err != nil {
			t.Fatal(err)
		}
		if !hasEvent(events, EventConversation, "codex answer") || !hasEvent(events, EventCompleted, "codex answer") {
			t.Fatalf("Codex events = %+v", events)
		}
	})
	t.Run("最终通知后立即退出", func(t *testing.T) {
		binary := writeScript(t, codexScript(`printf '%s\n' '{"method":"item/completed","params":{"threadId":"codex-thread","turnId":"turn-1","item":{"type":"agentMessage","text":"codex answer"}}}'
printf '%s\n' '{"method":"turn/completed","params":{"threadId":"codex-thread","turn":{"id":"turn-1","status":"completed"}}}'
exit 0`))
		const attempts = 24
		inputs := make([]Input, attempts)
		for attempt := range inputs {
			inputs[attempt] = validAdapterInput(t, a2aext.AgentCodex)
		}
		type runResult struct {
			attempt int
			events  []Event
			err     error
		}
		results := make(chan runResult, attempts)
		for attempt, input := range inputs {
			go func() {
				var events []Event
				err := NewCodex(binary).Run(context.Background(), input, func(event Event) { events = append(events, event) })
				results <- runResult{attempt: attempt, events: events, err: err}
			}()
		}
		for range attempts {
			result := <-results
			if result.err != nil {
				t.Fatalf("第 %d 次快速退出失败: %v", result.attempt+1, result.err)
			}
			if !hasEvent(result.events, EventConversation, "codex answer") || !hasEvent(result.events, EventCompleted, "codex answer") {
				t.Fatalf("第 %d 次 Codex events = %+v", result.attempt+1, result.events)
			}
		}
	})
	t.Run("turn 中途退出", func(t *testing.T) {
		binary := writeScript(t, codexScript(`exit 7`))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := NewCodex(binary).Run(ctx, validAdapterInput(t, a2aext.AgentCodex), func(Event) {})
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Codex 中途退出 err=%v", err)
		}
	})
	t.Run("等待交互时退出", func(t *testing.T) {
		binary := writeScript(t, codexScript(`printf '%s\n' '{"id":99,"method":"item/commandExecution/requestApproval","params":{"threadId":"codex-thread","turnId":"turn-1","itemId":"item-1","command":"make test"}}'
exit 8`))
		input := validAdapterInput(t, a2aext.AgentCodex)
		input.RequestInteraction = func(ctx context.Context, _ InteractionRequest) (InteractionResponse, error) {
			<-ctx.Done()
			return InteractionResponse{}, ctx.Err()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := NewCodex(binary).Run(ctx, input, func(Event) {})
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Codex 交互期间退出 err=%v", err)
		}
	})
	t.Run("畸形 JSON", func(t *testing.T) {
		binary := writeScript(t, codexScript(`printf '%s\n' 'not-json'
sleep 5`))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := NewCodex(binary).Run(ctx, validAdapterInput(t, a2aext.AgentCodex), func(Event) {})
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Codex 畸形 JSON err=%v", err)
		}
	})
}

func TestCodexInteractionCancelReturnsSentinel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("伪 CLI 脚本仅用于 Unix 测试")
	}
	binary := writeScript(t, codexScript(`printf '%s\n' '{"id":99,"method":"item/commandExecution/requestApproval","params":{"threadId":"codex-thread","turnId":"turn-1","itemId":"item-1","command":"make test"}}'
sleep 5`))
	input := validAdapterInput(t, a2aext.AgentCodex)
	input.RequestInteraction = func(_ context.Context, request InteractionRequest) (InteractionResponse, error) {
		if request.Kind != a2aext.InteractionCommandApproval {
			t.Fatalf("interaction = %+v", request)
		}
		return InteractionResponse{Decision: a2aext.DecisionCancel}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := NewCodex(binary).Run(ctx, input, func(Event) {})
	if !errors.Is(err, ErrInteractionCanceled) {
		t.Fatalf("Codex CANCEL err=%v", err)
	}
}

type cliAgentFunc func(context.Context, Input, func(Event)) error

func (fn cliAgentFunc) run(ctx context.Context, input Input, emit func(Event)) error {
	return fn(ctx, input, emit)
}

func validAdapterInput(t *testing.T, agentType a2aext.AgentType) Input {
	t.Helper()
	request := &a2aext.ExecutionRequest{
		Kind: a2aext.RequestKind, Version: a2aext.Version,
		Command:  a2aext.Command{ID: "command-1", Operation: a2aext.OperationStart, IssuedAt: time.Now().UTC()},
		Scope:    a2aext.RequestScope{LocalTaskID: "task-1", ExecutionID: "execution-1", Attempt: 1, Turn: 1, ExpectedWorkerID: "worker-1"},
		Task:     a2aext.TaskSpec{Title: "Test task", Description: "Complete the test", BaseBranch: "main"},
		Agent:    a2aext.AgentSpec{Type: agentType, WorkMode: a2aext.WorkModeImplement},
		Project:  a2aext.ProjectSpec{ID: "project-1", GitURL: "https://example.com/repo.git", DefaultBranch: "main", WorktreeNamePrefix: "test"},
		Worktree: a2aext.WorktreeSpec{Mode: a2aext.WorktreeCreate}, Commands: a2aext.Commands{Pre: []string{}, Post: []string{}},
		Environment: a2aext.Environment{Variables: []a2aext.EnvironmentVariable{}},
	}
	return Input{Request: request, WorktreePath: t.TempDir(), Environment: map[string]string{}}
}

func hasEvent(events []Event, eventType EventType, content string) bool {
	for _, event := range events {
		if event.Type == eventType && event.Content == content {
			return true
		}
	}
	return false
}

func writeScript(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/agent-cli"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexScript(afterTurn string) string {
	return `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex-test 1.0"
  exit 0
fi
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"thread/start"'*) printf '%s\n' '{"id":2,"result":{"thread":{"id":"codex-thread"}}}' ;;
    *'"method":"thread/resume"'*) printf '%s\n' '{"id":2,"result":{"thread":{"id":"codex-thread"}}}' ;;
    *'"method":"turn/start"'*)
      printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-1"}}}'
      ` + afterTurn + `
      ;;
  esac
done
`
}
