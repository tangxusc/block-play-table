package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

const (
	a2aDispatchBatchSize         = 32
	a2aUnreachableGrace          = 60 * time.Second
	a2aUnconfirmedGrace          = 60 * time.Second
	a2aUnknownGrace              = 60 * time.Second
	a2aPollInterval              = 500 * time.Millisecond
	a2aProtocolFailureMessage    = "Worker returned an incompatible A2A response."
	a2aTaskNotFoundMessage       = "Worker A2A task was not found."
	a2aUnknownTimeoutMessage     = "Worker A2A status remained UNKNOWN or UNSPECIFIED for 60 seconds."
	a2aUnreachableMessage        = "Worker A2A endpoint is temporarily unreachable."
	a2aUnreachableTimeoutMessage = "Worker A2A endpoint remained unreachable for 60 seconds."
)

var (
	// ErrA2AUnknownTimeout 表示远端状态 UNKNOWN/UNSPECIFIED 已超过确定性收敛窗口。
	ErrA2AUnknownTimeout = errors.New("a2a unknown status timeout")
	// ErrA2AUnconfirmedTimeout 表示持久化 intent 在收敛窗口内没有获得首条远端更新。
	ErrA2AUnconfirmedTimeout = errors.New("a2a dispatch confirmation timeout")
	// ErrA2AUnreachableTimeout 表示远端网络不可达已超过确定性收敛窗口。
	ErrA2AUnreachableTimeout = errors.New("a2a unreachable timeout")
)

// ReconcileA2A 领取到期 intent，并恢复所有非终态远端 round 的对账流。
// 参数：ctx 绑定 Manager 进程生命周期。
// 返回：查询或领取失败时返回错误；网络流错误由后台任务记录并持久化。
// 错误：Store 查询、OCC 更新或 context 取消时返回错误。
func (r *Reconciler) ReconcileA2A(ctx context.Context) error {
	if r.a2a == nil {
		return nil
	}
	now := r.service.clock()
	intents, err := r.service.store.A2ADispatchIntentsDue(ctx, now, a2aDispatchBatchSize)
	if err != nil {
		return err
	}
	for index := range intents {
		intent := intents[index]
		dispatchKey := "dispatch:task:" + intent.TaskID
		if !r.activateA2A(dispatchKey) {
			continue
		}
		streamKey := ""
		if intent.Operation.CreatesRound() || intent.Operation == domain.TaskA2AOperationInteractionResponse {
			streamKey = "stream:task:" + intent.TaskID
			if !r.activateA2A(streamKey) {
				r.deactivateA2A(dispatchKey)
				continue
			}
		}
		expectedVersion := intent.Version
		if err := intent.StartAttempt(now); err != nil {
			r.deactivateA2A(dispatchKey)
			if streamKey != "" {
				r.deactivateA2A(streamKey)
			}
			continue
		}
		if err := r.service.store.UpdateA2ADispatchIntent(ctx, &intent, expectedVersion); err != nil {
			r.deactivateA2A(dispatchKey)
			if streamKey != "" {
				r.deactivateA2A(streamKey)
			}
			if errors.Is(err, store.ErrA2AConcurrentModification) {
				continue
			}
			return err
		}
		go r.runA2ADispatch(ctx, dispatchKey, streamKey, intent)
	}
	rounds, err := r.service.store.A2ARoundsForReconcile(ctx)
	if err != nil {
		return err
	}
	for index := range rounds {
		round := rounds[index]
		key := "stream:task:" + round.TaskID
		if !r.activateA2A(key) {
			continue
		}
		go r.runA2AReconcile(ctx, key, round)
	}
	return nil
}

func (r *Reconciler) runA2ADispatch(ctx context.Context, dispatchKey, streamKey string, intent domain.TaskA2ADispatchIntent) {
	var releaseOnce sync.Once
	releaseDispatch := func() { releaseOnce.Do(func() { r.deactivateA2A(dispatchKey) }) }
	defer releaseDispatch()
	if streamKey != "" {
		defer r.deactivateA2A(streamKey)
	}
	request, err := r.service.PrepareA2ADispatchRequest(ctx, intent)
	if err != nil {
		r.handleA2AStreamFailure(context.WithoutCancel(ctx), intent.RoundID, intent.ID, false, err)
		return
	}
	streamCtx, cancel := context.WithCancelCause(ctx)
	watchDone := r.watchA2ADeadlines(streamCtx, request.Round.ID, intent.ID, cancel)
	defer func() {
		cancel(nil)
		<-watchDone
	}()
	acknowledged := false
	handler := func(updateCtx context.Context, update A2ARemoteUpdate) error {
		return r.withA2AProjection(intent.TaskID, func() error {
			if !acknowledged {
				if err := r.service.ApplyA2ADispatchUpdate(updateCtx, intent.ID, update); err != nil {
					return err
				}
				acknowledged = true
				// 远端身份绑定后允许取消等控制命令领取，但当前流仍由 streamKey 防止重复订阅。
				releaseDispatch()
				return nil
			}
			return r.service.ApplyA2ARemoteUpdate(updateCtx, request.Round.ID, update)
		})
	}
	err = r.a2a.Dispatch(streamCtx, request, handler)
	if cause := context.Cause(streamCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		err = cause
	}
	if ctx.Err() != nil {
		return
	}
	if err == nil || errors.Is(err, context.Canceled) && context.Cause(streamCtx) == context.Canceled {
		return
	}
	r.handleA2AStreamFailure(context.WithoutCancel(ctx), request.Round.ID, intent.ID, acknowledged, err)
}

