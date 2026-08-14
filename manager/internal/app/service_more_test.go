package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestServiceSettingsConversationFailureHeartbeatAndArchive(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventConversationMessage, domain.TaskA2ARemoteStatusWorking, nil, map[string]any{"role": "assistant", "content": "hello"})
	if messages, _ := service.Store().TaskConversations(ctx, task.ID); len(messages) != 1 {
		t.Fatalf("conversation count = %d", len(messages))
	}
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusFailed, nil, map[string]any{"status": string(a2aext.TerminalFailed), "message": "boom"})
	loaded, _ := service.Task(ctx, task.ID)
	if loaded.Status != domain.TaskFailed {
		t.Fatalf("status = %s, want failed", loaded.Status)
	}
	worker, err := service.Store().Worker(ctx, "worker-1")
	if err != nil {
		t.Fatalf("Worker returned error: %v", err)
	}
	if len(worker.CurrentTaskIDs) != 0 {
		t.Fatalf("worker current tasks = %+v, want released after failure", worker.CurrentTaskIDs)
	}
	if _, err := service.WorkerHeartbeat(ctx, "worker-1"); err != nil {
		t.Fatalf("WorkerHeartbeat returned error: %v", err)
	}
	project, _ := service.CreateProject(ctx, CreateProjectInput{Name: "Archive", GitURL: "git://archive"})
	if archived, err := service.ArchiveProject(ctx, project.ID); err != nil || !archived.Archived {
		t.Fatalf("ArchiveProject = %+v, %v", archived, err)
	}
}

func TestWorkerHeartbeatRestoresStaleWorkerWithoutEnablingDisabledWorker(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID: "worker-heartbeat-recovery", Name: "Heartbeat recovery", WorkDir: "/tmp/worker-heartbeat-recovery",
		SupportedAgents: []domain.AgentType{domain.AgentCodex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerDisconnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.WorkerHeartbeat(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != domain.WorkerOnline {
		t.Fatalf("心跳恢复状态 = %s，期望 %s", recovered.Status, domain.WorkerOnline)
	}

	recovered.Disable(time.Now().UTC())
	if err := service.Store().SaveWorker(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	disabled, err := service.WorkerHeartbeat(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Status != domain.WorkerDisabled {
		t.Fatalf("禁用 Worker 心跳状态 = %s，期望 %s", disabled.Status, domain.WorkerDisabled)
	}
}

func TestServiceTaskInteractionLifecycleAndDedup(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)

	round, requested := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, &a2aext.RuntimeInfo{AgentSessionID: "session-1"}, map[string]any{
		"interactionId": "interaction-1",
		"kind":          string(a2aext.InteractionCommandApproval),
		"title":         "Command approval",
		"body":          "Run tests",
		"rawPayload":    `{"command":"make test"}`,
	})
	if err := applyTestA2ARawEvent(ctx, service, round, requested, domain.TaskA2ARemoteStatusInputRequired); err != nil {
		t.Fatalf("首次 interaction.requested 返回错误: %v", err)
	}
	waiting := loadTestA2ATask(t, ctx, service, task.ID)
	if waiting.Status != domain.TaskWaitingInput || waiting.AgentSessionID != "session-1" {
		t.Fatalf("task after request = %+v", waiting)
	}
	if err := applyTestA2ARawEvent(ctx, service, round, requested, domain.TaskA2ARemoteStatusInputRequired); err != nil {
		t.Fatalf("重复 interaction.requested 返回错误: %v", err)
	}
	interactions, err := service.Store().TaskInteractions(ctx, task.ID, domain.TaskInteractionPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 1 || interactions[0].Title != "Command approval" {
		t.Fatalf("interactions = %+v", interactions)
	}

	answered, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-1",
		Decision:      domain.TaskInteractionApprove,
	})
	if err != nil {
		t.Fatalf("RespondTaskInteraction returned error: %v", err)
	}
	if answered.Status != domain.TaskInteractionAnswered || answered.ResponseDecision != domain.TaskInteractionApprove {
		t.Fatalf("answered interaction = %+v", answered)
	}
	resumed := applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionResolved, domain.TaskA2ARemoteStatusWorking, &a2aext.RuntimeInfo{AgentSessionID: "session-1"}, map[string]any{
		"interactionId": "interaction-1",
		"kind":          string(a2aext.InteractionCommandApproval),
		"decision":      string(a2aext.DecisionApprove),
	})
	if resumed.Status != domain.TaskRunning {
		t.Fatalf("task after resolved = %+v", resumed)
	}
}

