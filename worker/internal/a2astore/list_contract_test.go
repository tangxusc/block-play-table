package a2astore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

func TestStoreListFiltersPaginatesAndTrimsSnapshots(t *testing.T) {
	base := time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC)
	clockTick := 0
	store, err := Open(context.Background(), Config{
		Path:          filepath.Join(t.TempDir(), "list.db"),
		Authenticator: func(context.Context) (string, error) { return "owner", nil },
		Now: func() time.Time {
			clockTick++
			return base.Add(time.Duration(clockTick) * time.Second)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	tasks := []*a2a.Task{
		listTestTask("task-1", "context-a", a2a.TaskStateWorking, base.Add(time.Second)),
		listTestTask("task-2", "context-a", a2a.TaskStateCompleted, base.Add(2*time.Second)),
		listTestTask("task-3", "context-b", a2a.TaskStateWorking, base.Add(3*time.Second)),
	}
	for _, task := range tasks {
		if version, err := store.Create(context.Background(), task); err != nil || version != 1 {
			t.Fatalf("Create(%s) version=%d err=%v", task.ID, version, err)
		}
	}
	if _, err := store.Create(context.Background(), tasks[0]); !errors.Is(err, taskstore.ErrTaskAlreadyExists) {
		t.Fatalf("重复 Create 错误=%v", err)
	}

	firstPage, err := store.List(context.Background(), &a2a.ListTasksRequest{PageSize: 1})
	if err != nil || len(firstPage.Tasks) != 1 || firstPage.TotalSize != 3 || firstPage.NextPageToken == "" {
		t.Fatalf("首页=%+v err=%v", firstPage, err)
	}
	secondPage, err := store.List(context.Background(), &a2a.ListTasksRequest{PageSize: 1, PageToken: firstPage.NextPageToken})
	if err != nil || len(secondPage.Tasks) != 1 || secondPage.Tasks[0].ID == firstPage.Tasks[0].ID {
		t.Fatalf("第二页=%+v err=%v", secondPage, err)
	}

	historyLength := 1
	filtered, err := store.List(context.Background(), &a2a.ListTasksRequest{
		ContextID: "context-a", Status: a2a.TaskStateWorking, PageSize: 10,
		HistoryLength: &historyLength, IncludeArtifacts: true,
	})
	if err != nil || len(filtered.Tasks) != 1 || len(filtered.Tasks[0].History) != 1 || len(filtered.Tasks[0].Artifacts) != 1 {
		t.Fatalf("过滤/裁剪=%+v err=%v", filtered, err)
	}
	zero := 0
	trimmed, err := store.List(context.Background(), &a2a.ListTasksRequest{PageSize: 10, HistoryLength: &zero})
	if err != nil || len(trimmed.Tasks) != 3 {
		t.Fatalf("零历史列表=%+v err=%v", trimmed, err)
	}
	for _, task := range trimmed.Tasks {
		if len(task.History) != 0 || task.Artifacts != nil {
			t.Fatalf("Task 未裁剪 history/artifacts: %+v", task)
		}
	}
	cutoff := base.Add(2500 * time.Millisecond)
	after, err := store.List(context.Background(), &a2a.ListTasksRequest{PageSize: 10, StatusTimestampAfter: &cutoff})
	if err != nil || len(after.Tasks) != 1 || after.Tasks[0].ID != "task-3" {
		t.Fatalf("时间过滤=%+v err=%v", after, err)
	}
}

func TestStoreListAndTaskValidationErrors(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.List(context.Background(), nil); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("nil List 错误=%v", err)
	}
	for _, pageSize := range []int{-1, 101} {
		if _, err := store.List(context.Background(), &a2a.ListTasksRequest{PageSize: pageSize}); !errors.Is(err, a2a.ErrInvalidRequest) {
			t.Fatalf("pageSize %d 错误=%v", pageSize, err)
		}
	}
	for _, token := range []string{"%%%", "bm8tc2VwYXJhdG9y", encodeCursor(0, "task"), encodeCursor(1, "")} {
		if _, _, err := decodeCursor(token); !errors.Is(err, a2a.ErrParseError) {
			t.Fatalf("非法 cursor %q 错误=%v", token, err)
		}
	}
	timestamp, taskID, err := decodeCursor(encodeCursor(123, "task"))
	if err != nil || timestamp != 123 || taskID != "task" {
		t.Fatalf("cursor 往返=(%d,%q,%v)", timestamp, taskID, err)
	}
	if timestamp, taskID, err := decodeCursor(""); err != nil || timestamp != 0 || taskID != "" {
		t.Fatalf("空 cursor=(%d,%q,%v)", timestamp, taskID, err)
	}

	for _, task := range []*a2a.Task{
		nil,
		{ContextID: "context", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}},
		{ID: "task", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}},
		{ID: "task", ContextID: "context", Status: a2a.TaskStatus{State: a2a.TaskStateUnspecified}},
	} {
		if err := validateTask(task); !errors.Is(err, a2a.ErrInvalidParams) {
			t.Fatalf("非法 Task %+v 错误=%v", task, err)
		}
	}
	if _, err := decodeTask([]byte("not-json")); err == nil {
		t.Fatal("畸形 Task JSON 未失败")
	}
	if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("缺失 Get 错误=%v", err)
	}
	if _, err := store.Update(context.Background(), nil); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("nil Update 错误=%v", err)
	}
	if _, err := store.Update(context.Background(), &taskstore.UpdateRequest{Task: listTestTask("missing", "context", a2a.TaskStateWorking, time.Now()), PrevVersion: 1}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("缺失 Update 错误=%v", err)
	}
	if !isUniqueViolation(errors.New("UNIQUE constraint failed")) || !isUniqueViolation(errors.New("constraint failed")) || isUniqueViolation(errors.New("other")) {
		t.Fatal("SQLite unique violation 分类错误")
	}
}

func TestStoreOpenAndAuthenticationValidation(t *testing.T) {
	if _, err := Open(context.Background(), Config{}); err == nil {
		t.Fatal("空 Store path 未失败")
	}
	if _, err := Open(context.Background(), Config{Path: ":memory:"}); err == nil {
		t.Fatal("空 authenticator 未失败")
	}
	for name, authenticator := range map[string]taskstore.Authenticator{
		"empty": func(context.Context) (string, error) { return " ", nil },
		"error": func(context.Context) (string, error) { return "owner", errors.New("auth failed") },
	} {
		t.Run(name, func(t *testing.T) {
			store, err := Open(context.Background(), Config{Path: filepath.Join(t.TempDir(), "auth.db"), Authenticator: authenticator})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.List(context.Background(), &a2a.ListTasksRequest{}); !errors.Is(err, a2a.ErrUnauthenticated) {
				t.Fatalf("认证错误=%v", err)
			}
		})
	}
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil Store Close=%v", err)
	}
}

func listTestTask(id, contextID string, state a2a.TaskState, timestamp time.Time) *a2a.Task {
	return &a2a.Task{
		ID: a2a.TaskID(id), ContextID: contextID,
		Status: a2a.TaskStatus{State: state, Timestamp: &timestamp},
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("one")),
			a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("two")),
		},
		Artifacts: []*a2a.Artifact{{ID: a2a.ArtifactID("artifact-" + id), Name: "result", Parts: a2a.ContentParts{a2a.NewTextPart("result")}}},
	}
}
