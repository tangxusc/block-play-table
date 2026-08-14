package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

// TestValidateWorkerAgentCard 验证 Manager 只接受与 Worker 注册能力完全一致的 Card。
func TestValidateWorkerAgentCard(t *testing.T) {
	target := workerA2ATarget{
		worker: &domain.Worker{ID: "worker-card"}, host: "127.0.0.1", port: 39123,
		endpointPath: "/a2a", cardPath: "/.well-known/agent-card.json",
	}
	valid := validA2ATestCard(target)
	if err := validateWorkerAgentCard(valid, target); err != nil {
		t.Fatalf("合法 Agent Card 被拒绝: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*a2a.AgentCard)
	}{
		{name: "nil card", mutate: func(card *a2a.AgentCard) { *card = a2a.AgentCard{} }},
		{name: "streaming disabled", mutate: func(card *a2a.AgentCard) { card.Capabilities.Streaming = false }},
		{name: "extension optional", mutate: func(card *a2a.AgentCard) { card.Capabilities.Extensions[0].Required = false }},
		{name: "wrong endpoint", mutate: func(card *a2a.AgentCard) { card.SupportedInterfaces[0].URL = "http://127.0.0.1:39123/outside" }},
		{name: "wrong protocol", mutate: func(card *a2a.AgentCard) { card.SupportedInterfaces[0].ProtocolBinding = a2a.TransportProtocolHTTPJSON }},
		{name: "missing claude skill", mutate: func(card *a2a.AgentCard) { card.Skills = card.Skills[:1] }},
		{name: "duplicate codex skill", mutate: func(card *a2a.AgentCard) { card.Skills[1] = card.Skills[0] }},
		{name: "extra skill", mutate: func(card *a2a.AgentCard) {
			card.Skills = append(card.Skills, a2a.AgentSkill{ID: "unexpected", Name: "Unexpected", Description: "unexpected", Tags: []string{"unexpected"}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			card := validA2ATestCard(target)
			test.mutate(card)
			if err := validateWorkerAgentCard(card, target); err == nil {
				t.Fatal("非法 Agent Card 未被拒绝")
			}
		})
	}
	if err := validateWorkerAgentCard(nil, target); err == nil {
		t.Fatal("nil Agent Card 未被拒绝")
	}
}

// TestA2AExecutionEventExtractionAndProjection 验证 Artifact 和 Message 事件统一按序投影。
func TestA2AExecutionEventExtractionAndProjection(t *testing.T) {
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	round := domain.TaskA2ARound{
		ID: "round-transport", TaskID: "task-transport", ExecutionID: "execution-transport",
		Attempt: 1, Turn: 1, WorkerID: "worker-transport",
	}
	first := validA2ATestEvent(t, round, 1, a2aext.EventLogChunk, now, map[string]any{
		"stream": string(a2aext.LogStdout), "content": "first log", "chunkIndex": int64(0), "finalChunk": true,
	})
	firstArtifact := validA2ATestArtifact(t, round, first, a2aext.ArtifactLog)
	second := validA2ATestEvent(t, round, 2, a2aext.EventResultUpdated, now.Add(time.Second), map[string]any{"result": "done"})
	resultMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(second))
	resultMessage.TaskID = a2a.TaskID("remote-task")
	resultMessage.ContextID = "remote-context"
	resultMessage.Extensions = []string{a2aext.ExtensionURI}
	third := validA2ATestEvent(t, round, 3, a2aext.EventExecutionTerminal, now.Add(2*time.Second), map[string]any{
		"status": string(a2aext.TerminalCompleted), "result": "done",
	})
	statusMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(third))
	statusMessage.TaskID = a2a.TaskID("remote-task")
	statusMessage.ContextID = "remote-context"
	statusMessage.Extensions = []string{a2aext.ExtensionURI}
	task := &a2a.Task{
		ID: a2a.TaskID("remote-task"), ContextID: "remote-context",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: statusMessage},
		History:   []*a2a.Message{resultMessage},
		Artifacts: []*a2a.Artifact{firstArtifact, validA2ATestArtifact(t, round, third, a2aext.ArtifactManifest)},
	}

	var updates []app.A2ARemoteUpdate
	terminal, err := emitA2ATask(context.Background(), round, task, func(_ context.Context, update app.A2ARemoteUpdate) error {
		updates = append(updates, update)
		return nil
	})
	if err != nil || !terminal {
		t.Fatalf("emitA2ATask = %v, %v", terminal, err)
	}
	if len(updates) != 3 {
		t.Fatalf("投影更新数量 = %d，期望 3: %+v", len(updates), updates)
	}
	if updates[0].Sequence != 1 || updates[0].Event == nil || updates[0].Event.Event.Type != a2aext.EventLogChunk {
		t.Fatalf("第一条更新 = %+v", updates[0])
	}
	if updates[0].Event.Payload["content"] != "first log" || updates[0].Event.Payload["stream"] != string(a2aext.LogStdout) {
		t.Fatalf("合成日志 payload = %+v", updates[0].Event.Payload)
	}
	if updates[1].Sequence != 2 || updates[1].Event == nil || updates[1].Event.Event.Type != a2aext.EventResultUpdated {
		t.Fatalf("第二条更新 = %+v", updates[1])
	}
	if updates[2].Sequence != 3 || updates[2].Event == nil || updates[2].Event.Event.Type != a2aext.EventExecutionTerminal || updates[2].Status != domain.TaskA2ARemoteStatusCompleted {
		t.Fatalf("最终状态更新 = %+v", updates[2])
	}

	extracted, err := executionEventsFromArtifact(round, firstArtifact)
	if err != nil || len(extracted) != 1 || extracted[0].Scope.ExecutionID != round.ExecutionID {
		t.Fatalf("executionEventsFromArtifact = %+v", extracted)
	}
	if got, err := executionEventsFromMessage(round, nil); err != nil || got != nil {
		t.Fatalf("nil Message 提取结果 = %+v", got)
	}
	if got, err := executionEventsFromArtifact(round, nil); err != nil || got != nil {
		t.Fatalf("nil Artifact 提取结果 = %+v", got)
	}
}