func TestA2ARoundStatusOnlyUpdatePublishesTaskRefreshEvent(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)
	round, requested := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, nil, map[string]any{
		"interactionId": "interaction-refresh", "kind": string(a2aext.InteractionCommandApproval),
		"title": "Command approval", "body": "Run tests",
	})
	if err := applyTestA2ARawEvent(ctx, service, round, requested, ""); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{
		AggregateType: "Task", AggregateID: task.ID, EventType: "TaskA2AExecutionUpdated",
	})
	defer unsubscribe()
	applyTestA2AStatus(t, ctx, service, task.ID, domain.TaskA2ARemoteStatusInputRequired)
	select {
	case event := <-events:
		if event.AggregateID != task.ID {
			t.Fatalf("刷新事件=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("只更新 A2A round 状态时未发布 Task 刷新事件")
	}
}

func TestServiceCancelTaskInteractionCreatesCancelIntentAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedRunningTask(t, ctx, service)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, domain.TaskA2ARemoteStatusInputRequired, &a2aext.RuntimeInfo{AgentSessionID: "session-cancel"}, map[string]any{
		"interactionId": "interaction-cancel",
		"kind":          string(a2aext.InteractionCommandApproval),
		"title":         "Command approval",
		"body":          "Run destructive command",
	})

	input := RespondTaskInteractionInput{
		InteractionID: "interaction-cancel",
		Decision:      domain.TaskInteractionCancel,
		Message:       "stop",
		Payload:       `{"reason":"user canceled"}`,
	}
	canceled, err := service.RespondTaskInteraction(ctx, input)
	if err != nil {
		t.Fatalf("取消交互返回错误: %v", err)
	}
	if canceled.Status != domain.TaskInteractionCanceled || canceled.ResponseDecision != domain.TaskInteractionCancel {
		t.Fatalf("取消后的 interaction = %+v", canceled)
	}
	updated, err := service.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.TaskInterrupting {
		t.Fatalf("取消交互后的 task status = %s，期望 %s", updated.Status, domain.TaskInterrupting)
	}
	intents, err := service.Store().A2ADispatchIntentsDue(ctx, now.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	cancelIntentIDs := make([]string, 0, 1)
	for _, intent := range intents {
		if intent.Operation == domain.TaskA2AOperationCancel {
			cancelIntentIDs = append(cancelIntentIDs, intent.ID)
			if len(intent.Payload) != 0 {
				t.Fatalf("CancelTask intent 不应携带请求 payload: %+v", intent)
			}
		}
	}
	if len(cancelIntentIDs) != 1 {
		t.Fatalf("取消 intent = %+v", intents)
	}

	replayed, err := service.RespondTaskInteraction(ctx, input)
	if err != nil || replayed.ID != canceled.ID {
		t.Fatalf("重复取消应幂等: interaction=%+v err=%v", replayed, err)
	}
	intents, err = service.Store().A2ADispatchIntentsDue(ctx, now.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	replayedCancelIDs := make([]string, 0, 1)
	for _, intent := range intents {
		if intent.Operation == domain.TaskA2AOperationCancel {
			replayedCancelIDs = append(replayedCancelIDs, intent.ID)
		}
	}
	if len(replayedCancelIDs) != 1 || replayedCancelIDs[0] != cancelIntentIDs[0] {
		t.Fatalf("重复取消创建了额外 intent: intents=%+v err=%v", intents, err)
	}
}

func TestServiceTaskInteractionResponseRequiresOnlineWorkerAndTerminalCancelsPending(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, domain.TaskA2ARemoteStatusInputRequired, &a2aext.RuntimeInfo{AgentSessionID: "session-offline"}, map[string]any{
		"interactionId": "interaction-offline",
		"kind":          string(a2aext.InteractionUserInput),
		"title":         "Question",
		"body":          "Need input",
	})
	if _, err := service.WorkerDisconnected(ctx, task.WorkerID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-offline",
		Message:       "answer",
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("offline response err = %v, want conflict", err)
	}

	if _, err := service.WorkerConnected(ctx, task.WorkerID); err != nil {
		t.Fatal(err)
	}
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusCompleted, nil, map[string]any{"status": string(a2aext.TerminalCompleted), "result": "done"})
	canceled, err := service.Store().TaskInteraction(ctx, "interaction-offline")
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.TaskInteractionCanceled || canceled.ResponseDecision != domain.TaskInteractionCancel {
		t.Fatalf("terminal task should cancel pending interaction, got %+v", canceled)
	}
}

