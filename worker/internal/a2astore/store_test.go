package a2astore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestStoreCreateAndUpdateSanitizeTaskWithoutMutatingRuntimeInput(t *testing.T) {
	store := newTestStore(t)
	historyRequest := testExecutionRequest("command-history", "history-secret")
	statusRequest := testExecutionRequest("command-status", "status-secret")
	artifactRequest := testExecutionRequest("command-artifact", "artifact-secret")
	task := &a2a.Task{
		ID:        "task-sensitive",
		ContextID: "context-sensitive",
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("执行"), a2a.NewDataPart(historyRequest)),
		},
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateWorking,
			Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(statusRequest)),
		},
		Artifacts: []*a2a.Artifact{{
			ID:    "artifact-sensitive",
			Name:  "manifest",
			Parts: a2a.ContentParts{a2a.NewDataPart(artifactRequest)},
		}},
	}

	version, err := store.Create(context.Background(), task)
	if err != nil {
		t.Fatalf("Create() 失败: %v", err)
	}
	assertTaskSanitized(t, store, task.ID, "history-secret", "status-secret", "artifact-secret")
	assertTaskContains(t, task, "history-secret", "status-secret", "artifact-secret")

	updateRequest := testExecutionRequest("command-update", "update-secret")
	task.History = append(task.History, a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(updateRequest)))
	task.Status.State = a2a.TaskStateInputRequired
	nextVersion, err := store.Update(context.Background(), &taskstore.UpdateRequest{Task: task, PrevVersion: version})
	if err != nil {
		t.Fatalf("Update() 失败: %v", err)
	}
	if nextVersion != version+1 {
		t.Fatalf("Update() version = %d，期望 %d", nextVersion, version+1)
	}
	assertTaskSanitized(t, store, task.ID, "history-secret", "status-secret", "artifact-secret", "update-secret")
	assertTaskContains(t, task, "history-secret", "status-secret", "artifact-secret", "update-secret")
}

func TestStoreUpdateRequiresAndComparesPrevVersion(t *testing.T) {
	store := newTestStore(t)
	task := &a2a.Task{
		ID: "task-occ", ContextID: "context-occ",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
	}
	version, err := store.Create(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(context.Background(), &taskstore.UpdateRequest{Task: task}); !errors.Is(err, taskstore.ErrConcurrentModification) {
		t.Fatalf("缺失 PrevVersion 的错误 = %v", err)
	}

	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	versions := make(chan taskstore.TaskVersion, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func(state a2a.TaskState) {
			defer wait.Done()
			candidate := *task
			candidate.Status = a2a.TaskStatus{State: state}
			<-start
			got, updateErr := store.Update(context.Background(), &taskstore.UpdateRequest{Task: &candidate, PrevVersion: version})
			versions <- got
			errorsByCall <- updateErr
		}(a2a.TaskStateInputRequired)
	}
	close(start)
	wait.Wait()
	close(errorsByCall)
	close(versions)

	var succeeded, conflicted int
	for updateErr := range errorsByCall {
		switch {
		case updateErr == nil:
			succeeded++
		case errors.Is(updateErr, taskstore.ErrConcurrentModification):
			conflicted++
		default:
			t.Fatalf("并发 Update() 返回意外错误: %v", updateErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("并发结果 success=%d conflict=%d", succeeded, conflicted)
	}
	for got := range versions {
		if got != taskstore.TaskVersionMissing && got != version+1 {
			t.Fatalf("并发成功 version = %d", got)
		}
	}
	if _, err := store.Update(context.Background(), &taskstore.UpdateRequest{Task: task, PrevVersion: version}); !errors.Is(err, taskstore.ErrConcurrentModification) {
		t.Fatalf("旧 PrevVersion 的错误 = %v", err)
	}
}

func TestCommandInboxPersistsOnlySanitizedRequest(t *testing.T) {
	store := newTestStore(t)
	request := testExecutionRequest("command-inbox", "command-secret")
	record, created, err := store.ClaimCommand(context.Background(), request, "task-inbox", "context-inbox")
	if err != nil || !created {
		t.Fatalf("ClaimCommand() = created %v, err %v", created, err)
	}
	if request.Environment.Variables[0].Value != "command-secret" {
		t.Fatalf("运行时 request 被修改: %+v", request.Environment)
	}
	if record.Request.Environment.Variables[0].Value != a2aext.SensitivePlaceholder {
		t.Fatalf("返回记录未脱敏: %+v", record.Request.Environment)
	}
	var raw []byte
	if err := store.db.QueryRow(`SELECT request_json FROM a2a_command_inbox WHERE owner = ? AND command_id = ?`, "test-owner", request.Command.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "command-secret") || !strings.Contains(string(raw), a2aext.SensitivePlaceholder) {
		t.Fatalf("command inbox 泄漏敏感值: %s", raw)
	}

	replay := testExecutionRequest("command-inbox", "another-secret")
	if existing, replayCreated, err := store.ClaimCommand(context.Background(), replay, "unused-task", "unused-context"); err != nil || replayCreated || existing.TaskID != "task-inbox" {
		t.Fatalf("敏感值变化的幂等重放 = %+v, created %v, err %v", existing, replayCreated, err)
	}
	conflict := testExecutionRequest("command-inbox", "another-secret")
	conflict.Task.Title = "不同任务"
	if _, _, err := store.ClaimCommand(context.Background(), conflict, "unused-task", "unused-context"); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("非敏感内容冲突错误 = %v", err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), Config{
		Path: filepath.Join(t.TempDir(), "a2a.db"),
		Authenticator: func(context.Context) (string, error) {
			return "test-owner", nil
		},
	})
	if err != nil {
		t.Fatalf("Open() 失败: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() 失败: %v", err)
		}
	})
	return store
}

