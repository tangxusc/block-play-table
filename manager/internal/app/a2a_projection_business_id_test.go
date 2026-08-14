package app

import (
	"context"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/store"
)

// TestProjectA2AEventNamespacesBusinessIDsByRound 验证不同 round 可安全复用同一协议 eventId。
func TestProjectA2AEventNamespacesBusinessIDsByRound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 22, 0, 0, 0, time.UTC)
	service := NewService(store.NewMemoryStore(), WithClock(func() time.Time { return now }))
	baseTask := seedRunningTask(t, ctx, service)
	baseRound, err := service.Store().LatestTaskA2ARound(ctx, baseTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstRound := *baseRound
	firstRound.ID = "round-business-id-first"
	firstRound.ExecutionID = "execution-business-id-first"
	secondRound := *baseRound
	secondRound.ID = "round-business-id-second"
	secondRound.ExecutionID = "execution-business-id-second"
	secondRound.Attempt++
	sharedEventID := "018f0000-0000-7000-8000-000000000099"

	for _, test := range []struct {
		name      string
		eventType a2aext.EventType
		payload   map[string]any
		prefix    string
	}{
		{name: "log", eventType: a2aext.EventLogChunk, payload: map[string]any{"stream": string(a2aext.LogStdout), "content": "same event id"}, prefix: "log_"},
		{name: "conversation", eventType: a2aext.EventConversationMessage, payload: map[string]any{"role": "assistant", "content": "same event id"}, prefix: "msg_"},
		{name: "diagnostic", eventType: a2aext.EventExecutionDiagnostic, payload: map[string]any{"errorCode": "SHARED_EVENT", "message": "same event id"}, prefix: "log_"},
	} {
		t.Run(test.name, func(t *testing.T) {
			firstTask := *baseTask
			secondTask := *baseTask
			firstProjection := a2aBusinessProjection{}
			secondProjection := a2aBusinessProjection{}
			firstEvent := newTestA2AEvent(t, service, &firstRound, sharedEventID, firstRound.LastSequence+1, test.eventType, nil, test.payload)
			secondEvent := newTestA2AEvent(t, service, &secondRound, sharedEventID, secondRound.LastSequence+1, test.eventType, nil, test.payload)

			if err := service.projectA2AEvent(ctx, &firstTask, &firstRound, firstEvent, &firstProjection, now); err != nil {
				t.Fatalf("首次 round 投影失败: %v", err)
			}
			if err := service.projectA2AEvent(ctx, &secondTask, &secondRound, secondEvent, &secondProjection, now); err != nil {
				t.Fatalf("第二 round 投影失败: %v", err)
			}
			firstID := projectedBusinessID(t, firstProjection)
			secondID := projectedBusinessID(t, secondProjection)
			if firstID == secondID {
				t.Fatalf("跨 round 派生 ID 碰撞: %s", firstID)
			}
			if firstID != test.prefix+firstRound.ID+"_"+sharedEventID || secondID != test.prefix+secondRound.ID+"_"+sharedEventID {
				t.Fatalf("派生 ID = %q, %q", firstID, secondID)
			}
		})
	}
}

func projectedBusinessID(t *testing.T, projection a2aBusinessProjection) string {
	t.Helper()
	if len(projection.logs) == 1 && len(projection.conversations) == 0 {
		return projection.logs[0].ID
	}
	if len(projection.conversations) == 1 && len(projection.logs) == 0 {
		return projection.conversations[0].ID
	}
	t.Fatalf("业务投影数量非法: logs=%+v conversations=%+v", projection.logs, projection.conversations)
	return ""
}