func TestServiceTaskInteractionResponseRemainsIdempotentAfterCompletion(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	task := seedRunningTask(t, ctx, service)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, domain.TaskA2ARemoteStatusInputRequired, &a2aext.RuntimeInfo{AgentSessionID: "session-race"}, map[string]any{
		"interactionId": "interaction-race",
		"kind":          string(a2aext.InteractionCommandApproval),
		"title":         "Command approval",
	})
	answered, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-race",
		Decision:      domain.TaskInteractionApprove,
	})
	if err != nil {
		t.Fatalf("RespondTaskInteraction 返回错误: %v", err)
	}
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionResolved, domain.TaskA2ARemoteStatusWorking, nil, map[string]any{
		"interactionId": "interaction-race",
		"kind":          string(a2aext.InteractionCommandApproval),
		"decision":      string(a2aext.DecisionApprove),
	})
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, domain.TaskA2ARemoteStatusCompleted, nil, map[string]any{"status": string(a2aext.TerminalCompleted), "result": "done"})
	answered, err = service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-race",
		Decision:      domain.TaskInteractionApprove,
	})
	if err != nil {
		t.Fatalf("RespondTaskInteraction should be idempotent after resolved response: %v", err)
	}
	if answered.Status != domain.TaskInteractionAnswered || answered.ResponseDecision != domain.TaskInteractionApprove {
		t.Fatalf("interaction after fast completion = %+v", answered)
	}
}

func TestServiceRejectsUnavailableWorker(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-1", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentClaude}, WorkDir: "/tmp", BindingMode: domain.WorkerAllProjects})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.WorkerConnected(ctx, worker.ID)
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err == nil {
		t.Fatal("AssignWorker should reject unsupported agent")
	}
}

func TestServiceListHelpersAndSettings(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-list", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex}); err != nil {
		t.Fatal(err)
	}
	if projects, err := service.Projects(ctx); err != nil || len(projects) != 1 {
		t.Fatalf("Projects = %d, %v", len(projects), err)
	}
	if workers, err := service.Workers(ctx); err != nil || len(workers) != 1 {
		t.Fatalf("Workers = %d, %v", len(workers), err)
	}
	if tasks, err := service.Tasks(ctx); err != nil || len(tasks) != 1 {
		t.Fatalf("Tasks = %d, %v", len(tasks), err)
	}
	if settings, err := service.Settings(ctx); err != nil || settings.ID == "" {
		t.Fatalf("Settings = %+v, %v", settings, err)
	}
}

func TestServicePublishesDomainEventsAndMarksOutbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{EventType: "ProjectCreated"})
	defer unsubscribe()

	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.EventType != "ProjectCreated" || event.AggregateID != project.ID {
			t.Fatalf("published event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for domain event")
	}

	stored, err := service.DomainEvents(ctx, domain.EventFilter{AggregateID: project.ID})
	if err != nil || len(stored) != 1 {
		t.Fatalf("DomainEvents = %d, %v", len(stored), err)
	}
	outbox, err := service.OutboxMessages(ctx, true)
	if err != nil || len(outbox) != 1 {
		t.Fatalf("OutboxMessages = %d, %v", len(outbox), err)
	}
	if outbox[0].Status != domain.OutboxPublished || outbox[0].PublishedAt == nil {
		t.Fatalf("outbox message was not marked published: %+v", outbox[0])
	}
}

