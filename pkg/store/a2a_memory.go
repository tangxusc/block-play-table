package store

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

// CommitA2ACommand 原子保存用户命令产生的业务聚合、A2A round、下发意图和领域事件。
// 参数：ctx 用于取消调用，commit 包含完整原子变更集合。
// 返回：成功时返回 nil。
// 错误：身份重复、关联不一致或 OCC 版本冲突时返回错误，且不写入任何变更。
func (s *MemoryStore) CommitA2ACommand(ctx context.Context, commit A2ACommandCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateA2ACommitLocked(commit.Task, commit.ExpectedTaskVersion, commit.Worker, commit.ExpectedWorkerVersion, commit.Round, commit.Intent, commit.CreateRound); err != nil {
		return err
	}
	if err := s.validateTaskInteractionOwnerLocked(commit.Interaction); err != nil {
		return err
	}
	s.applyA2ACommonLocked(commit.Task, commit.Worker, commit.Interaction, commit.Conversations, nil, commit.Events, commit.CancelPendingInteractions)
	if commit.CreateRound && commit.Round != nil {
		s.a2aRounds[commit.Round.ID] = cloneA2ARound(*commit.Round)
	}
	if commit.Intent != nil {
		s.a2aIntents[commit.Intent.ID] = cloneA2AIntent(*commit.Intent)
	}
	return nil
}

// CommitA2AProjection 原子登记 event inbox 并保存 Task、round、日志、会话、交互和领域事件投影。
// 参数：ctx 用于取消调用，commit 包含事件及全部业务投影。
// 返回：首次事件返回 APPLIED，内容相同的重放返回 DUPLICATE。
// 错误：同 event ID 内容不同、sequence 冲突或 OCC 版本冲突时返回错误且不写入投影。
func (s *MemoryStore) CommitA2AProjection(ctx context.Context, commit A2AProjectionCommit) (A2AEventDisposition, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ""
	if commit.Inbox != nil {
		key = a2aInboxKey(commit.Inbox.RoundID, commit.Inbox.EventID)
		if existing, ok := s.a2aEventInbox[key]; ok {
			if existing.PayloadHash != commit.Inbox.PayloadHash {
				return "", fmt.Errorf("%w: event %s payload hash changed", domain.ErrConflict, commit.Inbox.EventID)
			}
			if commit.Intent != nil {
				// 首包并发竞态必须重载最新快照，不能把未完成 intent 当成已原子确认。
				return "", fmt.Errorf("%w: dispatch event was projected concurrently", ErrA2AConcurrentModification)
			}
			return A2AEventDuplicate, nil
		}
		if commit.Round == nil || commit.Inbox.ExecutionID == "" || commit.Inbox.ExecutionID != commit.Round.ExecutionID {
			return "", fmt.Errorf("%w: inbox execution does not match round", domain.ErrConflict)
		}
		for _, existing := range s.a2aEventInbox {
			if existing.ExecutionID == commit.Inbox.ExecutionID && existing.Sequence == commit.Inbox.Sequence && (existing.RoundID != commit.Inbox.RoundID || existing.EventID != commit.Inbox.EventID) {
				return "", fmt.Errorf("%w: sequence %d already belongs to event %s", domain.ErrConflict, commit.Inbox.Sequence, existing.EventID)
			}
		}
	}
	if err := s.validateA2AOCCLocked(commit.Task, commit.ExpectedTaskVersion, commit.Worker, commit.ExpectedWorkerVersion, commit.Round, commit.ExpectedRoundVersion, commit.Intent, commit.ExpectedIntentVersion); err != nil {
		return "", err
	}
	if err := s.validateTaskInteractionOwnerLocked(commit.Interaction); err != nil {
		return "", err
	}
	if err := s.validateProjectionRecordIDsLocked(commit.Logs, commit.Conversations); err != nil {
		return "", err
	}
	s.applyA2ACommonLocked(commit.Task, commit.Worker, commit.Interaction, commit.Conversations, commit.Logs, commit.Events, commit.CancelPendingInteractions)
	if commit.Round != nil {
		s.a2aRounds[commit.Round.ID] = cloneA2ARound(*commit.Round)
	}
	if commit.Intent != nil {
		s.a2aIntents[commit.Intent.ID] = cloneA2AIntent(*commit.Intent)
	}
	if commit.Inbox != nil {
		s.a2aEventInbox[key] = *commit.Inbox
	}
	return A2AEventApplied, nil
}

