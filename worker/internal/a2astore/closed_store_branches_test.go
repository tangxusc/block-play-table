package a2astore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestStorePublicOperationsPropagateClosedDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	storage, err := Open(ctx, Config{
		Path: filepath.Join(t.TempDir(), "closed.db"),
		Authenticator: func(context.Context) (string, error) {
			return "closed-owner", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	request := testExecutionRequest("closed-command", "secret")
	task := &a2a.Task{ID: "task", ContextID: "context", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}
	binding := RuntimeBinding{
		ExecutionID: request.Scope.ExecutionID, LocalTaskID: request.Scope.LocalTaskID, WorkerID: request.Scope.ExpectedWorkerID,
		TaskID: string(task.ID), ContextID: task.ContextID, Attempt: 1, Turn: 1, AgentType: a2aext.AgentCodex,
	}
	event := testStoreEvent(a2aext.EventExecutionAccepted, 1, time.Now().UTC())

	operations := []struct {
		name string
		run  func() error
	}{
		{"create", func() error { _, err := storage.Create(ctx, task); return err }},
		{"update", func() error {
			_, err := storage.Update(ctx, &taskstore.UpdateRequest{Task: task, PrevVersion: 1})
			return err
		}},
		{"get", func() error { _, err := storage.Get(ctx, task.ID); return err }},
		{"list", func() error { _, err := storage.List(ctx, &a2a.ListTasksRequest{}); return err }},
		{"claim command", func() error {
			_, _, err := storage.ClaimCommand(ctx, request, string(task.ID), task.ContextID)
			return err
		}},
		{"reserve command", func() error { _, _, err := storage.ReserveCommand(ctx, request); return err }},
		{"bind command", func() error { _, err := storage.BindCommand(ctx, request, string(task.ID), task.ContextID); return err }},
		{"abandon command", func() error { _, err := storage.AbandonCommand(ctx, request); return err }},
		{"abandon bound", func() error { _, err := storage.AbandonBoundCommand(ctx, request, string(task.ID)); return err }},
		{"abandon failed bound", func() error { _, err := storage.AbandonFailedBoundCommand(ctx, request, string(task.ID)); return err }},
		{"lookup command", func() error { _, err := storage.LookupCommand(ctx, request.Command.ID); return err }},
		{"save binding", func() error { return storage.SaveBinding(ctx, binding) }},
		{"get binding", func() error { _, err := storage.GetBinding(ctx, binding.ExecutionID); return err }},
		{"get binding by task", func() error { _, err := storage.GetBindingByTask(ctx, binding.TaskID); return err }},
		{"append event", func() error { _, err := storage.AppendEvent(ctx, event); return err }},
		{"list events", func() error { _, err := storage.ListEvents(ctx, binding.ExecutionID, 0, 10); return err }},
		{"recover", func() error { _, err := storage.RecoverStartup(ctx); return err }},
		{"fail non terminal", func() error { _, err := storage.FailNonTerminal(ctx); return err }},
		{"compact", func() error { _, err := storage.CompactBefore(ctx, time.Now()); return err }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err == nil {
				t.Fatal("关闭数据库后操作未返回错误")
			}
		})
	}
}