// TestEmitA2AEventVariantsAndErrors 验证 SDK 事件分支、终态和错误传播。
func TestEmitA2AEventVariantsAndErrors(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	round := domain.TaskA2ARound{TaskID: "task-event", ExecutionID: "execution-event", Attempt: 1, Turn: 1, WorkerID: "worker-event"}
	event := validA2ATestEvent(t, round, 1, a2aext.EventConversationMessage, now, map[string]any{"role": "assistant", "content": "hello"})
	message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(event))
	message.TaskID = a2a.TaskID("remote-event")
	message.ContextID = "context-event"
	message.Extensions = []string{a2aext.ExtensionURI}

	var count int
	handler := func(_ context.Context, update app.A2ARemoteUpdate) error {
		count++
		if update.TaskID != "remote-event" || update.ContextID != "context-event" {
			return fmt.Errorf("错误远端身份: %+v", update)
		}
		return nil
	}
	if terminal, err := emitA2AEvent(context.Background(), round, message, handler); err != nil || terminal {
		t.Fatalf("Message 事件 = %v, %v", terminal, err)
	}
	artifactEvent := &a2a.TaskArtifactUpdateEvent{
		TaskID: a2a.TaskID("remote-event"), ContextID: "context-event",
		Artifact: validA2ATestArtifact(t, round, event, a2aext.ArtifactConversation),
	}
	if terminal, err := emitA2AEvent(context.Background(), round, artifactEvent, handler); err != nil || terminal {
		t.Fatalf("Artifact 事件 = %v, %v", terminal, err)
	}
	statusEvent := &a2a.TaskStatusUpdateEvent{
		TaskID: a2a.TaskID("remote-event"), ContextID: "context-event",
		Status: a2a.TaskStatus{State: a2a.TaskStateFailed, Message: message},
	}
	if terminal, err := emitA2AEvent(context.Background(), round, statusEvent, handler); err != nil || !terminal {
		t.Fatalf("Status 事件 = %v, %v", terminal, err)
	}
	if count != 4 {
		t.Fatalf("handler 调用次数 = %d，期望 4", count)
	}

	wantErr := errors.New("projection stopped")
	if _, err := emitA2AEvent(context.Background(), round, message, func(context.Context, app.A2ARemoteUpdate) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("投影错误 = %v", err)
	}
	if _, err := emitA2AEvent(context.Background(), round, nil, handler); err == nil {
		t.Fatal("未知 SDK 事件未返回错误")
	}
	if _, err := emitA2ATask(context.Background(), round, nil, handler); err == nil {
		t.Fatal("nil Task 未返回错误")
	}
}