// CommitA2AMigrationFailure 原子结束没有 A2A round 的旧活跃任务并释放 Worker。
// 参数：ctx 用于取消调用，commit 包含失败后的 Task、可选 Worker 和领域事件。
// 返回：实际迁移返回 true；任务已有 round 时返回 false。
// 错误：关联非法或聚合版本冲突时返回错误，且不写入任何变更。
func (s *MemoryStore) CommitA2AMigrationFailure(ctx context.Context, commit A2AMigrationCommit) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if commit.Task == nil {
		return false, fmt.Errorf("migration task is required")
	}
	if commit.Worker != nil && commit.Task.WorkerID != commit.Worker.ID {
		return false, fmt.Errorf("%w: migration worker does not match task", domain.ErrConflict)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, round := range s.a2aRounds {
		if round.TaskID == commit.Task.ID {
			return false, nil
		}
	}
	if err := s.validateA2AOCCLocked(commit.Task, commit.ExpectedTaskVersion, commit.Worker, commit.ExpectedWorkerVersion, nil, 0, nil, 0); err != nil {
		return false, err
	}
	s.applyA2ACommonLocked(commit.Task, commit.Worker, nil, nil, nil, commit.Events, commit.CancelPendingInteractions)
	return true, nil
}

// A2AEventInbox 查询一个 round 内已投影事件的幂等证明。
// 参数：ctx 用于取消查询，roundID/eventID 是事件复合身份。
// 返回：找到时返回 inbox 副本。
// 错误：事件不存在或 context 已取消时返回错误。
func (s *MemoryStore) A2AEventInbox(ctx context.Context, roundID, eventID string) (*domain.A2AEventInbox, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	inbox, ok := s.a2aEventInbox[a2aInboxKey(roundID, eventID)]
	if !ok {
		return nil, fmt.Errorf("%w: a2a event %s", domain.ErrNotFound, eventID)
	}
	copy := inbox
	return &copy, nil
}

// TaskA2ARounds 返回一个 Manager Task 的全部 A2A 操作轮次。
// 参数：ctx 用于取消调用，taskID 是 Manager Task ID。
// 返回：按 attempt、turn、创建时间稳定排序的 round 副本。
// 错误：context 已取消时返回对应错误。
func (s *MemoryStore) TaskA2ARounds(ctx context.Context, taskID string) ([]domain.TaskA2ARound, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.TaskA2ARound, 0)
	for _, round := range s.a2aRounds {
		if round.TaskID == taskID {
			out = append(out, cloneA2ARound(round))
		}
	}
	sortA2ARounds(out)
	return out, nil
}

// TaskA2ARound 按 round ID 查询 A2A 操作轮次。
// 参数：ctx 用于取消调用，id 是 round ID。
// 返回：找到时返回 round 副本。
// 错误：不存在时返回 domain.ErrNotFound，context 取消时返回对应错误。
func (s *MemoryStore) TaskA2ARound(ctx context.Context, id string) (*domain.TaskA2ARound, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	round, ok := s.a2aRounds[id]
	if !ok {
		return nil, fmt.Errorf("%w: a2a round %s", domain.ErrNotFound, id)
	}
	copy := cloneA2ARound(round)
	return &copy, nil
}

