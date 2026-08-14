package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestMemoryStoreContextAwareOperationsHonorCancellation(t *testing.T) {
	storage := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Now().UTC()
	round := &domain.TaskA2ARound{ID: "round"}
	intent := &domain.TaskA2ADispatchIntent{ID: "intent"}

	operations := map[string]func() error{
		"append log":          func() error { return storage.AppendTaskLog(ctx, domain.TaskLog{}) },
		"append conversation": func() error { return storage.AppendConversation(ctx, domain.ConversationMessage{}) },
		"save interaction":    func() error { return storage.SaveTaskInteraction(ctx, domain.TaskInteraction{}) },
		"commit command":      func() error { return storage.CommitA2ACommand(ctx, A2ACommandCommit{}) },
		"commit projection": func() error {
			_, err := storage.CommitA2AProjection(ctx, A2AProjectionCommit{})
			return err
		},
		"commit migration": func() error {
			_, err := storage.CommitA2AMigrationFailure(ctx, A2AMigrationCommit{})
			return err
		},
		"event inbox":      func() error { _, err := storage.A2AEventInbox(ctx, "round", "event"); return err },
		"rounds":           func() error { _, err := storage.TaskA2ARounds(ctx, "task"); return err },
		"round":            func() error { _, err := storage.TaskA2ARound(ctx, "round"); return err },
		"latest round":     func() error { _, err := storage.LatestTaskA2ARound(ctx, "task"); return err },
		"reconcile rounds": func() error { _, err := storage.A2ARoundsForReconcile(ctx); return err },
		"due intents":      func() error { _, err := storage.A2ADispatchIntentsDue(ctx, now, 10); return err },
		"intent":           func() error { _, err := storage.A2ADispatchIntent(ctx, "intent"); return err },
		"update round":     func() error { return storage.UpdateA2ARound(ctx, round, 1) },
		"update intent":    func() error { return storage.UpdateA2ADispatchIntent(ctx, intent, 1) },
		"commit dispatch":  func() error { return storage.CommitA2ADispatchResult(ctx, round, 1, intent, 1) },
	}

	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, context.Canceled) {
				t.Fatalf("取消 context 后错误=%v，期望 context.Canceled", err)
			}
		})
	}
}
