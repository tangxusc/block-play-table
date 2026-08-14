package domain

import (
	"testing"
	"time"
)

// FuzzTaskA2ARoundSequence 验证远端 sequence 单调性和终态不可逆约束。
func FuzzTaskA2ARoundSequence(f *testing.F) {
	f.Add(uint16(0), uint16(1), uint8(1))
	f.Add(uint16(5), uint16(4), uint8(5))
	f.Add(uint16(5), uint16(6), uint8(2))
	f.Fuzz(func(t *testing.T, current, next uint16, statusIndex uint8) {
		statuses := []TaskA2ARemoteStatus{
			TaskA2ARemoteStatusSubmitted,
			TaskA2ARemoteStatusWorking,
			TaskA2ARemoteStatusInputRequired,
			TaskA2ARemoteStatusAuthRequired,
			TaskA2ARemoteStatusCompleted,
			TaskA2ARemoteStatusFailed,
			TaskA2ARemoteStatusRejected,
			TaskA2ARemoteStatusCanceled,
			TaskA2ARemoteStatusUnknown,
		}
		round := TaskA2ARound{
			A2ATaskID: "remote-task", ContextID: "context", RemoteStatus: TaskA2ARemoteStatusWorking,
			LastSequence: int64(current), Version: 1, UpdatedAt: time.Unix(1, 0).UTC(),
		}
		before := round
		status := statuses[int(statusIndex)%len(statuses)]
		changed, err := round.ApplyRemoteSnapshot(status, int64(next), time.Unix(2, 0).UTC())
		if err != nil {
			if round != before {
				t.Fatalf("失败的快照更新改变了 round：before=%+v after=%+v", before, round)
			}
			return
		}
		if round.LastSequence != int64(next) {
			t.Fatalf("成功更新后的 sequence=%d，期望 %d", round.LastSequence, next)
		}
		if !changed && round.Version != before.Version {
			t.Fatalf("无变化更新推进了版本：%d -> %d", before.Version, round.Version)
		}
	})
}