func (r *Reconciler) runA2AReconcile(ctx context.Context, key string, round domain.TaskA2ARound) {
	defer r.deactivateA2A(key)
	streamCtx, cancel := context.WithCancelCause(ctx)
	watchDone := r.watchA2ADeadlines(streamCtx, round.ID, "", cancel)
	defer func() {
		cancel(nil)
		<-watchDone
	}()
	err := r.a2a.Reconcile(streamCtx, round, func(updateCtx context.Context, update A2ARemoteUpdate) error {
		return r.withA2AProjection(round.TaskID, func() error {
			return r.service.ApplyA2ARemoteUpdate(updateCtx, round.ID, update)
		})
	})
	if cause := context.Cause(streamCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		err = cause
	}
	if ctx.Err() != nil {
		return
	}
	if err == nil || errors.Is(err, context.Canceled) && context.Cause(streamCtx) == context.Canceled {
		return
	}
	r.handleA2AStreamFailure(context.WithoutCancel(ctx), round.ID, "", true, err)
}

func (r *Reconciler) handleA2AStreamFailure(ctx context.Context, roundID, intentID string, acknowledged bool, streamErr error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	failureIntentID := intentID
	if acknowledged {
		failureIntentID = ""
	}
	switch {
	case errors.Is(streamErr, a2a.ErrTaskNotFound):
		if err := r.service.failA2ARoundWithIntent(deadlineCtx, roundID, failureIntentID, "A2A_TASK_NOT_FOUND", a2aTaskNotFoundMessage); err != nil {
			r.logger.Warn("fail missing a2a task failed", "roundId", roundID, "error", err)
		}
	case errors.Is(streamErr, ErrA2AUnknownTimeout):
		if err := r.service.failA2ARoundWithIntent(deadlineCtx, roundID, failureIntentID, "A2A_PROTOCOL_CONFLICT", a2aUnknownTimeoutMessage); err != nil {
			r.logger.Warn("fail unknown a2a status failed", "roundId", roundID, "error", err)
		}
	case errors.Is(streamErr, ErrA2AUnconfirmedTimeout):
		if err := r.service.failA2ARoundWithIntent(deadlineCtx, roundID, failureIntentID, "WORKER_UNREACHABLE_TIMEOUT", a2aUnreachableTimeoutMessage); err != nil {
			r.logger.Warn("fail unconfirmed a2a dispatch failed", "roundId", roundID, "error", err)
		}
	case errors.Is(streamErr, ErrA2AProtocolConflict), errors.Is(streamErr, ErrA2ASequenceGap), errors.Is(streamErr, domain.ErrConflict), isA2ASDKProtocolError(streamErr):
		r.logger.Warn("a2a protocol conflict", "roundId", roundID, "error", streamErr)
		if err := r.service.failA2ARoundWithIntent(deadlineCtx, roundID, failureIntentID, "A2A_PROTOCOL_CONFLICT", a2aProtocolFailureMessage); err != nil {
			r.logger.Warn("fail conflicting a2a round failed", "roundId", roundID, "error", err)
		}
	case errors.Is(streamErr, ErrA2AProjection), errors.Is(streamErr, store.ErrA2AConcurrentModification):
		// 投影失败保留远端 round，下一次 GetTask/幂等命令重放会继续恢复。
		r.logger.Warn("a2a projection will be retried", "roundId", roundID, "error", streamErr)
	default:
		r.handleA2ANetworkFailure(ctx, roundID, intentID, acknowledged, streamErr)
	}
}

