package httpapi

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

type terminalPairA2ATransport struct {
	a2aclient.Transport
	events         []a2a.Event
	tasks          []*a2a.Task
	getCalls       int
	subscribeCalls int
}

func (t *terminalPairA2ATransport) GetTask(context.Context, a2aclient.ServiceParams, *a2a.GetTaskRequest) (*a2a.Task, error) {
	if t.getCalls >= len(t.tasks) {
		return nil, a2a.ErrTaskNotFound
	}
	task := t.tasks[t.getCalls]
	t.getCalls++
	return task, nil
}

func (t *terminalPairA2ATransport) SubscribeToTask(context.Context, a2aclient.ServiceParams, *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	t.subscribeCalls++
	return func(yield func(a2a.Event, error) bool) {
		for _, event := range t.events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

// TestSubscribeA2ATaskProjectsTerminalPairAtomically 验证终态 Artifact 和标准状态只产生一次原子投影。
func TestSubscribeA2ATaskProjectsTerminalPairAtomically(t *testing.T) {
	for _, test := range []struct {
		name          string
		standardState a2a.TaskState
		changePayload bool
		wantConflict  bool
	}{
		{name: "matching", standardState: a2a.TaskStateFailed},
		{name: "conflicting state", standardState: a2a.TaskStateCompleted, wantConflict: true},
		{name: "conflicting hash", standardState: a2a.TaskStateFailed, changePayload: true, wantConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			round := domain.TaskA2ARound{
				ID: "round-terminal", TaskID: "task-terminal", ExecutionID: "execution-terminal",
				Attempt: 1, Turn: 1, WorkerID: "worker-terminal", A2ATaskID: "remote-terminal", ContextID: "context-terminal",
			}
			terminal := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, testA2ATime(), map[string]any{
				"status": string(a2aext.TerminalFailed), "errorCode": "AGENT_FAILED", "message": "agent failed",
			})
			statusTerminal := terminal
			if test.changePayload {
				copy := *terminal
				copy.Payload = map[string]any{"status": string(a2aext.TerminalFailed), "errorCode": "AGENT_FAILED", "message": "changed"}
				statusTerminal = &copy
			}
			message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(statusTerminal))
			message.TaskID = a2a.TaskID(round.A2ATaskID)
			message.ContextID = round.ContextID
			message.Extensions = []string{a2aext.ExtensionURI}
			transport := &terminalPairA2ATransport{events: []a2a.Event{
				&a2a.TaskArtifactUpdateEvent{
					TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
					Artifact: validA2ATestArtifact(t, round, terminal, a2aext.ArtifactManifest), LastChunk: true,
				},
				&a2a.TaskStatusUpdateEvent{
					TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
					Status: a2a.TaskStatus{State: test.standardState, Message: message},
				},
			}}
			client := newTerminalPairA2AClient(t, round, transport)

			var updates []app.A2ARemoteUpdate
			err := subscribeA2ATask(t.Context(), client, round, func(_ context.Context, update app.A2ARemoteUpdate) error {
				updates = append(updates, update)
				return nil
			})
			if test.wantConflict {
				if !errors.Is(err, app.ErrA2AProtocolConflict) {
					t.Fatalf("冲突终态错误 = %v，期望协议冲突", err)
				}
				if len(updates) != 0 {
					t.Fatalf("冲突终态已产生 %d 次部分投影: %+v", len(updates), updates)
				}
				return
			}
			if err != nil {
				t.Fatalf("匹配终态返回错误: %v", err)
			}
			if len(updates) != 1 || updates[0].Event == nil || updates[0].Event.Event.ID != terminal.Event.ID || updates[0].Status != domain.TaskA2ARemoteStatusFailed {
				t.Fatalf("匹配终态投影 = %+v，期望一次 Event+Status 组合更新", updates)
			}
		})
	}
}