// LatestTaskA2ARound 返回任务最新创建的 A2A round。
// 参数：ctx 用于取消调用，taskID 是 Manager Task ID。
// 返回：找到时返回最新 round 副本。
// 错误：不存在时返回 domain.ErrNotFound。
func (s *MemoryStore) LatestTaskA2ARound(ctx context.Context, taskID string) (*domain.TaskA2ARound, error) {
	rounds, err := s.TaskA2ARounds(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if len(rounds) == 0 {
		return nil, fmt.Errorf("%w: a2a round for task %s", domain.ErrNotFound, taskID)
	}
	copy := rounds[len(rounds)-1]
	return &copy, nil
}

// A2ARoundsForReconcile 返回需要恢复订阅、查询或超时收敛的非终态执行 round。
// 参数：ctx 用于取消调用。
// 返回：按更新时间排序的 round 副本。
// 错误：context 已取消时返回对应错误。
func (s *MemoryStore) A2ARoundsForReconcile(ctx context.Context) ([]domain.TaskA2ARound, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.TaskA2ARound, 0)
	for _, round := range s.a2aRounds {
		if round.CompletedAt == nil && round.A2ATaskID != "" && round.ContextID != "" {
			out = append(out, cloneA2ARound(round))
		}
	}
	slices.SortFunc(out, func(a, b domain.TaskA2ARound) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	return out, nil
}

// A2ADispatchIntentsDue 返回当前可领取或发送超时可恢复的下发意图。
// 参数：ctx 用于取消调用，now 是调度时间，limit 是最大条数；limit 小于 1 时不限制。
// 返回：按 availableAt、创建时间和 ID 稳定排序的意图副本。
// 错误：context 已取消时返回对应错误。
func (s *MemoryStore) A2ADispatchIntentsDue(ctx context.Context, now time.Time, limit int) ([]domain.TaskA2ADispatchIntent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.TaskA2ADispatchIntent, 0)
	for _, intent := range s.a2aIntents {
		due := intent.Status == domain.A2ADispatchPending && !intent.AvailableAt.After(now)
		staleSending := intent.Status == domain.A2ADispatchSending && intent.LastAttemptAt != nil && !intent.LastAttemptAt.After(now.Add(-15*time.Second))
		if due || staleSending {
			out = append(out, cloneA2AIntent(intent))
		}
	}
	slices.SortFunc(out, func(a, b domain.TaskA2ADispatchIntent) int {
		if cmp := a.AvailableAt.Compare(b.AvailableAt); cmp != 0 {
			return cmp
		}
		if cmp := a.CreatedAt.Compare(b.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// A2ADispatchIntent 按 ID 查询持久化下发意图。
// 参数：ctx 用于取消调用，id 是意图 ID。
// 返回：找到时返回意图副本。
// 错误：不存在时返回 domain.ErrNotFound。
func (s *MemoryStore) A2ADispatchIntent(ctx context.Context, id string) (*domain.TaskA2ADispatchIntent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.a2aIntents[id]
	if !ok {
		return nil, fmt.Errorf("%w: a2a dispatch intent %s", domain.ErrNotFound, id)
	}
	copy := cloneA2AIntent(intent)
	return &copy, nil
}

// UpdateA2ARound 使用 expectedVersion 对 round 执行 OCC 更新。
// 参数：ctx 用于取消调用，round 是新快照，expectedVersion 是存储中的预期版本。
// 返回：成功时返回 nil。
// 错误：记录不存在或版本不匹配时返回错误。
func (s *MemoryStore) UpdateA2ARound(ctx context.Context, round *domain.TaskA2ARound, expectedVersion int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if round == nil {
		return fmt.Errorf("a2a round is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.a2aRounds[round.ID]
	if !ok {
		return fmt.Errorf("%w: a2a round %s", domain.ErrNotFound, round.ID)
	}
	if existing.Version != expectedVersion {
		return fmt.Errorf("%w: round %s expected version %d, got %d", ErrA2AConcurrentModification, round.ID, expectedVersion, existing.Version)
	}
	s.a2aRounds[round.ID] = cloneA2ARound(*round)
	return nil
}

// UpdateA2ADispatchIntent 使用 expectedVersion 对下发意图执行 OCC 更新。
// 参数：ctx 用于取消调用，intent 是新快照，expectedVersion 是存储中的预期版本。
// 返回：成功时返回 nil。
// 错误：记录不存在或版本不匹配时返回错误。
func (s *MemoryStore) UpdateA2ADispatchIntent(ctx context.Context, intent *domain.TaskA2ADispatchIntent, expectedVersion int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if intent == nil {
		return fmt.Errorf("a2a dispatch intent is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.a2aIntents[intent.ID]
	if !ok {
		return fmt.Errorf("%w: a2a dispatch intent %s", domain.ErrNotFound, intent.ID)
	}
	if existing.Version != expectedVersion {
		return fmt.Errorf("%w: intent %s expected version %d, got %d", ErrA2AConcurrentModification, intent.ID, expectedVersion, existing.Version)
	}
	s.a2aIntents[intent.ID] = cloneA2AIntent(*intent)
	return nil
}

// CommitA2ADispatchResult 原子保存远端绑定 round 与对应 intent 的 OCC 结果。
// 参数：ctx 用于取消调用；round/intent 是新快照；两个 expectedVersion 是各自旧版本。
// 返回：成功时返回 nil。
// 错误：任一记录不存在或版本不匹配时不写入并返回错误。
func (s *MemoryStore) CommitA2ADispatchResult(ctx context.Context, round *domain.TaskA2ARound, expectedRoundVersion int, intent *domain.TaskA2ADispatchIntent, expectedIntentVersion int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if round == nil || intent == nil {
		return fmt.Errorf("a2a round and intent are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateA2AOCCLocked(nil, 0, nil, 0, round, expectedRoundVersion, intent, expectedIntentVersion); err != nil {
		return err
	}
	s.a2aRounds[round.ID] = cloneA2ARound(*round)
	s.a2aIntents[intent.ID] = cloneA2AIntent(*intent)
	return nil
}

func (s *MemoryStore) validateA2ACommitLocked(task *domain.Task, expectedTask int, worker *domain.Worker, expectedWorker int, round *domain.TaskA2ARound, intent *domain.TaskA2ADispatchIntent, createRound bool) error {
	if err := s.validateA2AOCCLocked(task, expectedTask, worker, expectedWorker, nil, 0, nil, 0); err != nil {
		return err
	}
	if round == nil || intent == nil {
		return fmt.Errorf("a2a round and dispatch intent are required")
	}
	if intent.RoundID != round.ID || intent.TaskID != round.TaskID || (intent.Operation.CreatesRound() && intent.CommandID != round.CommandID) {
		return fmt.Errorf("%w: a2a round and intent identity mismatch", domain.ErrConflict)
	}
	existingRound, exists := s.a2aRounds[round.ID]
	if createRound && exists {
		return fmt.Errorf("%w: a2a round %s already exists", domain.ErrConflict, round.ID)
	}
	if createRound {
		for _, other := range s.a2aRounds {
			if other.TaskID == round.TaskID && other.Attempt == round.Attempt && other.Turn == round.Turn {
				return fmt.Errorf("%w: task %s attempt %d turn %d already exists", domain.ErrConflict, round.TaskID, round.Attempt, round.Turn)
			}
		}
	}
	if !createRound && (!exists || existingRound.TaskID != round.TaskID || existingRound.ExecutionID != round.ExecutionID) {
		return fmt.Errorf("%w: a2a round %s is not the active execution", domain.ErrConflict, round.ID)
	}
	if _, exists := s.a2aIntents[intent.ID]; exists {
		return fmt.Errorf("%w: a2a dispatch intent %s already exists", domain.ErrConflict, intent.ID)
	}
	for _, existing := range s.a2aIntents {
		if existing.CommandID == intent.CommandID {
			return fmt.Errorf("%w: a2a command %s already exists", domain.ErrConflict, intent.CommandID)
		}
	}
	return nil
}

func (s *MemoryStore) validateA2AOCCLocked(task *domain.Task, expectedTask int, worker *domain.Worker, expectedWorker int, round *domain.TaskA2ARound, expectedRound int, intent *domain.TaskA2ADispatchIntent, expectedIntent int) error {
	if task != nil && expectedTask > 0 {
		existing, ok := s.tasks[task.ID]
		if !ok || existing.Version != expectedTask {
			return fmt.Errorf("%w: task %s version", ErrA2AConcurrentModification, task.ID)
		}
	}
	if worker != nil && expectedWorker > 0 {
		existing, ok := s.workers[worker.ID]
		if !ok || existing.Version != expectedWorker {
			return fmt.Errorf("%w: worker %s version", ErrA2AConcurrentModification, worker.ID)
		}
	}
	if round != nil {
		existing, ok := s.a2aRounds[round.ID]
		if !ok {
			return fmt.Errorf("%w: a2a round %s", domain.ErrNotFound, round.ID)
		}
		if existing.Version != expectedRound {
			return fmt.Errorf("%w: round %s version", ErrA2AConcurrentModification, round.ID)
		}
		if round.A2ATaskID != "" {
			for _, other := range s.a2aRounds {
				if other.ID != round.ID && other.WorkerID == round.WorkerID && other.A2ATaskID == round.A2ATaskID {
					return fmt.Errorf("%w: worker %s a2a task %s already exists", domain.ErrConflict, round.WorkerID, round.A2ATaskID)
				}
			}
		}
	}
	if intent != nil {
		existing, ok := s.a2aIntents[intent.ID]
		if !ok {
			return fmt.Errorf("%w: a2a dispatch intent %s", domain.ErrNotFound, intent.ID)
		}
		if existing.Version != expectedIntent {
			return fmt.Errorf("%w: intent %s version", ErrA2AConcurrentModification, intent.ID)
		}
	}
	return nil
}

func (s *MemoryStore) validateTaskInteractionOwnerLocked(interaction *domain.TaskInteraction) error {
	if interaction == nil {
		return nil
	}
	existing, ok := s.interactions[interaction.ID]
	if ok && existing.TaskID != interaction.TaskID {
		return fmt.Errorf("%w: task interaction %s belongs to task %s", domain.ErrConflict, interaction.ID, existing.TaskID)
	}
	return nil
}

func (s *MemoryStore) validateProjectionRecordIDsLocked(logs []domain.TaskLog, conversations []domain.ConversationMessage) error {
	// 先验证全部 ID，避免任一冲突发生时留下部分业务投影。
	if err := s.validateConversationIDsLocked(conversations); err != nil {
		return err
	}
	return s.validateTaskLogIDsLocked(logs)
}

func (s *MemoryStore) validateTaskLogIDsLocked(logs []domain.TaskLog) error {
	seen := make(map[string]struct{}, len(s.logs)+len(logs))
	for _, existing := range s.logs {
		seen[existing.ID] = struct{}{}
	}
	for _, log := range logs {
		if _, exists := seen[log.ID]; exists {
			return fmt.Errorf("%w: task log %s already exists", domain.ErrConflict, log.ID)
		}
		seen[log.ID] = struct{}{}
	}
	return nil
}

func (s *MemoryStore) validateConversationIDsLocked(messages []domain.ConversationMessage) error {
	seen := make(map[string]struct{}, len(s.conversations)+len(messages))
	for _, existing := range s.conversations {
		seen[existing.ID] = struct{}{}
	}
	for _, message := range messages {
		if _, exists := seen[message.ID]; exists {
			return fmt.Errorf("%w: task conversation %s already exists", domain.ErrConflict, message.ID)
		}
		seen[message.ID] = struct{}{}
	}
	return nil
}

func (s *MemoryStore) applyA2ACommonLocked(task *domain.Task, worker *domain.Worker, interaction *domain.TaskInteraction, conversations []domain.ConversationMessage, logs []domain.TaskLog, events []domain.DomainEvent, cancelPending bool) {
	if task != nil {
		s.tasks[task.ID] = cloneTask(task)
	}
	if worker != nil {
		s.workers[worker.ID] = cloneWorker(worker)
	}
	if interaction != nil {
		s.interactions[interaction.ID] = *interaction
	}
	if cancelPending && task != nil {
		for id, item := range s.interactions {
			if item.TaskID == task.ID && item.Status == domain.TaskInteractionPending {
				item.Status = domain.TaskInteractionCanceled
				item.ResponseDecision = domain.TaskInteractionCancel
				item.UpdatedAt = task.UpdatedAt
				s.interactions[id] = item
			}
		}
	}
	s.logs = append(s.logs, logs...)
	s.conversations = append(s.conversations, conversations...)
	for _, event := range events {
		duplicate := slices.ContainsFunc(s.events, func(existing domain.DomainEvent) bool { return existing.EventID == event.EventID })
		if duplicate {
			continue
		}
		s.events = append(s.events, event)
		s.outbox = append(s.outbox, domain.OutboxMessage{ID: "out_" + event.EventID, Event: event, Status: domain.OutboxPending, CreatedAt: event.OccurredAt})
	}
}

func cloneA2ARound(round domain.TaskA2ARound) domain.TaskA2ARound {
	copy := round
	copy.LastSyncedAt = cloneTime(round.LastSyncedAt)
	copy.LastNetworkAt = cloneTime(round.LastNetworkAt)
	copy.UnreachableSince = cloneTime(round.UnreachableSince)
	copy.UnknownSince = cloneTime(round.UnknownSince)
	copy.CompletedAt = cloneTime(round.CompletedAt)
	return copy
}

func cloneA2AIntent(intent domain.TaskA2ADispatchIntent) domain.TaskA2ADispatchIntent {
	copy := intent
	copy.Payload = append([]byte(nil), intent.Payload...)
	copy.LastAttemptAt = cloneTime(intent.LastAttemptAt)
	copy.SentAt = cloneTime(intent.SentAt)
	copy.CompletedAt = cloneTime(intent.CompletedAt)
	return copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func a2aInboxKey(roundID, eventID string) string {
	return roundID + "\x00" + eventID
}

func sortA2ARounds(rounds []domain.TaskA2ARound) {
	sort.SliceStable(rounds, func(i, j int) bool {
		if rounds[i].Attempt != rounds[j].Attempt {
			return rounds[i].Attempt < rounds[j].Attempt
		}
		if rounds[i].Turn != rounds[j].Turn {
			return rounds[i].Turn < rounds[j].Turn
		}
		if !rounds[i].CreatedAt.Equal(rounds[j].CreatedAt) {
			return rounds[i].CreatedAt.Before(rounds[j].CreatedAt)
		}
		return rounds[i].ID < rounds[j].ID
	})
}