func TestServiceDeleteWorkerPublishesDomainEventAndMarksOutbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time {
		return time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	}))
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-delete", Name: "Delete Me", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{AggregateType: "Worker", EventType: "WorkerDeleted"})
	defer unsubscribe()

	if err := service.DeleteWorker(ctx, worker.ID); err != nil {
		t.Fatalf("DeleteWorker returned error: %v", err)
	}

	select {
	case event := <-events:
		if event.EventType != "WorkerDeleted" || event.AggregateID != worker.ID {
			t.Fatalf("published delete event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker delete event")
	}
	stored, err := service.DomainEvents(ctx, domain.EventFilter{AggregateID: worker.ID, EventType: "WorkerDeleted"})
	if err != nil || len(stored) != 1 {
		t.Fatalf("DomainEvents = %d, %v", len(stored), err)
	}
	outbox, err := service.OutboxMessages(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range outbox {
		if message.Event.EventType == "WorkerDeleted" && message.Event.AggregateID == worker.ID {
			found = message.Status == domain.OutboxPublished && message.PublishedAt != nil
		}
	}
	if !found {
		t.Fatalf("worker delete outbox was not published: %+v", outbox)
	}
}

func TestServiceDeleteTaskRequiresArchivedAndPublishesDomainEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "Delete Me", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteTask(ctx, task.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("DeleteTask active err = %v, want conflict", err)
	}
	if _, err := service.ArchiveTask(ctx, task.ID); err != nil {
		t.Fatalf("ArchiveTask returned error: %v", err)
	}
	if err := service.Store().AppendTaskLog(ctx, domain.TaskLog{ID: "log-delete", TaskID: task.ID, Stream: "stdout", Content: "hello", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := service.Store().AppendConversation(ctx, domain.ConversationMessage{ID: "msg-delete", TaskID: task.ID, Role: "assistant", Content: "done", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{AggregateType: "Task", EventType: "TaskDeleted"})
	defer unsubscribe()

	if err := service.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask archived returned error: %v", err)
	}
	select {
	case event := <-events:
		if event.EventType != "TaskDeleted" || event.AggregateID != task.ID {
			t.Fatalf("published delete event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task delete event")
	}
	if _, err := service.Task(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted Task err = %v, want not found", err)
	}
	if logs, err := service.Store().TaskLogs(ctx, task.ID); err != nil || len(logs) != 0 {
		t.Fatalf("TaskLogs after delete = %+v, %v", logs, err)
	}
	stored, err := service.DomainEvents(ctx, domain.EventFilter{AggregateID: task.ID, EventType: "TaskDeleted"})
	if err != nil || len(stored) != 1 {
		t.Fatalf("DomainEvents = %d, %v", len(stored), err)
	}
	outbox, err := service.OutboxMessages(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range outbox {
		if message.Event.EventType == "TaskDeleted" && message.Event.AggregateID == task.ID {
			found = message.Status == domain.OutboxPublished && message.PublishedAt != nil
		}
	}
	if !found {
		t.Fatalf("task delete outbox was not published: %+v", outbox)
	}
	if err := service.DeleteTask(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteTask missing err = %v, want not found", err)
	}
}

func TestServiceDeleteOccupiedWorkerDoesNotPublishDeleteEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewService(store.NewMemoryStore())
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "P", GitURL: "git://repo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-occupied", Name: "Busy", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "T", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := service.SubscribeDomainEvents(ctx, domain.EventFilter{AggregateType: "Worker", EventType: "WorkerDeleted"})
	defer unsubscribe()

	if err := service.DeleteWorker(ctx, worker.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("DeleteWorker occupied err = %v, want conflict", err)
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected delete event = %+v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestServiceMarksStaleWorkersOfflineAndReconnects(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-stale", Name: "W", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(91 * time.Second)
	if err := service.MarkStaleWorkersOffline(ctx, 90*time.Second); err != nil {
		t.Fatalf("MarkStaleWorkersOffline returned error: %v", err)
	}
	loaded, err := service.Store().Worker(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.WorkerOffline {
		t.Fatalf("status = %s, want OFFLINE", loaded.Status)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	loaded, _ = service.Store().Worker(ctx, worker.ID)
	if loaded.Status != domain.WorkerOnline {
		t.Fatalf("status = %s, want ONLINE after reconnect", loaded.Status)
	}
}

func TestServiceAutoAssignAllowsWorkersWithRunningTasks(t *testing.T) {
	ctx := context.Background()
	service := NewService(store.NewMemoryStore())
	targetProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Target", GitURL: "git://target"})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := service.CreateProject(ctx, CreateProjectInput{Name: "Other", GitURL: "git://other"})
	if err != nil {
		t.Fatal(err)
	}
	offline, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-offline", Name: "Offline", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	unsupported, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-unsupported", Name: "Unsupported", SupportedAgents: []domain.AgentType{domain.AgentClaude}, WorkDir: "/tmp"})
	wrongProject, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-wrong-project", Name: "Wrong Project", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp", BindingMode: domain.WorkerSpecificProjects, BoundProjectIDs: []string{otherProject.ID}})
	occupied, _ := service.RegisterWorker(ctx, RegisterWorkerInput{ID: "worker-occupied", Name: "Occupied", SupportedAgents: []domain.AgentType{domain.AgentCodex}, WorkDir: "/tmp"})
	for _, workerID := range []string{unsupported.ID, wrongProject.ID, occupied.ID} {
		if _, err := service.WorkerConnected(ctx, workerID); err != nil {
			t.Fatal(err)
		}
	}
	offline.MarkOffline(time.Now())
	if err := service.Store().SaveWorker(ctx, offline); err != nil {
		t.Fatal(err)
	}
	otherTask, err := service.CreateTask(ctx, CreateTaskInput{Title: "Other Task", ProjectID: targetProject.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, otherTask.ID, occupied.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, otherTask.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "Needs Worker", ProjectID: targetProject.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if started.WorkerID != occupied.ID {
		t.Fatalf("worker id = %q, want %q", started.WorkerID, occupied.ID)
	}
}