// TestEmitA2ATaskProjectsTerminalPairAtomically 验证 GetTask 快照先校验终态一致性，再执行任何投影。
func TestEmitA2ATaskProjectsTerminalPairAtomically(t *testing.T) {
	for _, test := range []struct {
		name          string
		standardState a2a.TaskState
		changePayload bool
		wantConflict  bool
	}{
		{name: "matching", standardState: a2a.TaskStateFailed},
		{name: "conflicting state", standardState: a2a.TaskStateCompleted, wantConflict: true},
		{name: "conflicting hash", standardState: a2a.TaskStateFailed, changePayload: true, wantConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			round := domain.TaskA2ARound{
				ID: "round-snapshot-terminal", TaskID: "task-snapshot-terminal", ExecutionID: "execution-snapshot-terminal",
				Attempt: 1, Turn: 1, WorkerID: "worker-snapshot-terminal", A2ATaskID: "remote-snapshot-terminal", ContextID: "context-snapshot-terminal",
			}
			terminal := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, testA2ATime(), map[string]any{
				"status": string(a2aext.TerminalFailed), "errorCode": "AGENT_FAILED", "message": "agent failed",
			})
			artifactTerminal := terminal
			if test.changePayload {
				copy := *terminal
				copy.Payload = map[string]any{"status": string(a2aext.TerminalFailed), "errorCode": "AGENT_FAILED", "message": "changed"}
				artifactTerminal = &copy
			}
			message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(terminal))
			message.TaskID = a2a.TaskID(round.A2ATaskID)
			message.ContextID = round.ContextID
			message.Extensions = []string{a2aext.ExtensionURI}
			task := &a2a.Task{
				ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
				Status:    a2a.TaskStatus{State: test.standardState, Message: message},
				History:   []*a2a.Message{message},
				Artifacts: []*a2a.Artifact{validA2ATestArtifact(t, round, artifactTerminal, a2aext.ArtifactManifest)},
			}

			var updates []app.A2ARemoteUpdate
			isTerminal, err := emitA2ATask(t.Context(), round, task, func(_ context.Context, update app.A2ARemoteUpdate) error {
				updates = append(updates, update)
				return nil
			})
			if test.wantConflict {
				if !errors.Is(err, app.ErrA2AProtocolConflict) {
					t.Fatalf("冲突终态错误 = %v，期望协议冲突", err)
				}
				if isTerminal || len(updates) != 0 {
					t.Fatalf("冲突终态产生部分投影: terminal=%v updates=%+v", isTerminal, updates)
				}
				return
			}
			if err != nil || !isTerminal {
				t.Fatalf("匹配终态结果 = %v, %v", isTerminal, err)
			}
			if len(updates) != 1 || updates[0].Event == nil || updates[0].Event.Event.ID != terminal.Event.ID || updates[0].Status != domain.TaskA2ARemoteStatusFailed {
				t.Fatalf("匹配终态投影 = %+v，期望一次 Event+Status 组合更新", updates)
			}
		})
	}
}

// TestTerminalStatusWithoutExtensionRemainsCompatible 验证绑定前的标准 REJECTED 不依赖 execution Artifact。
func TestTerminalStatusWithoutExtensionRemainsCompatible(t *testing.T) {
	round := domain.TaskA2ARound{
		ID: "round-rejected", TaskID: "task-rejected", ExecutionID: "execution-rejected",
		Attempt: 1, Turn: 1, WorkerID: "worker-rejected", A2ATaskID: "remote-rejected", ContextID: "context-rejected",
	}
	message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("request rejected"))
	message.TaskID = a2a.TaskID(round.A2ATaskID)
	message.ContextID = round.ContextID
	status := &a2a.TaskStatusUpdateEvent{
		TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateRejected, Message: message},
	}
	transport := &terminalPairA2ATransport{events: []a2a.Event{status}}
	client := newTerminalPairA2AClient(t, round, transport)

	var streamUpdates []app.A2ARemoteUpdate
	if err := subscribeA2ATask(t.Context(), client, round, func(_ context.Context, update app.A2ARemoteUpdate) error {
		streamUpdates = append(streamUpdates, update)
		return nil
	}); err != nil {
		t.Fatalf("纯标准 REJECTED 流返回错误: %v", err)
	}
	if len(streamUpdates) != 1 || streamUpdates[0].Event != nil || streamUpdates[0].Status != domain.TaskA2ARemoteStatusRejected {
		t.Fatalf("纯标准 REJECTED 流投影 = %+v", streamUpdates)
	}

	var snapshotUpdates []app.A2ARemoteUpdate
	terminal, err := emitA2ATask(t.Context(), round, &a2a.Task{
		ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateRejected, Message: message},
	}, func(_ context.Context, update app.A2ARemoteUpdate) error {
		snapshotUpdates = append(snapshotUpdates, update)
		return nil
	})
	if err != nil || !terminal {
		t.Fatalf("纯标准 REJECTED 快照结果 = %v, %v", terminal, err)
	}
	if len(snapshotUpdates) != 1 || snapshotUpdates[0].Event != nil || snapshotUpdates[0].Status != domain.TaskA2ARemoteStatusRejected {
		t.Fatalf("纯标准 REJECTED 快照投影 = %+v", snapshotUpdates)
	}
}

