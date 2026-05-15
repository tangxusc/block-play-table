package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

type Reconciler struct {
	service  *Service
	timeout  time.Duration
	interval time.Duration
	logger   *slog.Logger
}

type ReconcilerOption func(*Reconciler)

func WithReconcilerLogger(logger *slog.Logger) ReconcilerOption {
	return func(r *Reconciler) {
		if logger != nil {
			r.logger = logger
		}
	}
}

func NewReconciler(service *Service, timeout, interval time.Duration, options ...ReconcilerOption) *Reconciler {
	r := &Reconciler{
		service:  service,
		timeout:  timeout,
		interval: interval,
		logger:   slog.Default(),
	}
	if r.interval <= 0 {
		r.interval = timeout / 3
	}
	if r.interval <= 0 {
		r.interval = time.Second
	}
	for _, option := range options {
		option(r)
	}
	return r
}

func (r *Reconciler) Run(ctx context.Context) {
	if r.timeout <= 0 {
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.ReconcileWorkerLiveness(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Warn("reconcile worker liveness failed", "error", err)
			}
		}
	}
}

func (r *Reconciler) ReconcileWorkerLiveness(ctx context.Context) error {
	if r.timeout <= 0 {
		return nil
	}
	workers, err := r.service.store.Workers(ctx)
	if err != nil {
		return err
	}
	cutoff := r.service.clock().Add(-r.timeout)
	for _, worker := range workers {
		online := worker.Status == domain.WorkerOnline
		hasTasks := len(worker.CurrentTaskIDs) > 0
		stale := worker.LastHeartbeatAt == nil || !worker.LastHeartbeatAt.After(cutoff)
		if online && !stale {
			continue
		}
		if !online && !hasTasks {
			continue
		}
		if online && !hasTasks && stale {
			if _, err := r.service.WorkerDisconnected(ctx, worker.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			continue
		}
		if _, err := r.service.MarkWorkerLost(ctx, worker.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return nil
}
