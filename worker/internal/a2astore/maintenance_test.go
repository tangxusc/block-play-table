package a2astore

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestFailNonTerminalAtomicallyPersistsRestartDiagnostic(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	store := newTestStoreAt(t, now)
	working := &a2a.Task{
		ID: "task-working", ContextID: "context-working",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
	}
	completed := &a2a.Task{
		ID: "task-completed", ContextID: "context-completed",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
	if _, err := store.Create(context.Background(), working); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), completed); err != nil {
		t.Fatal(err)
	}
	binding := RuntimeBinding{
		ExecutionID: "execution-restart", LocalTaskID: "local-restart", WorkerID: "worker-restart",
		TaskID: string(working.ID), ContextID: working.ContextID, Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, AgentSessionID: "session-restart", WorktreePath: "/tmp/worktree-restart", State: "WORKING",
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	accepted := testStoreEvent(a2aext.EventExecutionAccepted, 1, now.Add(-time.Minute))
	accepted.Scope = a2aext.EventScope{
		LocalTaskID: binding.LocalTaskID, ExecutionID: binding.ExecutionID,
		Attempt: binding.Attempt, Turn: binding.Turn, WorkerID: binding.WorkerID,
	}
	if _, err := store.AppendEvent(context.Background(), accepted); err != nil {
		t.Fatal(err)
	}

	failed, err := store.FailNonTerminal(context.Background())
	if err != nil {
		t.Fatalf("FailNonTerminal() 失败: %v", err)
	}
	if failed != 1 {
		t.Fatalf("FailNonTerminal() = %d", failed)
	}
	stored, err := store.Get(context.Background(), working.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != 2 || stored.Task.Status.State != a2a.TaskStateFailed {
		t.Fatalf("重启 Task = version %d, state %s", stored.Version, stored.Task.Status.State)
	}
	statusJSON, err := json.Marshal(stored.Task.Status.Message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusJSON), string(a2aext.ErrorWorkerRestarted)) {
		t.Fatalf("重启状态缺少错误码: %s", statusJSON)
	}
	storedCompleted, err := store.Get(context.Background(), completed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedCompleted.Version != 1 || storedCompleted.Task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("终态 Task 被修改: %+v", storedCompleted)
	}
	updatedBinding, err := store.GetBinding(context.Background(), binding.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedBinding.State != runtimeStateFailed || updatedBinding.LastSequence != 3 {
		t.Fatalf("重启 binding = %+v", updatedBinding)
	}
	events, err := store.ListEvents(context.Background(), binding.ExecutionID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Event.Type != a2aext.EventExecutionDiagnostic || events[1].Payload["errorCode"] != string(a2aext.ErrorWorkerRestarted) ||
		events[2].Event.Type != a2aext.EventExecutionTerminal || events[2].Payload["status"] != string(a2aext.TerminalFailed) {
		t.Fatalf("重启事件 = %+v", events)
	}
	for _, event := range events[1:] {
		restartID, parseErr := uuid.Parse(event.Event.ID)
		if parseErr != nil || restartID.Version() != 7 {
			t.Fatalf("重启 event ID 不是 UUIDv7: %q, %v", event.Event.ID, parseErr)
		}
	}
	statusEvents, err := executionEventsFromMessage(stored.Task.Status.Message)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Task.Status.Message == nil || stored.Task.Status.Message.TaskID != stored.Task.ID || stored.Task.Status.Message.ContextID != stored.Task.ContextID ||
		len(statusEvents) != 1 || statusEvents[0].Event.Type != a2aext.EventExecutionTerminal {
		t.Fatalf("重启终态消息未与 Task 身份及 terminal event 配对: %+v, events=%+v", stored.Task.Status.Message, statusEvents)
	}
	artifactRoles := map[a2aext.ArtifactRole]bool{}
	for _, artifact := range stored.Task.Artifacts {
		metadata, _, ok, decodeErr := decodeArtifactMetadata(artifact)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if ok {
			artifactRoles[metadata.Role] = true
		}
	}
	if !artifactRoles[a2aext.ArtifactDiagnostic] || !artifactRoles[a2aext.ArtifactManifest] {
		t.Fatalf("重启 Task 缺少 diagnostic/manifest Artifact: %+v", stored.Task.Artifacts)
	}
	if failed, err := store.FailNonTerminal(context.Background()); err != nil || failed != 0 {
		t.Fatalf("重复 FailNonTerminal() = %d, %v", failed, err)
	}
}

func TestFailNonTerminalReconstructsMissingBindingFromCommand(t *testing.T) {
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	store := newTestStoreAt(t, now)
	request := testExecutionRequest("command-restart-missing-binding", "restart-secret")
	request.Scope.ExecutionID = "execution-restart-missing-binding"
	request.Scope.LocalTaskID = "local-restart-missing-binding"
	task := &a2a.Task{
		ID: "task-restart-missing-binding", ContextID: "context-restart-missing-binding",
		Status: a2a.TaskStatus{State: a2a.TaskStateSubmitted},
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("执行"), a2a.NewDataPart(request)),
		},
	}
	if _, created, err := store.ClaimCommand(context.Background(), request, string(task.ID), task.ContextID); err != nil || !created {
		t.Fatalf("ClaimCommand() = created %v, err %v", created, err)
	}
	if _, err := store.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	failed, err := store.FailNonTerminal(context.Background())
	if err != nil || failed != 1 {
		t.Fatalf("FailNonTerminal() = %d, %v", failed, err)
	}
	binding, err := store.GetBinding(context.Background(), request.Scope.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.TaskID != string(task.ID) || binding.ContextID != task.ContextID || binding.State != runtimeStateFailed || binding.LastSequence != 2 {
		t.Fatalf("重建 binding = %+v", binding)
	}
	stored, err := store.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	statusEvents, err := executionEventsFromMessage(stored.Task.Status.Message)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Task.Status.State != a2a.TaskStateFailed || len(statusEvents) != 1 || statusEvents[0].Event.Type != a2aext.EventExecutionTerminal ||
		statusEvents[0].Payload["errorCode"] != string(a2aext.ErrorWorkerRestarted) {
		t.Fatalf("重建后的重启终态不完整: task=%+v, events=%+v", stored.Task, statusEvents)
	}
	var raw []byte
	if err := store.db.QueryRow(`SELECT task_json FROM a2a_tasks WHERE owner = ? AND task_id = ?`, "test-owner", task.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "restart-secret") {
		t.Fatalf("重启恢复泄漏敏感值: %s", raw)
	}
}

// TestRecoverStartupRollsBackInterruptedContinueBinding 验证 CONTINUE 首个 Task 落盘前崩溃时恢复上一终态绑定。
func TestRecoverStartupRollsBackInterruptedContinueBinding(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "a2a.db")
	open := func() *Store {
		store, err := Open(context.Background(), Config{
			Path: path,
			Authenticator: func(context.Context) (string, error) {
				return "continue-recovery-owner", nil
			},
			Now: func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("Open() 失败: %v", err)
		}
		return store
	}

	store := open()
	previousTask := &a2a.Task{
		ID: "task-continue-previous", ContextID: "context-continue-recovery",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
	previousRequest := testExecutionRequest("command-continue-previous", "previous-secret")
	previousRequest.Scope.ExecutionID = "execution-continue-recovery"
	previousRequest.Scope.LocalTaskID = "local-continue-recovery"
	previousBinding := RuntimeBinding{
		ExecutionID: previousRequest.Scope.ExecutionID, LocalTaskID: previousRequest.Scope.LocalTaskID,
		WorkerID: "worker", TaskID: string(previousTask.ID), ContextID: previousTask.ContextID,
		Attempt: 1, Turn: 1, AgentType: a2aext.AgentCodex, AgentSessionID: "session-continue-recovery",
		WorktreePath: "/tmp/worktree-continue-recovery", State: "COMPLETED", LastSequence: 7,
	}
	if err := store.SaveBinding(context.Background(), previousBinding); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.ClaimCommand(context.Background(), previousRequest, string(previousTask.ID), previousTask.ContextID); err != nil || !created {
		t.Fatalf("创建上一轮 command = created %v, err %v", created, err)
	}
	if _, err := store.Create(context.Background(), previousTask); err != nil {
		t.Fatal(err)
	}

	continueRequest := testExecutionRequest("command-continue-interrupted", "continue-secret")
	continueRequest.Command.Operation = a2aext.OperationContinue
	continueRequest.Scope = previousRequest.Scope
	continueRequest.Scope.Turn = 2
	continueRequest.Worktree.Mode = a2aext.WorktreeResume
	continueRequest.Resume = &a2aext.Resume{
		AgentSessionID: previousBinding.AgentSessionID,
		WorktreePath:   previousBinding.WorktreePath,
	}
	missingTaskID := "task-continue-not-created"
	if _, created, err := store.ClaimCommand(context.Background(), continueRequest, missingTaskID, previousTask.ContextID); err != nil || !created {
		t.Fatalf("创建中断 CONTINUE command = created %v, err %v", created, err)
	}
	interrupted := previousBinding
	interrupted.TaskID = missingTaskID
	interrupted.Turn = continueRequest.Scope.Turn
	interrupted.State = "SUBMITTED"
	if err := store.SaveBinding(context.Background(), interrupted); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = open()
	t.Cleanup(func() { _ = store.Close() })
	stats, err := store.RecoverStartup(context.Background())
	if err != nil {
		t.Fatalf("RecoverStartup() 失败: %v", err)
	}
	if stats.Commands != 1 || stats.Bindings != 0 {
		t.Fatalf("RecoverStartup() = %+v", stats)
	}
	recovered, err := store.GetBinding(context.Background(), previousBinding.ExecutionID)
	if err != nil {
		t.Fatalf("读取回滚 binding: %v", err)
	}
	if recovered.TaskID != string(previousTask.ID) || recovered.ContextID != previousTask.ContextID || recovered.Turn != 1 ||
		recovered.State != "COMPLETED" || recovered.LastSequence != previousBinding.LastSequence ||
		recovered.AgentSessionID != previousBinding.AgentSessionID || recovered.WorktreePath != previousBinding.WorktreePath {
		t.Fatalf("回滚 binding = %+v", recovered)
	}
	if _, err := store.LookupCommand(context.Background(), continueRequest.Command.ID); err == nil {
		t.Fatal("中断 CONTINUE command 未清理")
	}
}

// TestCompactBeforeDeletesOnlyExpiredEventsFromTerminalTurns 验证跨 turn 压缩不会保留旧终态或误删活跃事件。
func TestCompactBeforeDeletesOnlyExpiredEventsFromTerminalTurns(t *testing.T) {
	now := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)
	cutoff := now.Add(-90 * 24 * time.Hour)
	store := newTestStoreAt(t, now)
	contextID := "context-cross-turn-retention"
	executionID := "execution"

	terminalTask := &a2a.Task{
		ID: "task-terminal-turn-one", ContextID: contextID,
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Artifacts: []*a2a.Artifact{testArtifact(t, "terminal-turn-one", a2aext.ArtifactManifest, 1, old, false)},
	}
	if _, err := store.Create(context.Background(), terminalTask); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE a2a_tasks SET updated_at = ? WHERE owner = ? AND task_id = ?`, old.UnixNano(), "test-owner", terminalTask.ID); err != nil {
		t.Fatal(err)
	}
	binding := RuntimeBinding{
		ExecutionID: executionID, LocalTaskID: "local-cross-turn-retention", WorkerID: "worker",
		TaskID: string(terminalTask.ID), ContextID: contextID, Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, State: "COMPLETED",
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	terminalEvent := testStoreEvent(a2aext.EventLogChunk, 1, old)
	terminalEvent.Scope.LocalTaskID = binding.LocalTaskID
	terminalEvent.Scope.ExecutionID = executionID
	if _, err := store.AppendEvent(context.Background(), terminalEvent); err != nil {
		t.Fatal(err)
	}

	activeTask := &a2a.Task{
		ID: "task-active-turn-two", ContextID: contextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired},
	}
	if _, err := store.Create(context.Background(), activeTask); err != nil {
		t.Fatal(err)
	}
	binding.TaskID = string(activeTask.ID)
	binding.Turn = 2
	binding.State = "INPUT_REQUIRED"
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	activeEvent := testStoreEvent(a2aext.EventLogChunk, 2, old.Add(time.Minute))
	activeEvent.Scope.LocalTaskID = binding.LocalTaskID
	activeEvent.Scope.ExecutionID = executionID
	activeEvent.Scope.Turn = 2
	if _, err := store.AppendEvent(context.Background(), activeEvent); err != nil {
		t.Fatal(err)
	}

	stats, err := store.CompactBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CompactBefore() 失败: %v", err)
	}
	if stats.Tasks != 1 || stats.Events != 1 {
		t.Fatalf("CompactBefore() = %+v", stats)
	}
	events, err := store.ListEvents(context.Background(), executionID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event.ID != activeEvent.Event.ID || events[0].Scope.Turn != 2 {
		t.Fatalf("跨 turn 压缩事件 = %+v", events)
	}
}

func TestCompactBeforeKeepsRecoverySummaryAndBinding(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)
	cutoff := now.Add(-90 * 24 * time.Hour)
	store := newTestStoreAt(t, now)
	task := &a2a.Task{
		ID: "task-compact", ContextID: "context-compact",
		Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted},
		History:  []*a2a.Message{a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("完整历史"))},
		Metadata: map[string]any{"transient": "detail"},
		Artifacts: []*a2a.Artifact{
			testArtifact(t, "manifest-old", a2aext.ArtifactManifest, 1, old, false),
			testArtifact(t, "manifest-latest", a2aext.ArtifactManifest, 2, old, false),
			testArtifact(t, "result", a2aext.ArtifactResult, 3, old, true),
			testArtifact(t, "diagnostic", a2aext.ArtifactDiagnostic, 4, old, false),
			testArtifact(t, "log", a2aext.ArtifactLog, 5, old, false),
			testArtifact(t, "output", a2aext.ArtifactOutput, 6, old, false),
		},
	}
	if _, err := store.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE a2a_tasks SET updated_at = ? WHERE owner = ? AND task_id = ?`, old.UnixNano(), "test-owner", task.ID); err != nil {
		t.Fatal(err)
	}
	binding := RuntimeBinding{
		ExecutionID: "execution", LocalTaskID: "local-task", WorkerID: "worker",
		TaskID: string(task.ID), ContextID: task.ContextID, Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, AgentSessionID: "session", WorktreePath: "/tmp/worktree", State: "COMPLETED",
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	command := testExecutionRequest("command-compact", "old-secret")
	if _, created, err := store.ClaimCommand(context.Background(), command, string(task.ID), task.ContextID); err != nil || !created {
		t.Fatalf("创建待压缩 command = created %v, err %v", created, err)
	}
	if _, err := store.db.Exec(`UPDATE a2a_command_inbox SET created_at = ? WHERE owner = ? AND command_id = ?`, old.UnixNano(), "test-owner", command.Command.ID); err != nil {
		t.Fatal(err)
	}
	for sequence, eventType := range []a2aext.EventType{
		a2aext.EventLogChunk,
		a2aext.EventResultUpdated,
		a2aext.EventExecutionDiagnostic,
	} {
		if _, err := store.AppendEvent(context.Background(), testStoreEvent(eventType, int64(sequence+1), old.Add(time.Duration(sequence)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	terminal := testStoreEvent(a2aext.EventExecutionTerminal, 4, now.Add(-time.Hour))
	if _, err := store.AppendEvent(context.Background(), terminal); err != nil {
		t.Fatal(err)
	}
	active := &a2a.Task{
		ID: "task-active-old", ContextID: "context-active-old",
		Status:  a2a.TaskStatus{State: a2a.TaskStateWorking},
		History: []*a2a.Message{a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("仍在运行"))},
	}
	if _, err := store.Create(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE a2a_tasks SET updated_at = ? WHERE owner = ? AND task_id = ?`, old.UnixNano(), "test-owner", active.ID); err != nil {
		t.Fatal(err)
	}
	activeBinding := RuntimeBinding{
		ExecutionID: "execution-active", LocalTaskID: "local-active", WorkerID: "worker",
		TaskID: string(active.ID), ContextID: active.ContextID, Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, State: "WORKING",
	}
	if err := store.SaveBinding(context.Background(), activeBinding); err != nil {
		t.Fatal(err)
	}
	activeCommand := testExecutionRequest("command-active", "active-secret")
	activeCommand.Scope.ExecutionID = activeBinding.ExecutionID
	if _, created, err := store.ClaimCommand(context.Background(), activeCommand, string(active.ID), active.ContextID); err != nil || !created {
		t.Fatalf("创建活跃 command = created %v, err %v", created, err)
	}
	if _, err := store.db.Exec(`UPDATE a2a_command_inbox SET created_at = ? WHERE owner = ? AND command_id = ?`, old.UnixNano(), "test-owner", activeCommand.Command.ID); err != nil {
		t.Fatal(err)
	}
	activeEvent := testStoreEvent(a2aext.EventLogChunk, 1, old)
	activeEvent.Scope.LocalTaskID = activeBinding.LocalTaskID
	activeEvent.Scope.ExecutionID = activeBinding.ExecutionID
	if _, err := store.AppendEvent(context.Background(), activeEvent); err != nil {
		t.Fatal(err)
	}

	stats, err := store.CompactBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CompactBefore() 失败: %v", err)
	}
	if stats.Tasks != 1 || stats.Events != 3 || stats.Commands != 1 {
		t.Fatalf("CompactBefore() = %+v", stats)
	}
	stored, err := store.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != 2 || len(stored.Task.History) != 0 || stored.Task.Metadata != nil || stored.Task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("压缩 Task = %+v", stored)
	}
	if len(stored.Task.Artifacts) != 3 {
		t.Fatalf("压缩 Artifact 数量 = %d", len(stored.Task.Artifacts))
	}
	wantNames := map[string]bool{"manifest-latest": true, "result": true, "diagnostic": true}
	for _, artifact := range stored.Task.Artifacts {
		if !wantNames[artifact.Name] {
			t.Fatalf("保留了意外 Artifact: %s", artifact.Name)
		}
		metadata, _, ok, err := decodeArtifactMetadata(artifact)
		if err != nil || !ok || !metadata.Compacted {
			t.Fatalf("Artifact 压缩标记错误: %+v, ok %v, err %v", metadata, ok, err)
		}
	}
	events, err := store.ListEvents(context.Background(), binding.ExecutionID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event.Type != a2aext.EventExecutionTerminal {
		t.Fatalf("新终态事件应保留: %+v", events)
	}
	activeEvents, err := store.ListEvents(context.Background(), activeBinding.ExecutionID, 0, 10)
	if err != nil || len(activeEvents) != 1 || activeEvents[0].Event.Type != a2aext.EventLogChunk {
		t.Fatalf("非终态 execution 的过期事件被清理: %+v, %v", activeEvents, err)
	}
	activeStored, err := store.Get(context.Background(), active.ID)
	if err != nil || activeStored.Version != 1 || activeStored.Task.Status.State != a2a.TaskStateWorking || len(activeStored.Task.History) != 1 {
		t.Fatalf("活跃旧 Task 被压缩: %+v, %v", activeStored, err)
	}
	persistedBinding, err := store.GetBinding(context.Background(), binding.ExecutionID)
	if err != nil || persistedBinding.AgentSessionID != "session" || persistedBinding.WorktreePath != "/tmp/worktree" || persistedBinding.LastSequence != 4 {
		t.Fatalf("压缩后 binding = %+v, err %v", persistedBinding, err)
	}
	commandRecord, err := store.LookupCommand(context.Background(), command.Command.ID)
	if err != nil || commandRecord.Request != nil || commandRecord.PayloadHash == "" || commandRecord.TaskID != string(task.ID) {
		t.Fatalf("压缩后 command 幂等摘要 = %+v, err %v", commandRecord, err)
	}
	var requestBytes int
	if err := store.db.QueryRow(`SELECT length(request_json) FROM a2a_command_inbox WHERE owner = ? AND command_id = ?`, "test-owner", command.Command.ID).Scan(&requestBytes); err != nil || requestBytes != 0 {
		t.Fatalf("压缩后 request_json 长度 = %d, err %v", requestBytes, err)
	}
	activeRecord, err := store.LookupCommand(context.Background(), activeCommand.Command.ID)
	if err != nil || activeRecord.Request == nil {
		t.Fatalf("非终态 command 请求被缩减: %+v, err %v", activeRecord, err)
	}
	if stats, err := store.CompactBefore(context.Background(), cutoff); err != nil || stats != (CompactionStats{}) {
		t.Fatalf("重复 CompactBefore() = %+v, %v", stats, err)
	}
}

func newTestStoreAt(t *testing.T, now time.Time) *Store {
	t.Helper()
	store, err := Open(context.Background(), Config{
		Path: filepath.Join(t.TempDir(), "a2a.db"),
		Authenticator: func(context.Context) (string, error) {
			return "test-owner", nil
		},
		Now: func() time.Time { return now },
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

func testStoreEvent(eventType a2aext.EventType, sequence int64, occurredAt time.Time) *a2aext.ExecutionEvent {
	payload := map[string]any{}
	switch eventType {
	case a2aext.EventLogChunk:
		payload = map[string]any{"stream": string(a2aext.LogStdout), "content": "log"}
	case a2aext.EventResultUpdated:
		payload = map[string]any{"result": "result"}
	case a2aext.EventExecutionDiagnostic:
		payload = map[string]any{"message": "diagnostic"}
	case a2aext.EventExecutionTerminal:
		payload = map[string]any{"status": string(a2aext.TerminalCompleted)}
	}
	return &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event: a2aext.EventHeader{
			ID:       deterministicUUIDv7(sequence),
			Sequence: sequence, Type: eventType, OccurredAt: occurredAt,
		},
		Scope: a2aext.EventScope{
			LocalTaskID: "local-task", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker",
		},
		Payload: payload,
	}
}

func testArtifact(t *testing.T, name string, role a2aext.ArtifactRole, sequence int64, createdAt time.Time, nested bool) *a2a.Artifact {
	t.Helper()
	metadata := a2aext.ArtifactMetadata{
		Kind: a2aext.ArtifactKind, Version: a2aext.Version, Role: role,
		ExecutionID: "execution", Attempt: 1, Turn: 1, EventID: deterministicUUIDv7(sequence),
		Sequence: sequence, CreatedAt: createdAt,
	}
	if role == a2aext.ArtifactLog {
		metadata.Stream = a2aext.LogStdout
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	artifactMetadata := value
	if nested {
		artifactMetadata = map[string]any{a2aext.ExtensionURI: value}
	}
	return &a2a.Artifact{
		ID: a2a.ArtifactID("artifact-" + name), Name: name,
		Metadata: artifactMetadata, Parts: a2a.ContentParts{a2a.NewTextPart(name)},
	}
}

func deterministicUUIDv7(sequence int64) string {
	return fmt.Sprintf("018f0000-0000-7000-8000-%012x", sequence)
}
