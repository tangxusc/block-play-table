package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

// TestIsA2ASDKProtocolErrorClassifiesPermanentAndTemporaryErrors 验证 SDK 错误按永久协议错误和暂时服务端错误分类。
func TestIsA2ASDKProtocolErrorClassifiesPermanentAndTemporaryErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "parse", err: a2a.ErrParseError, want: true},
		{name: "invalid request", err: a2a.ErrInvalidRequest, want: true},
		{name: "method not found", err: a2a.ErrMethodNotFound, want: true},
		{name: "invalid params", err: a2a.ErrInvalidParams, want: true},
		{name: "task not cancelable", err: a2a.ErrTaskNotCancelable, want: true},
		{name: "unsupported operation", err: a2a.ErrUnsupportedOperation, want: true},
		{name: "unsupported content", err: a2a.ErrUnsupportedContentType, want: true},
		{name: "invalid response", err: a2a.ErrInvalidAgentResponse, want: true},
		{name: "extended card", err: a2a.ErrExtendedCardNotConfigured, want: true},
		{name: "extension required", err: a2a.ErrExtensionSupportRequired, want: true},
		{name: "version", err: a2a.ErrVersionNotSupported, want: true},
		{name: "unauthenticated", err: a2a.ErrUnauthenticated, want: true},
		{name: "unauthorized", err: a2a.ErrUnauthorized, want: true},
		{name: "internal", err: a2a.ErrInternalError, want: false},
		{name: "server", err: a2a.ErrServerError, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("远端 SDK 错误: %w", test.err)
			if got := isA2ASDKProtocolError(wrapped); got != test.want {
				t.Fatalf("isA2ASDKProtocolError(%v) = %v，期望 %v", test.err, got, test.want)
			}
		})
	}
}

// TestA2ATemporaryServerErrorsPreserveAcknowledgedRound 验证已确认 round 的暂时服务端错误进入 60 秒不可达恢复窗口。
func TestA2ATemporaryServerErrorsPreserveAcknowledgedRound(t *testing.T) {
	for _, sdkErr := range []error{a2a.ErrInternalError, a2a.ErrServerError} {
		t.Run(sdkErr.Error(), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
			service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
			task := seedRunningTask(t, ctx, service)
			round, err := service.Store().LatestTaskA2ARound(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			reconciler := newSilentA2AErrorTestReconciler(service)

			reconciler.handleA2AStreamFailure(ctx, round.ID, "", true, fmt.Errorf("worker failed: %w", sdkErr))

			persistedRound, err := service.Store().TaskA2ARound(ctx, round.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persistedRound.CompletedAt != nil || persistedRound.UnreachableSince == nil || persistedRound.ErrorCode != "" {
				t.Fatalf("暂时错误错误地终结已确认 round: %+v", persistedRound)
			}
			persistedTask, err := service.Store().Task(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persistedTask.Status != domain.TaskRunning {
				t.Fatalf("暂时错误后的 Task 状态 = %s，期望 RUNNING", persistedTask.Status)
			}
		})
	}
}

// TestA2ATemporaryServerErrorsRetryUnconfirmedDispatch 验证未确认 intent 的暂时服务端错误按现有退避策略重试。
func TestA2ATemporaryServerErrorsRetryUnconfirmedDispatch(t *testing.T) {
	for _, sdkErr := range []error{a2a.ErrInternalError, a2a.ErrServerError} {
		t.Run(sdkErr.Error(), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 8, 10, 20, 30, 0, 0, time.UTC)
			service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
			task, round, intent := seedUnconfirmedA2AErrorTestDispatch(t, ctx, service, now)
			reconciler := newSilentA2AErrorTestReconciler(service)

			reconciler.handleA2AStreamFailure(ctx, round.ID, intent.ID, false, fmt.Errorf("worker failed: %w", sdkErr))

			persistedRound, err := service.Store().TaskA2ARound(ctx, round.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persistedRound.CompletedAt != nil || persistedRound.UnreachableSince == nil || persistedRound.ErrorCode != "" {
				t.Fatalf("暂时错误错误地终结未确认 round: %+v", persistedRound)
			}
			persistedIntent, err := service.Store().A2ADispatchIntent(ctx, intent.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persistedIntent.Status != domain.A2ADispatchPending || persistedIntent.ErrorCode != "WORKER_UNREACHABLE" || !persistedIntent.AvailableAt.Equal(now.Add(time.Second)) {
				t.Fatalf("暂时错误后的 intent = %+v", persistedIntent)
			}
			persistedTask, err := service.Store().Task(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persistedTask.Status != domain.TaskStarting {
				t.Fatalf("暂时错误后的 Task 状态 = %s，期望 STARTING", persistedTask.Status)
			}
		})
	}
}

func newSilentA2AErrorTestReconciler(service *Service) *Reconciler {
	return NewReconciler(service, 0, time.Second,
		WithReconcilerLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

func seedUnconfirmedA2AErrorTestDispatch(t *testing.T, ctx context.Context, service *Service, now time.Time) (*domain.Task, *domain.TaskA2ARound, *domain.TaskA2ADispatchIntent) {
	t.Helper()
	project, err := service.CreateProject(ctx, CreateProjectInput{Name: "SDK error", GitURL: "file:///tmp/sdk-error"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := service.RegisterWorker(ctx, RegisterWorkerInput{
		ID: "worker-sdk-error", Name: "SDK error worker", SupportedAgents: []domain.AgentType{domain.AgentCodex},
		WorkDir: "/tmp/sdk-error-worker", BindingMode: domain.WorkerAllProjects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkerConnected(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{Title: "SDK error task", ProjectID: project.ID, AgentType: domain.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignWorker(ctx, task.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	round, err := service.Store().LatestTaskA2ARound(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	intents, err := service.Store().A2ADispatchIntentsDue(ctx, now, 10)
	if err != nil || len(intents) != 1 {
		t.Fatalf("待发送 intent = %+v, %v", intents, err)
	}
	intent := intents[0]
	expectedVersion := intent.Version
	if err := intent.StartAttempt(now); err != nil {
		t.Fatal(err)
	}
	if err := service.Store().UpdateA2ADispatchIntent(ctx, &intent, expectedVersion); err != nil {
		t.Fatal(err)
	}
	return task, round, &intent
}