// TestReconcileA2AInputRequiredDoesNotResubscribe 验证等待交互时不会把已结束的 SDK execution 误判为远端 Task 丢失。
func TestReconcileA2AInputRequiredDoesNotResubscribe(t *testing.T) {
	round := domain.TaskA2ARound{
		ID: "round-waiting-input", TaskID: "task-waiting-input", ExecutionID: "execution-waiting-input",
		Attempt: 1, Turn: 1, WorkerID: "worker-waiting-input", A2ATaskID: "remote-waiting-input", ContextID: "context-waiting-input",
	}
	event := validA2ATestEvent(t, round, 1, a2aext.EventInteractionRequested, time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC), map[string]any{
		"interactionId": "interaction-waiting-input", "kind": string(a2aext.InteractionUserInput), "title": "User input", "body": "Continue?",
	})
	message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(event))
	message.TaskID = a2a.TaskID(round.A2ATaskID)
	message.ContextID = round.ContextID
	message.Extensions = []string{a2aext.ExtensionURI}
	transport := &waitingInputA2ATransport{task: &a2a.Task{
		ID: a2a.TaskID(round.A2ATaskID), ContextID: round.ContextID,
		Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: message},
	}}
	card := validA2ATestCard(workerA2ATarget{
		worker: &domain.Worker{ID: round.WorkerID}, host: "127.0.0.1", port: 39124,
		endpointPath: "/a2a", cardPath: "/.well-known/agent-card.json",
	})
	client, err := a2aclient.NewFromCard(context.Background(), card,
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithTransport(a2a.TransportProtocolJSONRPC, a2aclient.TransportFactoryFn(
			func(context.Context, *a2a.AgentCard, *a2a.AgentInterface) (a2aclient.Transport, error) {
				return transport, nil
			},
		)),
	)
	if err != nil {
		t.Fatalf("创建测试 A2A client: %v", err)
	}

	var updates []app.A2ARemoteUpdate
	if err := reconcileA2ATask(context.Background(), client, round, func(_ context.Context, update app.A2ARemoteUpdate) error {
		updates = append(updates, update)
		return nil
	}); err != nil {
		t.Fatalf("等待交互对账返回错误: %v", err)
	}
	if transport.subscribeCalls != 0 {
		t.Fatalf("等待交互时 SubscribeToTask 调用次数 = %d，期望 0", transport.subscribeCalls)
	}
	if len(updates) != 2 || updates[0].Event == nil || updates[0].Event.Event.Type != a2aext.EventInteractionRequested ||
		updates[1].Status != domain.TaskA2ARemoteStatusInputRequired {
		t.Fatalf("等待交互状态投影 = %+v", updates)
	}
}

type waitingInputA2ATransport struct {
	a2aclient.Transport
	task           *a2a.Task
	subscribeCalls int
}

func (t *waitingInputA2ATransport) GetTask(context.Context, a2aclient.ServiceParams, *a2a.GetTaskRequest) (*a2a.Task, error) {
	return t.task, nil
}

func (t *waitingInputA2ATransport) SubscribeToTask(context.Context, a2aclient.ServiceParams, *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	t.subscribeCalls++
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, a2a.ErrTaskNotFound)
	}
}

