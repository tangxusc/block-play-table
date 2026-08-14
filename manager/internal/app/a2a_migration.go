package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/store"
)

const legacyA2AMigrationMessage = "retry is required after Manager migrated task control to A2A"

// MigrateLegacyA2ATasks 结束升级前无法通过 A2A 恢复的活跃任务。
// 参数：ctx 用于取消任务、round 和 Worker 查询及原子提交。
// 返回：实际迁移为失败状态的任务数量。
// 错误：存储查询、领域状态转换或原子提交失败时返回错误。
func (s *Service) MigrateLegacyA2ATasks(ctx context.Context) (int, error) {
	tasks, err := s.store.Tasks(ctx)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, task := range tasks {
		if task == nil || !isLegacyA2AActiveTask(task.Status) {
			continue
		}
		rounds, err := s.store.TaskA2ARounds(ctx, task.ID)
		if err != nil {
			return migrated, err
		}
		if len(rounds) > 0 {
			continue
		}
		expectedTaskVersion := task.Version
		reason := string(a2aext.ErrorA2AMigrationRetryRequired) + ": " + legacyA2AMigrationMessage
		if err := task.Fail(reason, s.clock()); err != nil {
			return migrated, err
		}
		var worker *domain.Worker
		expectedWorkerVersion := 0
		if task.WorkerID != "" {
			worker, err = s.store.Worker(ctx, task.WorkerID)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return migrated, err
			}
			if err == nil {
				expectedWorkerVersion = worker.Version
				worker.ReleaseTask(task.ID, task.UpdatedAt)
			} else {
				worker = nil
			}
		}
		events := task.PullEvents()
		if worker != nil {
			events = append(events, worker.PullEvents()...)
		}
		applied, err := s.store.CommitA2AMigrationFailure(ctx, store.A2AMigrationCommit{
			Task: task, ExpectedTaskVersion: expectedTaskVersion, Worker: worker, ExpectedWorkerVersion: expectedWorkerVersion,
			Events: events, CancelPendingInteractions: true,
		})
		if err != nil {
			return migrated, fmt.Errorf("migrate legacy task %s: %w", task.ID, err)
		}
		if applied {
			migrated++
			s.publishEvents(events)
		}
	}
	return migrated, nil
}

func isLegacyA2AActiveTask(status domain.TaskStatus) bool {
	switch status {
	case domain.TaskStarting, domain.TaskRunning, domain.TaskWaitingInput, domain.TaskInterrupting:
		return true
	default:
		return false
	}
}