func testExecutionRequest(commandID, secret string) *a2aext.ExecutionRequest {
	return &a2aext.ExecutionRequest{
		Kind: a2aext.RequestKind, Version: a2aext.Version,
		Command: a2aext.Command{ID: commandID, Operation: a2aext.OperationStart, IssuedAt: time.Unix(1, 0).UTC()},
		Scope: a2aext.RequestScope{
			LocalTaskID: "local-task", ExecutionID: "execution", Attempt: 1, Turn: 1, ExpectedWorkerID: "worker",
		},
		Task:     a2aext.TaskSpec{Title: "任务", Description: "描述", BaseBranch: "main"},
		Agent:    a2aext.AgentSpec{Type: a2aext.AgentCodex, WorkMode: a2aext.WorkModeImplement},
		Project:  a2aext.ProjectSpec{ID: "project", GitURL: "https://example.com/repo.git", DefaultBranch: "main", WorktreeNamePrefix: "task"},
		Worktree: a2aext.WorktreeSpec{Mode: a2aext.WorktreeCreate},
		Environment: a2aext.Environment{Variables: []a2aext.EnvironmentVariable{
			{Key: "TOKEN", Value: secret, Sensitive: true},
		}},
	}
}

func assertTaskSanitized(t *testing.T, store *Store, taskID a2a.TaskID, secrets ...string) {
	t.Helper()
	var raw []byte
	if err := store.db.QueryRow(`SELECT task_json FROM a2a_tasks WHERE owner = ? AND task_id = ?`, "test-owner", taskID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("task_json 泄漏 %q: %s", secret, raw)
		}
	}
	if !strings.Contains(string(raw), a2aext.SensitivePlaceholder) {
		t.Fatalf("task_json 缺少脱敏占位符: %s", raw)
	}
	stored, err := store.Get(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored.Task)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("Get() 泄漏 %q: %s", secret, encoded)
		}
	}
}

func assertTaskContains(t *testing.T, task *a2a.Task, values ...string) {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !strings.Contains(string(raw), value) {
			t.Fatalf("运行时 Task 丢失 %q: %s", value, raw)
		}
	}
}