// TestA2ATransportDecodersAndStateMapping 验证非法扩展数据不会进入投影。
func TestA2ATransportDecodersAndStateMapping(t *testing.T) {
	now := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	round := domain.TaskA2ARound{TaskID: "task-decode", ExecutionID: "execution-decode", Attempt: 1, Turn: 1, WorkerID: "worker-decode"}
	validEvent := validA2ATestEvent(t, round, 1, a2aext.EventExecutionAccepted, now, map[string]any{})
	if decoded, ok, err := decodeA2AExecutionEvent(validEvent); err != nil || !ok || decoded.Event.ID != validEvent.Event.ID {
		t.Fatalf("合法事件解码 = %+v, %v, %v", decoded, ok, err)
	}
	for name, input := range map[string]any{
		"nil":        nil,
		"wrong kind": map[string]any{"kind": "other", "content": "ordinary data"},
	} {
		t.Run(name, func(t *testing.T) {
			if decoded, ok, err := decodeA2AExecutionEvent(input); err != nil || ok || decoded != nil {
				t.Fatalf("普通 DataPart 解码 = %+v, %v, %v", decoded, ok, err)
			}
		})
	}
	unknownEvent := toA2ATestMap(t, validEvent)
	unknownEvent["unexpected"] = true
	for name, input := range map[string]any{
		"invalid event": map[string]any{"kind": a2aext.EventKind, "version": a2aext.Version},
		"unknown field": unknownEvent,
	} {
		t.Run(name, func(t *testing.T) {
			decoded, ok, err := decodeA2AExecutionEvent(input)
			if decoded != nil || ok || !errors.Is(err, app.ErrA2AProtocolConflict) {
				t.Fatalf("畸形 execution DataPart 解码 = %+v, %v, %v", decoded, ok, err)
			}
		})
	}
	ordinary, err := executionEventsFromParts(a2a.ContentParts{a2a.NewDataPart(map[string]any{"kind": "other", "content": "ordinary data"})})
	if err != nil || len(ordinary) != 0 {
		t.Fatalf("普通非 execution DataPart = %+v, %v", ordinary, err)
	}

	metadata := a2aext.ArtifactMetadata{
		Kind: a2aext.ArtifactKind, Version: a2aext.Version, Role: a2aext.ArtifactResult,
		ExecutionID: round.ExecutionID, Attempt: 1, Turn: 1, EventID: mustA2ATestEventID(t),
		Sequence: 1, FinalChunk: true, MIMEType: "text/plain", CreatedAt: now,
	}
	if decoded, ok, err := decodeA2AArtifactMetadata(map[string]any{a2aext.ExtensionURI: toA2ATestMap(t, metadata)}); err != nil || !ok || decoded.EventID != metadata.EventID {
		t.Fatalf("合法 metadata 解码 = %+v, %v, %v", decoded, ok, err)
	}
	if _, ok, err := decodeA2AArtifactMetadata(nil); err != nil || ok {
		t.Fatal("nil metadata 被接受")
	}
	if _, ok, err := decodeA2AArtifactMetadata(map[string]any{"kind": "other"}); err != nil || ok {
		t.Fatal("非法 metadata 被接受")
	}
	unknownMetadata := toA2ATestMap(t, metadata)
	unknownMetadata["unexpected"] = true
	for name, input := range map[string]map[string]any{
		"declared wrong kind": {a2aext.ExtensionURI: map[string]any{"kind": "other"}},
		"unknown field":       {a2aext.ExtensionURI: unknownMetadata},
	} {
		t.Run(name, func(t *testing.T) {
			_, ok, err := decodeA2AArtifactMetadata(input)
			if ok || !errors.Is(err, app.ErrA2AProtocolConflict) {
				t.Fatalf("畸形 execution metadata = %v, %v", ok, err)
			}
		})
	}

	tests := map[a2a.TaskState]domain.TaskA2ARemoteStatus{
		a2a.TaskStateSubmitted:     domain.TaskA2ARemoteStatusSubmitted,
		a2a.TaskStateWorking:       domain.TaskA2ARemoteStatusWorking,
		a2a.TaskStateInputRequired: domain.TaskA2ARemoteStatusInputRequired,
		a2a.TaskStateAuthRequired:  domain.TaskA2ARemoteStatusAuthRequired,
		a2a.TaskStateCompleted:     domain.TaskA2ARemoteStatusCompleted,
		a2a.TaskStateFailed:        domain.TaskA2ARemoteStatusFailed,
		a2a.TaskStateRejected:      domain.TaskA2ARemoteStatusRejected,
		a2a.TaskStateCanceled:      domain.TaskA2ARemoteStatusCanceled,
		"custom":                   domain.TaskA2ARemoteStatusUnspecified,
	}
	for state, want := range tests {
		if got := mapA2ATaskState(state); got != want {
			t.Fatalf("mapA2ATaskState(%q) = %q，期望 %q", state, got, want)
		}
	}
}

