package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestA2ATransportIdentityHashAndRoleNegativeBranches(t *testing.T) {
	round := domain.TaskA2ARound{
		TaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker",
		A2ATaskID: "remote-task", ContextID: "remote-context",
	}
	for name, identity := range map[string][2]string{
		"empty":           {"", ""},
		"task changed":    {"other", round.ContextID},
		"context changed": {round.A2ATaskID, "other"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateA2ARemoteIdentity(round, identity[0], identity[1]); !errors.Is(err, app.ErrA2AProtocolConflict) {
				t.Fatalf("身份错误=%v", err)
			}
		})
	}
	if err := validateA2ARemoteIdentity(round, round.A2ATaskID, round.ContextID); err != nil {
		t.Fatal(err)
	}
	if _, err := hashA2ATransportEvent(nil); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("nil event hash 错误=%v", err)
	}

	now := time.Date(2026, 8, 14, 21, 0, 0, 0, time.UTC)
	for terminal, want := range map[a2aext.TerminalStatus]domain.TaskA2ARemoteStatus{
		a2aext.TerminalCompleted: domain.TaskA2ARemoteStatusCompleted,
		a2aext.TerminalFailed:    domain.TaskA2ARemoteStatusFailed,
		a2aext.TerminalRejected:  domain.TaskA2ARemoteStatusRejected,
		a2aext.TerminalCanceled:  domain.TaskA2ARemoteStatusCanceled,
	} {
		event := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, now, map[string]any{"status": string(terminal)})
		if got := mapA2ATerminalEventStatus(event); got != want {
			t.Fatalf("terminal %s 映射=%s", terminal, got)
		}
	}
	if mapA2ATerminalEventStatus(nil) != domain.TaskA2ARemoteStatusUnspecified ||
		mapA2ATerminalEventStatus(validA2ATestEvent(t, round, 1, a2aext.EventExecutionAccepted, now, map[string]any{})) != domain.TaskA2ARemoteStatusUnspecified {
		t.Fatal("非终态事件应映射为 UNSPECIFIED")
	}
	unknown := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, now, map[string]any{"status": string(a2aext.TerminalCompleted)})
	unknown.Payload["status"] = "UNKNOWN"
	if mapA2ATerminalEventStatus(unknown) != domain.TaskA2ARemoteStatusUnspecified {
		t.Fatal("未知终态应映射为 UNSPECIFIED")
	}

	roleCases := []struct {
		role      a2aext.ArtifactRole
		eventType a2aext.EventType
	}{
		{a2aext.ArtifactManifest, a2aext.EventAgentSessionUpdated},
		{a2aext.ArtifactLog, a2aext.EventLogChunk},
		{a2aext.ArtifactConversation, a2aext.EventConversationMessage},
		{a2aext.ArtifactInteraction, a2aext.EventInteractionResolved},
		{a2aext.ArtifactResult, a2aext.EventResultUpdated},
		{a2aext.ArtifactDiagnostic, a2aext.EventExecutionDiagnostic},
	}
	for _, test := range roleCases {
		if !a2aArtifactRoleMatchesEvent(test.role, test.eventType) {
			t.Fatalf("角色 %s 不接受事件 %s", test.role, test.eventType)
		}
	}
	if a2aArtifactRoleMatchesEvent("unknown", a2aext.EventLogChunk) ||
		a2aArtifactRoleMatchesEvent(a2aext.ArtifactResult, a2aext.EventLogChunk) {
		t.Fatal("错误 Artifact 角色被接受")
	}
	if events, err := executionEventsFromParts(a2a.ContentParts{nil, a2a.NewTextPart("ordinary")}); err != nil || len(events) != 0 {
		t.Fatalf("普通 parts=%+v, %v", events, err)
	}
}