// TestTerminalStatusWithoutExtensionConflicts 验证绑定后或非 REJECTED 终态缺少 execution 终态时拒绝投影。
func TestTerminalStatusWithoutExtensionConflicts(t *testing.T) {
	tests := []struct {
		name          string
		standardState a2a.TaskState
		lastSequence  int64
	}{
		{name: "completed", standardState: a2a.TaskStateCompleted},
		{name: "failed", standardState: a2a.TaskStateFailed},
		{name: "canceled", standardState: a2a.TaskStateCanceled},
		{name: "rejected after runtime binding", standardState: a2a.TaskStateRejected, lastSequence: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			round := domain.TaskA2ARound{
				ID: "round-status-only", TaskID: "task-status-only", ExecutionID: "execution-status-only",
				Attempt: 1, Turn: 1, WorkerID: "worker-status-only", A2ATaskID: "remote-status-only",
				ContextID: "context-status-only", LastSequence: test.lastSequence,
			}
			message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("terminal status"))
			message.TaskID = a2a.TaskID(round.A2ATaskID)
			message.ContextID = round.ContextID
			status := &a2a.TaskStatusUpdateEvent{
				TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
				Status: a2a.TaskStatus{State: test.standardState, Message: message},
			}
			transport := &terminalPairA2ATransport{events: []a2a.Event{status}}
			client := newTerminalPairA2AClient(t, round, transport)

			streamCalls := 0
			err := subscribeA2ATask(t.Context(), client, round, func(context.Context, app.A2ARemoteUpdate) error {
				streamCalls++
				return nil
			})
			if !errors.Is(err, app.ErrA2AProtocolConflict) || streamCalls != 0 {
				t.Fatalf("纯标准终态流错误 = %v，handler 调用 = %d", err, streamCalls)
			}

			snapshotCalls := 0
			terminal, err := emitA2ATask(t.Context(), round, &a2a.Task{
				ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
				Status: a2a.TaskStatus{State: test.standardState, Message: message},
			}, func(context.Context, app.A2ARemoteUpdate) error {
				snapshotCalls++
				return nil
			})
			if !errors.Is(err, app.ErrA2AProtocolConflict) || terminal || snapshotCalls != 0 {
				t.Fatalf("纯标准终态快照结果 = %v, %v，handler 调用 = %d", terminal, err, snapshotCalls)
			}
		})
	}
}

