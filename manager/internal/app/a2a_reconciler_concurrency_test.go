package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

type replayControlTransport struct {
	update A2ARemoteUpdate
}

func (t replayControlTransport) Dispatch(ctx context.Context, _ *A2ADispatchRequest, handler A2AUpdateHandler) error {
	return handler(ctx, t.update)
}

func (replayControlTransport) Reconcile(context.Context, domain.TaskA2ARound, A2AUpdateHandler) error {
	return nil
}

type blockingInteractionTransport struct {
	started chan struct{}
	release chan struct{}
}

func (t blockingInteractionTransport) Dispatch(ctx context.Context, _ *A2ADispatchRequest, _ A2AUpdateHandler) error {
	close(t.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.release:
		return nil
	}
}

func (blockingInteractionTransport) Reconcile(context.Context, domain.TaskA2ARound, A2AUpdateHandler) error {
	return nil
}

type a2aProjectionBarrierStore struct {
	store.Store
	targetIntentID string
	reached        chan struct{}
	release        chan struct{}
	result         chan error
	once           sync.Once
}

func newA2AProjectionBarrierStore(storage store.Store) *a2aProjectionBarrierStore {
	return &a2aProjectionBarrierStore{
		Store: storage, reached: make(chan struct{}), release: make(chan struct{}), result: make(chan error, 1),
	}
}

func (s *a2aProjectionBarrierStore) CommitA2AProjection(ctx context.Context, commit store.A2AProjectionCommit) (store.A2AEventDisposition, error) {
	blocked := false
	if commit.Inbox != nil && commit.Intent != nil && commit.Intent.ID == s.targetIntentID {
		s.once.Do(func() {
			blocked = true
			close(s.reached)
		})
	}
	if blocked {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.release:
		}
	}
	disposition, err := s.Store.CommitA2AProjection(ctx, commit)
	if blocked {
		s.result <- err
	}
	return disposition, err
}

// TestA2AControlDispatchRecoversWhenReconcileWinsQueryCommitRace 验证控制命令首包竞态可由超时重领恢复。
func TestA2AControlDispatchRecoversWhenReconcileWinsQueryCommitRace(t *testing.T) {
	for _, operation := range []domain.TaskA2AOperation{
		domain.TaskA2AOperationCancel,
		domain.TaskA2AOperationInteractionResponse,
	} {
		t.Run(string(operation), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
			storage := newA2AProjectionBarrierStore(store.NewMemoryStore())
			service := NewService(storage, WithClock(func() time.Time { return now }))
			task := seedRunningTask(t, ctx, service)
			completeTestA2AIntent(t, ctx, service, domain.TaskA2AOperationStart, now)

			switch operation {
			case domain.TaskA2AOperationCancel:
				if _, err := service.InterruptTask(ctx, task.ID); err != nil {
					t.Fatal(err)
				}
			case domain.TaskA2AOperationInteractionResponse:
				applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, domain.TaskA2ARemoteStatusInputRequired, &a2aext.RuntimeInfo{AgentSessionID: "session-race"}, map[string]any{
					"interactionId": "interaction-race",
					"kind":          string(a2aext.InteractionUserInput),
					"title":         "Need input",
				})
				if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{
					InteractionID: "interaction-race",
					Message:       "approved input",
				}); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("未覆盖的控制操作 %s", operation)
			}

			intent := findTestA2AIntent(t, ctx, service, operation)
			expectedIntentVersion := intent.Version
			if err := intent.StartAttempt(now); err != nil {
				t.Fatal(err)
			}
			if err := service.Store().UpdateA2ADispatchIntent(ctx, intent, expectedIntentVersion); err != nil {
				t.Fatal(err)
			}
			storage.targetIntentID = intent.ID

			round, replay := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventLogChunk, nil, map[string]any{
				"stream":     string(a2aext.LogStdout),
				"content":    "single replayed line",
				"chunkIndex": float64(0),
			})
			update := A2ARemoteUpdate{
				TaskID: round.A2ATaskID, ContextID: round.ContextID, Status: domain.TaskA2ARemoteStatusWorking,
				Sequence: replay.Event.Sequence, Event: replay,
			}
			reconciler := NewReconciler(service, 0, time.Second,
				WithA2ATransport(replayControlTransport{update: update}),
				WithReconcilerLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			)
			dispatchDone := make(chan struct{})
			go func() {
				reconciler.runA2ADispatch(ctx, "dispatch-race", "", *intent)
				close(dispatchDone)
			}()

			select {
			case <-storage.reached:
			case <-time.After(time.Second):
				close(storage.release)
				t.Fatal("控制投影未进入 query/commit 竞态窗口")
			}
			// 在控制流完成预查后提交同一事件，稳定触发 duplicate inbox OCC。
			if err := applyTestA2ARawEvent(ctx, service, round, replay, domain.TaskA2ARemoteStatusWorking); err != nil {
				close(storage.release)
				t.Fatalf("并发 reconcile 首次投影返回错误: %v", err)
			}
			close(storage.release)
			select {
			case commitErr := <-storage.result:
				if !errors.Is(commitErr, store.ErrA2AConcurrentModification) {
					t.Fatalf("控制投影提交错误 = %v，期望 OCC", commitErr)
				}
			case <-time.After(time.Second):
				t.Fatal("未观察到控制投影提交结果")
			}
			select {
			case <-dispatchDone:
			case <-time.After(time.Second):
				t.Fatal("首次控制下发未在 OCC 后退出")
			}

			assertTestA2AIntentStatus(t, ctx, service, intent.ID, domain.A2ADispatchSending)
			assertSingleTestA2ALog(t, ctx, service, task.ID)

			now = now.Add(16 * time.Second)
			if err := reconciler.ReconcileA2A(ctx); err != nil {
				t.Fatalf("重领 intent 返回错误: %v", err)
			}
			waitForTestA2AIntentStatus(t, ctx, service, intent.ID, domain.A2ADispatchCompleted)
			assertSingleTestA2ALog(t, ctx, service, task.ID)
		})
	}
}

