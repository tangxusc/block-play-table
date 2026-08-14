package a2astore

import (
	"context"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

// TestExecutionEventsSupportsEverySDKCarrier 覆盖 SDK Task、Artifact、Status、Message 和空事件载体。
func TestExecutionEventsSupportsEverySDKCarrier(t *testing.T) {
	binding := contractBinding("execution-carriers", "task-carriers", "context-carriers")
	event := contractEvent(t, binding, 1, "line")
	part := a2a.NewDataPart(event)
	message := a2a.NewMessage(a2a.MessageRoleAgent, part)
	artifact := &a2a.Artifact{ID: "artifact", Parts: a2a.ContentParts{part}}

	var nilArtifactUpdate *a2a.TaskArtifactUpdateEvent
	var nilStatusUpdate *a2a.TaskStatusUpdateEvent
	for _, test := range []struct {
		name  string
		input a2a.Event
		want  int
	}{
		{name: "task", input: &a2a.Task{Status: a2a.TaskStatus{Message: message}, Artifacts: []*a2a.Artifact{nil, artifact}}, want: 2},
		{name: "nil task", input: (*a2a.Task)(nil)},
		{name: "artifact", input: &a2a.TaskArtifactUpdateEvent{Artifact: artifact}, want: 1},
		{name: "empty artifact", input: &a2a.TaskArtifactUpdateEvent{}},
		{name: "nil artifact update", input: nilArtifactUpdate},
		{name: "status", input: &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: message}}, want: 1},
		{name: "nil status update", input: nilStatusUpdate},
		{name: "message", input: message, want: 1},
		{name: "nil message", input: (*a2a.Message)(nil)},
		{name: "nil event", input: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := executionEvents(test.input)
			if err != nil || len(got) != test.want {
				t.Fatalf("executionEvents() len=%d err=%v, want %d", len(got), err, test.want)
			}
		})
	}

	if got, err := executionEventsFromTask(nil); err != nil || got != nil {
		t.Fatalf("executionEventsFromTask(nil)=%+v err=%v", got, err)
	}
	if got, err := executionEventsFromMessage(nil); err != nil || got != nil {
		t.Fatalf("executionEventsFromMessage(nil)=%+v err=%v", got, err)
	}
}

// TestExecutionEventsFromPartsRejectsMalformedData 覆盖非事件 Part、不可编码值和非法 execution event。
func TestExecutionEventsFromPartsRejectsMalformedData(t *testing.T) {
	binding := contractBinding("execution-parts", "task-parts", "context-parts")
	valid := contractEvent(t, binding, 1, "line")
	parts := a2a.ContentParts{
		nil,
		a2a.NewTextPart("text"),
		a2a.NewDataPart(map[string]any{"kind": "unrelated"}),
		a2a.NewDataPart(valid),
	}
	events, err := executionEventsFromParts(parts)
	if err != nil || len(events) != 1 || events[0].Event.ID != valid.Event.ID {
		t.Fatalf("executionEventsFromParts=%+v err=%v", events, err)
	}

	if _, err := executionEventsFromParts(a2a.ContentParts{a2a.NewDataPart(func() {})}); err == nil {
		t.Fatal("不可编码 DataPart 未失败")
	}
	invalid := map[string]any{"kind": a2aext.EventKind, "version": a2aext.Version}
	if _, err := executionEventsFromParts(a2a.ContentParts{a2a.NewDataPart(invalid)}); err == nil {
		t.Fatal("非法 execution event DataPart 未失败")
	}
}