// TestA2AArtifactStrictValidation 验证 execution Artifact 声明与事件必须逐字段对齐。
func TestA2AArtifactStrictValidation(t *testing.T) {
	now := time.Date(2026, 8, 10, 13, 30, 0, 0, time.UTC)
	round := domain.TaskA2ARound{TaskID: "task-artifact", ExecutionID: "execution-artifact", Attempt: 2, Turn: 3, WorkerID: "worker-artifact"}

	tests := []struct {
		name   string
		mutate func(*a2a.Artifact)
	}{
		{name: "missing extension", mutate: func(artifact *a2a.Artifact) { artifact.Extensions = nil }},
		{name: "duplicate extension", mutate: func(artifact *a2a.Artifact) {
			artifact.Extensions = []string{a2aext.ExtensionURI, a2aext.ExtensionURI}
		}},
		{name: "missing metadata", mutate: func(artifact *a2a.Artifact) { delete(artifact.Metadata, a2aext.ExtensionURI) }},
		{name: "metadata only", mutate: func(artifact *a2a.Artifact) { artifact.Parts = nil }},
		{name: "duplicate execution event", mutate: func(artifact *a2a.Artifact) {
			artifact.Parts = append(artifact.Parts, artifact.Parts[0])
		}},
		{name: "artifact id mismatch", mutate: func(artifact *a2a.Artifact) { artifact.ID = "other-event" }},
		{name: "artifact role mismatch", mutate: func(artifact *a2a.Artifact) { artifact.Name = string(a2aext.ArtifactLog) }},
		{name: "metadata event id mismatch", mutate: func(artifact *a2a.Artifact) {
			artifact.Metadata[a2aext.ExtensionURI].(map[string]any)["eventId"] = mustA2ATestEventID(t)
		}},
		{name: "metadata sequence mismatch", mutate: func(artifact *a2a.Artifact) {
			artifact.Metadata[a2aext.ExtensionURI].(map[string]any)["sequence"] = float64(2)
		}},
		{name: "metadata execution mismatch", mutate: func(artifact *a2a.Artifact) {
			artifact.Metadata[a2aext.ExtensionURI].(map[string]any)["executionId"] = "other-execution"
		}},
		{name: "metadata attempt mismatch", mutate: func(artifact *a2a.Artifact) {
			artifact.Metadata[a2aext.ExtensionURI].(map[string]any)["attempt"] = float64(round.Attempt + 1)
		}},
		{name: "metadata turn mismatch", mutate: func(artifact *a2a.Artifact) {
			artifact.Metadata[a2aext.ExtensionURI].(map[string]any)["turn"] = float64(round.Turn + 1)
		}},
		{name: "metadata role event mismatch", mutate: func(artifact *a2a.Artifact) {
			artifact.Name = string(a2aext.ArtifactLog)
			metadata := artifact.Metadata[a2aext.ExtensionURI].(map[string]any)
			metadata["role"] = string(a2aext.ArtifactLog)
			metadata["stream"] = string(a2aext.LogStdout)
			metadata["chunkIndex"] = float64(0)
		}},
		{name: "metadata unknown field", mutate: func(artifact *a2a.Artifact) {
			artifact.Metadata[a2aext.ExtensionURI].(map[string]any)["unexpected"] = true
		}},
		{name: "event unknown field", mutate: func(artifact *a2a.Artifact) {
			event := toA2ATestMap(t, artifact.Parts[0].Data())
			event["unexpected"] = true
			artifact.Parts = a2a.ContentParts{a2a.NewDataPart(event)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validA2ATestEvent(t, round, 1, a2aext.EventResultUpdated, now, map[string]any{"result": "done"})
			artifact := validA2ATestArtifact(t, round, event, a2aext.ArtifactResult)
			test.mutate(artifact)
			if _, err := executionEventsFromArtifact(round, artifact); !errors.Is(err, app.ErrA2AProtocolConflict) {
				t.Fatalf("畸形 Artifact 错误 = %v", err)
			}
		})
	}

	event := validA2ATestEvent(t, round, 1, a2aext.EventExecutionAccepted, now, map[string]any{})
	for name, extensions := range map[string][]string{
		"missing message extension":   nil,
		"duplicate message extension": {a2aext.ExtensionURI, a2aext.ExtensionURI},
	} {
		t.Run(name, func(t *testing.T) {
			message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(event))
			message.Extensions = extensions
			if _, err := executionEventsFromMessage(round, message); !errors.Is(err, app.ErrA2AProtocolConflict) {
				t.Fatalf("畸形 Message 错误 = %v", err)
			}
		})
	}
}