func (r *Reconciler) handleA2ANetworkFailure(ctx context.Context, roundID, intentID string, acknowledged bool, networkErr error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	round, err := r.service.store.TaskA2ARound(deadlineCtx, roundID)
	if err != nil {
		return
	}
	expectedRoundVersion := round.Version
	now := r.service.clock()
	round.MarkUnreachable(now)
	if round.UnreachableSince != nil && now.Sub(*round.UnreachableSince) >= a2aUnreachableGrace {
		if err := r.service.failA2ARoundWithIntent(deadlineCtx, round.ID, intentID, "WORKER_UNREACHABLE_TIMEOUT", a2aUnreachableTimeoutMessage); err != nil {
			r.logger.Warn("fail unreachable a2a round failed", "roundId", roundID, "error", err)
		}
		return
	}
	r.logger.Warn("a2a worker request failed", "roundId", roundID, "error", networkErr)
	if !acknowledged && intentID != "" {
		intent, loadErr := r.service.store.A2ADispatchIntent(deadlineCtx, intentID)
		if loadErr != nil {
			return
		}
		if !now.Before(intent.CreatedAt.Add(a2aUnconfirmedGrace)) {
			if err := r.service.failA2ARoundWithIntent(deadlineCtx, round.ID, intent.ID, "WORKER_UNREACHABLE_TIMEOUT", a2aUnreachableTimeoutMessage); err != nil {
				r.logger.Warn("fail unconfirmed a2a dispatch failed", "roundId", roundID, "error", err)
			}
			return
		}
		expectedIntentVersion := intent.Version
		delay := time.Second << min(intent.AttemptCount-1, 5)
		availableAt := now.Add(delay)
		unconfirmedDeadline := intent.CreatedAt.Add(a2aUnconfirmedGrace)
		if availableAt.After(unconfirmedDeadline) {
			availableAt = unconfirmedDeadline
		}
		if round.UnreachableSince != nil {
			deadline := round.UnreachableSince.Add(a2aUnreachableGrace)
			if availableAt.After(deadline) {
				availableAt = deadline
			}
		}
		if retryErr := intent.ScheduleRetry("WORKER_UNREACHABLE", a2aUnreachableMessage, availableAt, now); retryErr != nil {
			return
		}
		if commitErr := r.service.store.CommitA2ADispatchResult(deadlineCtx, round, expectedRoundVersion, intent, expectedIntentVersion); commitErr != nil && !errors.Is(commitErr, store.ErrA2AConcurrentModification) {
			r.logger.Warn("persist a2a dispatch retry failed", "roundId", roundID, "error", commitErr)
		}
		return
	}
	if err := r.service.store.UpdateA2ARound(deadlineCtx, round, expectedRoundVersion); err != nil && !errors.Is(err, store.ErrA2AConcurrentModification) {
		r.logger.Warn("persist a2a unreachable state failed", "roundId", roundID, "error", err)
		return
	}
}

func (r *Reconciler) watchA2ADeadlines(ctx context.Context, roundID, intentID string, cancel context.CancelCauseFunc) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(a2aPollInterval)
		defer ticker.Stop()
		for {
			now := r.service.clock()
			if intentID != "" {
				intent, err := r.service.store.A2ADispatchIntent(ctx, intentID)
				if err == nil {
					if intent.Status != domain.A2ADispatchCompleted && intent.Status != domain.A2ADispatchFailed && !now.Before(intent.CreatedAt.Add(a2aUnconfirmedGrace)) {
						cancel(ErrA2AUnconfirmedTimeout)
						return
					}
				} else if !errors.Is(err, context.Canceled) {
					r.logger.Warn("read a2a intent deadline state failed", "intentId", intentID, "error", err)
				}
			}
			round, err := r.service.store.TaskA2ARound(ctx, roundID)
			if err == nil {
				if round.CompletedAt != nil {
					return
				}
				if round.UnknownSince != nil && !now.Before(round.UnknownSince.Add(a2aUnknownGrace)) {
					cancel(ErrA2AUnknownTimeout)
					return
				}
				if round.UnreachableSince != nil && !now.Before(round.UnreachableSince.Add(a2aUnreachableGrace)) {
					cancel(ErrA2AUnreachableTimeout)
					return
				}
			} else if !errors.Is(err, context.Canceled) {
				r.logger.Warn("read a2a deadline state failed", "roundId", roundID, "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func isA2ASDKProtocolError(err error) bool {
	for _, sentinel := range []error{
		a2a.ErrParseError, a2a.ErrInvalidRequest, a2a.ErrMethodNotFound, a2a.ErrInvalidParams,
		a2a.ErrTaskNotCancelable,
		a2a.ErrUnsupportedOperation, a2a.ErrUnsupportedContentType, a2a.ErrInvalidAgentResponse,
		a2a.ErrExtendedCardNotConfigured, a2a.ErrExtensionSupportRequired, a2a.ErrVersionNotSupported,
		a2a.ErrUnauthenticated, a2a.ErrUnauthorized,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

func (r *Reconciler) activateA2A(key string) bool {
	r.a2aMu.Lock()
	defer r.a2aMu.Unlock()
	if _, exists := r.a2aActive[key]; exists {
		return false
	}
	r.a2aActive[key] = struct{}{}
	return true
}

func (r *Reconciler) deactivateA2A(key string) {
	r.a2aMu.Lock()
	delete(r.a2aActive, key)
	r.a2aMu.Unlock()
}

func (r *Reconciler) withA2AProjection(taskID string, apply func() error) error {
	r.a2aProjectionMu.Lock()
	lock := r.a2aProjectionLocks[taskID]
	if lock == nil {
		lock = &a2aTaskProjectionLock{}
		r.a2aProjectionLocks[taskID] = lock
	}
	lock.refs++
	r.a2aProjectionMu.Unlock()

	lock.mu.Lock()
	err := apply()
	lock.mu.Unlock()

	r.a2aProjectionMu.Lock()
	lock.refs--
	if lock.refs == 0 {
		delete(r.a2aProjectionLocks, taskID)
	}
	r.a2aProjectionMu.Unlock()
	return err
}