// TestStoreSchemaHelperBranches 覆盖 Store 打开、任务匹配、裁剪、编码和脱敏的剩余边界。
func TestStoreSchemaHelperBranches(t *testing.T) {
	ctx := context.Background()
	storage, err := Open(ctx, Config{
		Path:          ":memory:",
		Authenticator: func(context.Context) (string, error) { return "owner", nil },
	})
	if err != nil {
		t.Fatalf("Open(:memory:)=%v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("重复关闭数据库=%v", err)
	}
	if err := (&Store{}).Close(); err != nil {
		t.Fatalf("empty Store Close=%v", err)
	}

	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	earlier := now.Add(-time.Minute)
	later := now.Add(time.Minute)
	task := listTestTask("task", "context", a2a.TaskStateWorking, now)
	for _, test := range []struct {
		name    string
		request *a2a.ListTasksRequest
		mutate  func(*a2a.Task)
		want    bool
	}{
		{name: "all", request: &a2a.ListTasksRequest{}, want: true},
		{name: "context match", request: &a2a.ListTasksRequest{ContextID: "context"}, want: true},
		{name: "context mismatch", request: &a2a.ListTasksRequest{ContextID: "other"}},
		{name: "status match", request: &a2a.ListTasksRequest{Status: a2a.TaskStateWorking}, want: true},
		{name: "status mismatch", request: &a2a.ListTasksRequest{Status: a2a.TaskStateCompleted}},
		{name: "after older cutoff", request: &a2a.ListTasksRequest{StatusTimestampAfter: &earlier}, want: true},
		{name: "before newer cutoff", request: &a2a.ListTasksRequest{StatusTimestampAfter: &later}},
		{name: "missing timestamp", request: &a2a.ListTasksRequest{StatusTimestampAfter: &later}, mutate: func(task *a2a.Task) { task.Status.Timestamp = nil }, want: true},
	} {
		t.Run("match/"+test.name, func(t *testing.T) {
			candidate := *task
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			if got := matchesTask(&candidate, test.request); got != test.want {
				t.Fatalf("matchesTask()=%v, want %v", got, test.want)
			}
		})
	}

	longHistory := make([]*a2a.Message, 101)
	for index := range longHistory {
		longHistory[index] = a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("line"))
	}
	trimmed := &a2a.Task{History: longHistory, Artifacts: []*a2a.Artifact{{ID: "artifact"}}}
	trimTask(trimmed, &a2a.ListTasksRequest{})
	if len(trimmed.History) != 100 || trimmed.Artifacts != nil {
		t.Fatalf("default trim=%+v", trimmed)
	}
	negative := -1
	untrimmed := &a2a.Task{History: longHistory, Artifacts: []*a2a.Artifact{{ID: "artifact"}}}
	trimTask(untrimmed, &a2a.ListTasksRequest{HistoryLength: &negative, IncludeArtifacts: true})
	if len(untrimmed.History) != 101 || len(untrimmed.Artifacts) != 1 {
		t.Fatalf("negative/include trim=%+v", untrimmed)
	}

	if err := validateTask(task); err != nil {
		t.Fatalf("valid task: %v", err)
	}
	raw, err := encodeTaskForStorage(&a2a.Task{
		ID: "encoded", ContextID: "context", Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		History:   []*a2a.Message{nil, a2a.NewMessage(a2a.MessageRoleUser, nil, a2a.NewTextPart("text"), a2a.NewDataPart(map[string]any{"kind": "unrelated"}))},
		Artifacts: []*a2a.Artifact{nil, {ID: "artifact", Parts: a2a.ContentParts{nil, a2a.NewTextPart("text")}}},
	})
	if err != nil {
		t.Fatalf("encodeTaskForStorage=%v", err)
	}
	decoded, err := decodeTask(raw)
	if err != nil || decoded.ID != "encoded" {
		t.Fatalf("decodeTask=%+v err=%v", decoded, err)
	}
	if err := sanitizeMessage(nil); err != nil {
		t.Fatalf("sanitizeMessage(nil)=%v", err)
	}
	if err := sanitizeParts(a2a.ContentParts{a2a.NewDataPart(func() {})}); err == nil {
		t.Fatal("不可编码 request DataPart 未失败")
	}
	badRequest := map[string]any{"kind": a2aext.RequestKind, "version": a2aext.Version, "unknown": true}
	if err := sanitizeParts(a2a.ContentParts{a2a.NewDataPart(badRequest)}); err == nil {
		t.Fatal("非法 execution request 未失败")
	}
}