// TestIdleReadCloserTimesOutAndCloses 验证 SSE 长时间无字节时主动断开以触发对账。
func TestIdleReadCloserTimesOutAndCloses(t *testing.T) {
	reader, writer := net.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	idle := newIdleReadCloser(reader, 10*time.Millisecond)
	buffer := make([]byte, 1)
	_, err := idle.Read(buffer)
	if !errors.Is(err, errA2AIdleTimeout) {
		t.Fatalf("空闲读取错误 = %v", err)
	}
	if err := idle.Close(); err != nil {
		t.Fatalf("重复 Close 返回错误: %v", err)
	}
	if _, err := idle.Read(buffer); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("关闭后读取错误 = %v", err)
	}
}

func validA2ATestCard(target workerA2ATarget) *a2a.AgentCard {
	endpoint := "http://" + net.JoinHostPort(target.host, fmt.Sprint(target.port)) + target.endpointPath
	return &a2a.AgentCard{
		Name: "A2A test worker", Description: "test", Version: a2aext.Version,
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(endpoint, a2a.TransportProtocolJSONRPC)},
		Capabilities: a2a.AgentCapabilities{
			Streaming:  true,
			Extensions: []a2a.AgentExtension{{URI: a2aext.ExtensionURI, Required: true}},
		},
		DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"application/json"},
		Skills: []a2a.AgentSkill{
			{ID: "block-play-table-codex", Name: "Execute with codex", Description: "Execute a test task with Codex", Tags: []string{"coding", "codex"}},
			{ID: "block-play-table-claude", Name: "Execute with claude", Description: "Execute a test task with Claude", Tags: []string{"coding", "claude"}},
		},
	}
}

func validA2ATestEvent(t *testing.T, round domain.TaskA2ARound, sequence int64, eventType a2aext.EventType, occurredAt time.Time, payload map[string]any) *a2aext.ExecutionEvent {
	t.Helper()
	event := &a2aext.ExecutionEvent{
		Kind: a2aext.EventKind, Version: a2aext.Version,
		Event: a2aext.EventHeader{ID: mustA2ATestEventID(t), Sequence: sequence, Type: eventType, OccurredAt: occurredAt},
		Scope: a2aext.EventScope{
			LocalTaskID: round.TaskID, ExecutionID: round.ExecutionID, Attempt: round.Attempt,
			Turn: round.Turn, WorkerID: round.WorkerID,
		},
		Payload: payload,
	}
	if err := a2aext.ValidateEvent(event); err != nil {
		t.Fatalf("测试事件非法: %v", err)
	}
	return event
}

func validA2ATestArtifact(t *testing.T, round domain.TaskA2ARound, event *a2aext.ExecutionEvent, role a2aext.ArtifactRole) *a2a.Artifact {
	t.Helper()
	metadata := a2aext.ArtifactMetadata{
		Kind: a2aext.ArtifactKind, Version: a2aext.Version, Role: role,
		ExecutionID: round.ExecutionID, Attempt: round.Attempt, Turn: round.Turn,
		EventID: event.Event.ID, Sequence: event.Event.Sequence, FinalChunk: true,
		MIMEType: "application/json", CreatedAt: event.Event.OccurredAt,
	}
	if role == a2aext.ArtifactLog {
		metadata.Stream = a2aext.LogStream(event.Payload["stream"].(string))
		metadata.ChunkIndex = event.Payload["chunkIndex"].(int64)
	}
	if err := a2aext.ValidateArtifact(&metadata); err != nil {
		t.Fatalf("测试 Artifact metadata 非法: %v", err)
	}
	return &a2a.Artifact{
		ID: a2a.ArtifactID(event.Event.ID), Name: string(role), Extensions: []string{a2aext.ExtensionURI},
		Metadata: map[string]any{a2aext.ExtensionURI: toA2ATestMap(t, metadata)},
		Parts:    a2a.ContentParts{a2a.NewDataPart(event)},
	}
}

func mustA2ATestEventID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func toA2ATestMap(t *testing.T, value any) map[string]any {
	t.Helper()
	// SDK DataPart 保留强类型值时，通过 JSON 解码得到传输后的 map 形态。
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("编码测试扩展数据 %T: %v", value, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("解码测试扩展数据 %T: %v", value, err)
	}
	return decoded
}