// TestA2AInteractionResponseWaitsForExclusiveTaskStream 验证交互回复不会与旧对账流并发投影同一 execution。
func TestA2AInteractionResponseWaitsForExclusiveTaskStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedRunningTask(t, ctx, service)
	completeTestA2AIntent(t, ctx, service, domain.TaskA2AOperationStart, now)
	applyTestA2AEvent(t, ctx, service, task.ID, a2aext.EventInteractionRequested, domain.TaskA2ARemoteStatusInputRequired, &a2aext.RuntimeInfo{AgentSessionID: "session-exclusive"}, map[string]any{
		"interactionId": "interaction-exclusive",
		"kind":          string(a2aext.InteractionUserInput),
		"title":         "Need input",
	})
	if _, err := service.RespondTaskInteraction(ctx, RespondTaskInteractionInput{
		InteractionID: "interaction-exclusive",
		Message:       "approved input",
	}); err != nil {
		t.Fatal(err)
	}

	transport := blockingInteractionTransport{started: make(chan struct{}), release: make(chan struct{})}
	reconciler := NewReconciler(service, 0, time.Second,
		WithA2ATransport(transport),
		WithReconcilerLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	streamKey := "stream:task:" + task.ID
	if !reconciler.activateA2A(streamKey) {
		t.Fatal("无法预占旧对账流")
	}
	if err := reconciler.ReconcileA2A(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-transport.started:
		t.Fatal("旧对账流仍活跃时不应下发交互回复")
	case <-time.After(20 * time.Millisecond):
	}
	intent := findTestA2AIntent(t, ctx, service, domain.TaskA2AOperationInteractionResponse)
	if intent.Status != domain.A2ADispatchPending {
		t.Fatalf("等待流锁时 intent 状态 = %s，期望 %s", intent.Status, domain.A2ADispatchPending)
	}

	reconciler.deactivateA2A(streamKey)
	if err := reconciler.ReconcileA2A(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("释放旧对账流后交互回复未启动")
	}
	if reconciler.activateA2A(streamKey) {
		reconciler.deactivateA2A(streamKey)
		t.Fatal("交互回复执行期间必须独占任务流")
	}
	close(transport.release)
	deadline := time.Now().Add(time.Second)
	for !reconciler.activateA2A(streamKey) {
		if time.Now().After(deadline) {
			t.Fatal("交互回复结束后任务流未释放")
		}
		time.Sleep(5 * time.Millisecond)
	}
	reconciler.deactivateA2A(streamKey)
}

func findTestA2AIntent(t *testing.T, ctx context.Context, service *Service, operation domain.TaskA2AOperation) *domain.TaskA2ADispatchIntent {
	t.Helper()
	intents, err := service.Store().A2ADispatchIntentsDue(ctx, time.Now().Add(100*365*24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for index := range intents {
		if intents[index].Operation == operation {
			intent := intents[index]
			return &intent
		}
	}
	t.Fatalf("未找到 %s intent", operation)
	return nil
}

func completeTestA2AIntent(t *testing.T, ctx context.Context, service *Service, operation domain.TaskA2AOperation, now time.Time) {
	t.Helper()
	intent := findTestA2AIntent(t, ctx, service, operation)
	expectedVersion := intent.Version
	intent.Complete(now)
	if err := service.Store().UpdateA2ADispatchIntent(ctx, intent, expectedVersion); err != nil {
		t.Fatal(err)
	}
}

func assertTestA2AIntentStatus(t *testing.T, ctx context.Context, service *Service, intentID string, expected domain.A2ADispatchStatus) {
	t.Helper()
	intent, err := service.Store().A2ADispatchIntent(ctx, intentID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != expected {
		t.Fatalf("intent 状态 = %s，期望 %s", intent.Status, expected)
	}
}

func waitForTestA2AIntentStatus(t *testing.T, ctx context.Context, service *Service, intentID string, expected domain.A2ADispatchStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		intent, err := service.Store().A2ADispatchIntent(ctx, intentID)
		if err != nil {
			t.Fatal(err)
		}
		if intent.Status == expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("intent 状态 = %s，期望 %s", intent.Status, expected)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertSingleTestA2ALog(t *testing.T, ctx context.Context, service *Service, taskID string) {
	t.Helper()
	logs, err := service.Store().TaskLogs(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Content != "single replayed line" {
		t.Fatalf("日志 = %+v，期望仅投影一次", logs)
	}
}
