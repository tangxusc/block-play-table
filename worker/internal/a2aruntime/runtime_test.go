package a2aruntime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2aadapter"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestAuthRequiredKeepsRuntimeOpenUntilCancel(t *testing.T) {
	store, err := a2astore.Open(context.Background(), a2astore.Config{
		Path: filepath.Join(t.TempDir(), "a2a.db"),
		Authenticator: func(context.Context) (string, error) {
			return "runtime-test", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rootCtx, rootCancel := context.WithCancel(context.Background())
	t.Cleanup(rootCancel)
	request := runtimeTestRequest()
	request.Project.GitURL = createRuntimeTestRepository(t)
	runtime, err := New(Config{
		Context: rootCtx, WorkerID: "worker-1", WorkDir: t.TempDir(), Store: store,
		Adapters: map[a2aext.AgentType]a2aadapter.Adapter{
			a2aext.AgentCodex: authRequiredAdapter{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := runtime.Begin(context.Background(), request, "执行任务", "a2a-task-1", "a2a-context-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), &a2a.Task{
		ID: "a2a-task-1", ContextID: "a2a-context-1", Status: a2a.TaskStatus{State: a2a.TaskStateSubmitted},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(turn.ExecutionID); err != nil {
		t.Fatal(err)
	}

	authUpdate := waitForRuntimeUpdate(t, turn.Updates, a2aext.EventExecutionDiagnostic)
	if authUpdate.State != a2a.TaskStateAuthRequired || authUpdate.Event.Payload["errorCode"] != string(a2aext.ErrorAuthRequired) {
		t.Fatalf("认证状态更新 = %+v", authUpdate)
	}
	binding, err := store.GetBinding(context.Background(), request.Scope.ExecutionID)
	if err != nil || binding.State != "AUTH_REQUIRED" {
		t.Fatalf("认证 binding = %+v, err %v", binding, err)
	}
	select {
	case update, open := <-turn.Updates:
		if !open {
			t.Fatal("AUTH_REQUIRED 后 Runtime 更新流提前关闭")
		}
		if update.Event.Event.Type == a2aext.EventExecutionTerminal {
			t.Fatalf("AUTH_REQUIRED 不应产生终态: %+v", update)
		}
	case <-time.After(50 * time.Millisecond):
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Cancel(cancelCtx, request.Scope.ExecutionID); err != nil {
		t.Fatalf("取消 AUTH_REQUIRED Runtime: %v", err)
	}
	select {
	case update, open := <-turn.Updates:
		if open && update.Event.Event.Type == a2aext.EventExecutionTerminal {
			t.Fatalf("Runtime Cancel 不应抢先写标准取消终态: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后 Runtime 更新流未关闭")
	}
}

type authRequiredAdapter struct{}

// Probe 返回测试 Adapter 的固定 readiness 信息。
func (authRequiredAdapter) Probe(context.Context) (a2aadapter.Info, error) {
	return a2aadapter.Info{Binary: "test", Version: "test"}, nil
}

// Run 模拟 CLI 明确要求重新认证。
func (authRequiredAdapter) Run(context.Context, a2aadapter.Input, func(a2aadapter.Event)) error {
	return errors.Join(a2aadapter.ErrAuthRequired, errors.New("not logged in"))
}

func runtimeTestRequest() *a2aext.ExecutionRequest {
	return &a2aext.ExecutionRequest{
		Kind: a2aext.RequestKind, Version: a2aext.Version,
		Command: a2aext.Command{ID: "command-1", Operation: a2aext.OperationStart, IssuedAt: time.Now().UTC()},
		Scope: a2aext.RequestScope{
			LocalTaskID: "local-task-1", ExecutionID: "execution-1", Attempt: 1, Turn: 1, ExpectedWorkerID: "worker-1",
		},
		Task:     a2aext.TaskSpec{Title: "测试任务", Description: "验证认证状态", BaseBranch: "main"},
		Agent:    a2aext.AgentSpec{Type: a2aext.AgentCodex, WorkMode: a2aext.WorkModeImplement},
		Project:  a2aext.ProjectSpec{ID: "project-1", DefaultBranch: "main", WorktreeNamePrefix: "task"},
		Worktree: a2aext.WorktreeSpec{Mode: a2aext.WorktreeCreate},
		Commands: a2aext.Commands{Pre: []string{}, Post: []string{}},
		Environment: a2aext.Environment{
			Variables: []a2aext.EnvironmentVariable{},
		},
	}
}

func createRuntimeTestRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	commands := [][]string{
		{"git", "init", "-b", "main", repository},
		{"git", "-C", repository, "add", "README.md"},
		{"git", "-C", repository, "-c", "user.name=Runtime Test", "-c", "user.email=runtime@example.invalid", "commit", "-m", "init"},
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("runtime test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if output, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("初始化测试 Git 仓库失败: %v: %s", err, output)
		}
	}
	return repository
}

func waitForRuntimeUpdate(t *testing.T, updates <-chan Update, eventType a2aext.EventType) Update {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case update, open := <-updates:
			if !open {
				t.Fatalf("等待 %s 时更新流关闭", eventType)
			}
			if update.Event != nil && update.Event.Event.Type == eventType {
				return update
			}
		case <-timer.C:
			t.Fatalf("等待 %s 超时", eventType)
		}
	}
}