func TestA2AStreamProjectorRejectsMalformedTerminalPairs(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	round := domain.TaskA2ARound{
		TaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker",
		A2ATaskID: "remote-task", ContextID: "remote-context",
	}
	okHandler := func(context.Context, app.A2ARemoteUpdate) error { return nil }
	projector := &a2aStreamProjector{round: round, handler: okHandler}
	if _, err := projector.emitTerminalStatus(ctx, nil); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("nil terminal status 错误=%v", err)
	}
	working := &a2a.TaskStatusUpdateEvent{TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}
	if _, err := projector.emitTerminalStatus(ctx, working); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("非终态 status 错误=%v", err)
	}
	failed := &a2a.TaskStatusUpdateEvent{TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}
	if _, err := projector.emitTerminalStatus(ctx, failed); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("缺失 terminal event 错误=%v", err)
	}

	rejected := &a2a.TaskStatusUpdateEvent{TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateRejected}}
	terminal, err := projector.emitTerminalStatus(ctx, rejected)
	if err != nil || !terminal {
		t.Fatalf("早期 rejected=%v, %v", terminal, err)
	}
	wantErr := errors.New("projection failed")
	projector.handler = func(context.Context, app.A2ARemoteUpdate) error { return wantErr }
	if _, err := projector.emitTerminalStatus(ctx, rejected); !errors.Is(err, wantErr) {
		t.Fatalf("rejected 投影错误=%v", err)
	}
	projector.handler = okHandler

	terminalEvent := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, now, map[string]any{"status": string(a2aext.TerminalCompleted)})
	terminalMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(terminalEvent))
	terminalMessage.TaskID = a2a.TaskID(round.A2ATaskID)
	terminalMessage.ContextID = round.ContextID
	terminalMessage.Extensions = []string{a2aext.ExtensionURI}
	completed := &a2a.TaskStatusUpdateEvent{
		TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: terminalMessage},
	}
	if _, err := projector.emitTerminalStatus(ctx, completed); !errors.Is(err, errA2ATerminalPairIncomplete) {
		t.Fatalf("无 Artifact 的终态配对错误=%v", err)
	}

	hash, err := hashA2ATransportEvent(terminalEvent)
	if err != nil {
		t.Fatal(err)
	}
	projector.terminal = &a2aPendingTerminal{update: app.A2ARemoteUpdate{
		TaskID: round.A2ATaskID, ContextID: round.ContextID, Sequence: terminalEvent.Event.Sequence, Event: terminalEvent,
	}, hash: hash}
	if _, err := projector.emit(ctx, a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("unexpected"))); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("终态 Artifact 后非 status 错误=%v", err)
	}
	if _, err := projector.emitTerminalStatus(ctx, &a2a.TaskStatusUpdateEvent{
		TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("配对 status 缺 event 错误=%v", err)
	}

	badIdentityMessage := *terminalMessage
	badIdentityMessage.ContextID = "other"
	if _, err := (&a2aStreamProjector{round: round, handler: okHandler}).emitTerminalStatus(ctx, &a2a.TaskStatusUpdateEvent{
		TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &badIdentityMessage},
	}); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("status message 身份错误=%v", err)
	}

	failedEvent := validA2ATestEvent(t, round, 1, a2aext.EventExecutionTerminal, now, map[string]any{"status": string(a2aext.TerminalFailed)})
	failedMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(failedEvent))
	failedMessage.TaskID = a2a.TaskID(round.A2ATaskID)
	failedMessage.ContextID = round.ContextID
	failedMessage.Extensions = []string{a2aext.ExtensionURI}
	if _, err := (&a2aStreamProjector{round: round, handler: okHandler, terminal: projector.terminal}).emitTerminalStatus(ctx, &a2a.TaskStatusUpdateEvent{
		TaskID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateFailed, Message: failedMessage},
	}); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("Artifact/status 状态不一致错误=%v", err)
	}
}

func TestEmitA2ATaskNegativeProjectionBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 23, 0, 0, 0, time.UTC)
	round := domain.TaskA2ARound{
		TaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker",
		A2ATaskID: "remote-task", ContextID: "remote-context",
	}
	okHandler := func(context.Context, app.A2ARemoteUpdate) error { return nil }
	if _, err := emitA2ATask(ctx, round, &a2a.Task{ID: "", ContextID: ""}, okHandler); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("空远端身份错误=%v", err)
	}

	badStatusMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("status"))
	badStatusMessage.TaskID = "other"
	badStatusMessage.ContextID = round.ContextID
	if _, err := emitA2ATask(ctx, round, &a2a.Task{
		ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: badStatusMessage},
	}, okHandler); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("status message 身份错误=%v", err)
	}

	terminalEvent := validA2ATestEvent(t, round, 2, a2aext.EventExecutionTerminal, now, map[string]any{"status": string(a2aext.TerminalCompleted)})
	terminalHistory := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(terminalEvent))
	terminalHistory.Extensions = []string{a2aext.ExtensionURI}
	if _, err := emitA2ATask(ctx, round, &a2a.Task{
		ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking}, History: []*a2a.Message{terminalHistory},
	}, okHandler); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("非终态快照含 terminal event 错误=%v", err)
	}

	normalEvent := validA2ATestEvent(t, round, 1, a2aext.EventLogChunk, now, map[string]any{"stream": string(a2aext.LogStdout), "content": "line"})
	normalMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(normalEvent))
	normalMessage.Extensions = []string{a2aext.ExtensionURI}
	wantErr := errors.New("handler failed")
	if _, err := emitA2ATask(ctx, round, &a2a.Task{
		ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking}, History: []*a2a.Message{normalMessage},
	}, func(context.Context, app.A2ARemoteUpdate) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("事件投影错误=%v", err)
	}

	rejectedTask := &a2a.Task{ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateRejected}}
	terminal, err := emitA2ATask(ctx, round, rejectedTask, okHandler)
	if err != nil || !terminal {
		t.Fatalf("早期 rejected task=%v, %v", terminal, err)
	}
	if _, err := emitA2ATask(ctx, round, rejectedTask, func(context.Context, app.A2ARemoteUpdate) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("早期 rejected handler 错误=%v", err)
	}
	completedWithoutEvent := &a2a.Task{ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	if _, err := emitA2ATask(ctx, round, completedWithoutEvent, okHandler); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("完成 Task 缺 terminal event 错误=%v", err)
	}
}

func TestWorkerGatewayA2ARejectsMissingInputs(t *testing.T) {
	gateway := &WorkerGateway{}
	if err := gateway.Dispatch(context.Background(), nil, nil); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("nil dispatch 错误=%v", err)
	}
	if err := gateway.Reconcile(context.Background(), domain.TaskA2ARound{}, nil); !errors.Is(err, app.ErrA2AProtocolConflict) {
		t.Fatalf("nil reconcile 错误=%v", err)
	}
	var tunnel *workerProxyTunnel
	tunnel.close()
	(&workerProxyTunnel{}).close()
}

func TestA2AArtifactValidatorRejectsNilScopeAndLogStreamMismatch(t *testing.T) {
	now := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	round := domain.TaskA2ARound{TaskID: "task", ExecutionID: "execution", Attempt: 1, Turn: 1, WorkerID: "worker"}
	metadata := a2aext.ArtifactMetadata{
		Kind: a2aext.ArtifactKind, Version: a2aext.Version, Role: a2aext.ArtifactLog,
		ExecutionID: round.ExecutionID, Attempt: round.Attempt, Turn: round.Turn,
		EventID: "018f0000-0000-7000-8000-000000000001", Sequence: 1,
		Stream: a2aext.LogStdout, CreatedAt: now,
	}
	artifact := &a2a.Artifact{ID: a2a.ArtifactID(metadata.EventID), Name: string(metadata.Role)}
	if err := validateA2AArtifactEvent(round, artifact, metadata, nil); err == nil {
		t.Fatal("nil Artifact event 未失败")
	}
	event := validA2ATestEvent(t, round, 1, a2aext.EventLogChunk, now, map[string]any{"stream": string(a2aext.LogStderr), "content": "line"})
	if err := validateA2AArtifactEvent(round, artifact, metadata, event); err == nil {
		t.Fatal("日志 stream 不一致未失败")
	}
	metadata.ExecutionID = "other"
	if err := validateA2AArtifactEvent(round, artifact, metadata, event); err == nil {
		t.Fatal("Artifact scope 不一致未失败")
	}
}

func TestRequestWithoutManagerTokenQueryBranches(t *testing.T) {
	if requestWithoutManagerTokenQuery(nil, "token") != nil {
		t.Fatal("nil request 应保持 nil")
	}
	request := httptest.NewRequest("GET", "/proxy?token=other&keep=1", nil)
	if got := requestWithoutManagerTokenQuery(request, "token"); got != request {
		t.Fatal("不匹配 token 不应克隆请求")
	}
	request = httptest.NewRequest("GET", "/proxy?token=token&keep=1", nil)
	got := requestWithoutManagerTokenQuery(request, "token")
	if got == request || got.URL.Query().Get("token") != "" || got.URL.Query().Get("keep") != "1" {
		t.Fatalf("Manager token 清理结果 = %s", got.URL.RawQuery)
	}
}