// TestTerminalSnapshotWithoutArtifactConflicts 验证状态 Message 的终态扩展不能替代必需 Artifact。
func TestTerminalSnapshotWithoutArtifactConflicts(t *testing.T) {
	round := domain.TaskA2ARound{
		ID: "round-missing-terminal-artifact", TaskID: "task-missing-terminal-artifact", ExecutionID: "execution-missing-terminal-artifact",
		Attempt: 1, Turn: 1, WorkerID: "worker-missing-terminal-artifact", A2ATaskID: "remote-missing-terminal-artifact", ContextID: "context-missing-terminal-artifact",
	}
	terminalEvent := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, testA2ATime(), map[string]any{
		"status": string(a2aext.TerminalCompleted), "result": "done",
	})
	message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(terminalEvent))
	message.TaskID = a2a.TaskID(round.A2ATaskID)
	message.ContextID = round.ContextID
	message.Extensions = []string{a2aext.ExtensionURI}

	calls := 0
	isTerminal, err := emitA2ATask(t.Context(), round, &a2a.Task{
		ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: message},
	}, func(context.Context, app.A2ARemoteUpdate) error {
		calls++
		return nil
	})
	if !errors.Is(err, app.ErrA2AProtocolConflict) || isTerminal || calls != 0 {
		t.Fatalf("缺少终态 Artifact 的快照结果 = %v, %v，handler 调用 = %d", isTerminal, err, calls)
	}
}

// TestReconcileA2ATaskRecoversIncompleteTerminalPairFromSnapshot 验证断流终态只由完整 GetTask 快照落库。
func TestReconcileA2ATaskRecoversIncompleteTerminalPairFromSnapshot(t *testing.T) {
	round := domain.TaskA2ARound{
		ID: "round-recover-terminal", TaskID: "task-recover-terminal", ExecutionID: "execution-recover-terminal",
		Attempt: 1, Turn: 1, WorkerID: "worker-recover-terminal", A2ATaskID: "remote-recover-terminal", ContextID: "context-recover-terminal",
	}
	terminal := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, testA2ATime(), map[string]any{
		"status": string(a2aext.TerminalCompleted), "result": "done",
	})
	message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(terminal))
	message.TaskID = a2a.TaskID(round.A2ATaskID)
	message.ContextID = round.ContextID
	message.Extensions = []string{a2aext.ExtensionURI}
	artifact := validA2ATestArtifact(t, round, terminal, a2aext.ArtifactManifest)
	transport := &terminalPairA2ATransport{
		events: []a2a.Event{&a2a.TaskArtifactUpdateEvent{
			TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID, Artifact: artifact, LastChunk: true,
		}},
		tasks: []*a2a.Task{
			{ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking}},
			{
				ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
				Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: message}, Artifacts: []*a2a.Artifact{artifact},
			},
		},
	}
	client := newTerminalPairA2AClient(t, round, transport)
	var updates []app.A2ARemoteUpdate
	if err := reconcileA2ATask(t.Context(), client, round, func(_ context.Context, update app.A2ARemoteUpdate) error {
		updates = append(updates, update)
		return nil
	}); err != nil {
		t.Fatalf("断流终态恢复返回错误: %v", err)
	}
	if transport.getCalls != 2 || transport.subscribeCalls != 1 {
		t.Fatalf("GetTask 调用 %d 次，Subscribe 调用 %d 次", transport.getCalls, transport.subscribeCalls)
	}
	if len(updates) != 2 || updates[0].Status != domain.TaskA2ARemoteStatusWorking || updates[1].Event == nil ||
		updates[1].Event.Event.ID != terminal.Event.ID || updates[1].Status != domain.TaskA2ARemoteStatusCompleted {
		t.Fatalf("断流终态恢复投影 = %+v", updates)
	}
}

func newTerminalPairA2AClient(t *testing.T, round domain.TaskA2ARound, transport a2aclient.Transport) *a2aclient.Client {
	t.Helper()
	card := validA2ATestCard(workerA2ATarget{
		worker: &domain.Worker{ID: round.WorkerID}, host: "127.0.0.1", port: 39125,
		endpointPath: "/a2a", cardPath: "/.well-known/agent-card.json",
	})
	client, err := a2aclient.NewFromCard(t.Context(), card,
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithTransport(a2a.TransportProtocolJSONRPC, a2aclient.TransportFactoryFn(
			func(context.Context, *a2a.AgentCard, *a2a.AgentInterface) (a2aclient.Transport, error) {
				return transport, nil
			},
		)),
	)
	if err != nil {
		t.Fatalf("创建终态测试 A2A client: %v", err)
	}
	return client
}

func testA2ATime() time.Time {
	return time.Date(2026, 8, 10, 17, 0, 0, 0, time.UTC)
}
