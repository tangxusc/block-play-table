package domain

import (
	"testing"
	"time"
)

func TestTaskA2ARoundDuplicateSnapshotPersistsNetworkActivity(t *testing.T) {
	createdAt := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	round, err := NewTaskA2ARound(NewTaskA2ARoundInput{
		ID: "round-1", TaskID: "task-1", ExecutionID: "execution-1", Attempt: 1, Turn: 1,
		Operation: TaskA2AOperationStart, WorkerID: "worker-1", CommandID: "command-1", Now: createdAt,
	})
	if err != nil {
		t.Fatalf("NewTaskA2ARound 返回错误: %v", err)
	}
	firstSync := createdAt.Add(time.Second)
	if err := round.BindRemote("remote-task-1", "context-1", TaskA2ARemoteStatusWorking, firstSync); err != nil {
		t.Fatalf("BindRemote 返回错误: %v", err)
	}
	if _, err := round.ApplyRemoteSnapshot(TaskA2ARemoteStatusWorking, 3, firstSync); err != nil {
		t.Fatalf("首次 ApplyRemoteSnapshot 返回错误: %v", err)
	}
	round.MarkUnreachable(firstSync.Add(30 * time.Second))
	version := round.Version
	refreshedAt := firstSync.Add(45 * time.Second)
	changed, err := round.ApplyRemoteSnapshot(TaskA2ARemoteStatusWorking, 3, refreshedAt)
	if err != nil {
		t.Fatalf("重复 ApplyRemoteSnapshot 返回错误: %v", err)
	}
	if !changed {
		t.Fatal("重复快照必须触发持久化")
	}
	if round.Version != version+1 {
		t.Fatalf("版本未推进: got %d want %d", round.Version, version+1)
	}
	if round.LastNetworkAt == nil || !round.LastNetworkAt.Equal(refreshedAt) {
		t.Fatalf("网络活跃时间未刷新: %#v", round.LastNetworkAt)
	}
	if round.UnreachableSince != nil {
		t.Fatalf("成功快照后未清除失联标记: %#v", round.UnreachableSince)
	}
}
