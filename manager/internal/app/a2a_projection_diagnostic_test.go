package app

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestA2ADiagnosticPersistsAcrossAuthRequiredAndFailedTerminal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	task := seedRunningTask(t, ctx, service)

	round, diagnostic := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionDiagnostic, nil, map[string]any{
		"errorCode": string(a2aext.ErrorAuthRequired),
		"message":   "authentication is required",
		"retryable": true,
	})
	if err := applyTestA2ARawEvent(ctx, service, round, diagnostic, domain.TaskA2ARemoteStatusAuthRequired); err != nil {
		t.Fatalf("投影 AUTH_REQUIRED 诊断返回错误: %v", err)
	}

	waitingRound, err := service.Store().TaskA2ARound(ctx, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waitingRound.ErrorCode != string(a2aext.ErrorAuthRequired) || waitingRound.ErrorMessage != "authentication is required" || !waitingRound.Retryable {
		t.Fatalf("AUTH_REQUIRED round 诊断 = %+v", waitingRound)
	}
	if waitingRound.CompletedAt != nil || waitingRound.RemoteStatus != domain.TaskA2ARemoteStatusAuthRequired {
		t.Fatalf("AUTH_REQUIRED 不应结束 round: %+v", waitingRound)
	}
	waitingTask := loadTestA2ATask(t, ctx, service, task.ID)
	if waitingTask.Status != domain.TaskWaitingInput {
		t.Fatalf("AUTH_REQUIRED task 状态 = %s", waitingTask.Status)
	}
	logs, err := service.Store().TaskLogs(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := logs[len(logs)-1].Content; got != "AUTH_REQUIRED: authentication is required" {
		t.Fatalf("诊断日志 = %q", got)
	}

	now = now.Add(time.Second)
	failedRound, terminal := prepareTestA2AEvent(t, ctx, service, task.ID, a2aext.EventExecutionTerminal, nil, map[string]any{
		"status": string(a2aext.TerminalFailed),
	})
	if err := applyTestA2ARawEvent(ctx, service, failedRound, terminal, domain.TaskA2ARemoteStatusFailed); err != nil {
		t.Fatalf("投影 FAILED 终态返回错误: %v", err)
	}

	completedRound, err := service.Store().TaskA2ARound(ctx, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completedRound.CompletedAt == nil || completedRound.ErrorCode != string(a2aext.ErrorAuthRequired) || completedRound.ErrorMessage != "authentication is required" || !completedRound.Retryable {
		t.Fatalf("FAILED round 未沿用诊断: %+v", completedRound)
	}
	failedTask := loadTestA2ATask(t, ctx, service, task.ID)
	if failedTask.Status != domain.TaskFailed || failedTask.Result != "AUTH_REQUIRED: authentication is required" {
		t.Fatalf("FAILED task 未使用诊断: %+v", failedTask)
	}
}
