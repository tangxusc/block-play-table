package a2astore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestRecoverStartupAfterReopenClearsPendingAndOrphanBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a2a.db")
	open := func() *Store {
		store, err := Open(context.Background(), Config{
			Path: path,
			Authenticator: func(context.Context) (string, error) {
				return "recovery-owner", nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	store := open()
	request := testExecutionRequest("pending-command", "secret")
	if _, created, err := store.ReserveCommand(context.Background(), request); err != nil || !created {
		t.Fatalf("ReserveCommand() = created %v, err %v", created, err)
	}
	binding := RuntimeBinding{
		ExecutionID: "orphan-execution", LocalTaskID: "local-task", WorkerID: "worker",
		TaskID: "missing-task", ContextID: "context", Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, State: "SUBMITTED",
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open()
	defer store.Close()
	stats, err := store.RecoverStartup(context.Background())
	if err != nil || stats.Commands != 1 || stats.Bindings != 1 {
		t.Fatalf("RecoverStartup() = %+v, %v", stats, err)
	}
	if _, err := store.LookupCommand(context.Background(), request.Command.ID); err == nil {
		t.Fatal("重开后 pending command 未清理")
	}
	if _, err := store.GetBinding(context.Background(), binding.ExecutionID); err == nil {
		t.Fatal("重开后孤儿 binding 未清理")
	}
}

func TestAppendEventConcurrentCASAndContentConflict(t *testing.T) {
	store := newTestStore(t)
	binding := RuntimeBinding{
		ExecutionID: "execution", LocalTaskID: "local-task", WorkerID: "worker",
		TaskID: "task", ContextID: "context", Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, State: "WORKING",
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	event := testStoreEvent(a2aext.EventLogChunk, 1, time.Now().UTC())
	start := make(chan struct{})
	type appendResult struct {
		duplicate bool
		err       error
	}
	results := make(chan appendResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			duplicate, err := store.AppendEvent(context.Background(), event)
			results <- appendResult{duplicate: duplicate, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var inserted, duplicated int
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.duplicate {
			duplicated++
		} else {
			inserted++
		}
	}
	if inserted != 1 || duplicated != 1 {
		t.Fatalf("并发 AppendEvent inserted=%d duplicated=%d", inserted, duplicated)
	}
	conflict := *event
	conflict.Payload = map[string]any{"stream": string(a2aext.LogStdout), "content": "different"}
	if _, err := store.AppendEvent(context.Background(), &conflict); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("同 event ID 异内容错误 = %v", err)
	}
	bindingAfter, err := store.GetBinding(context.Background(), binding.ExecutionID)
	if err != nil || bindingAfter.LastSequence != 1 {
		t.Fatalf("冲突后 binding = %+v, err %v", bindingAfter, err)
	}
}

func TestTaskUpdateAndJournalSequenceAreAtomic(t *testing.T) {
	store := newTestStore(t)
	task := &a2a.Task{
		ID: "task-atomic", ContextID: "context-atomic", Status: a2a.TaskStatus{State: a2a.TaskStateSubmitted},
	}
	version, err := store.Create(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	binding := RuntimeBinding{
		ExecutionID: "execution", LocalTaskID: "local-task", WorkerID: "worker",
		TaskID: string(task.ID), ContextID: task.ContextID, Attempt: 1, Turn: 1,
		AgentType: a2aext.AgentCodex, State: "SUBMITTED",
	}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	invalidSequence := testStoreEvent(a2aext.EventExecutionDiagnostic, 2, time.Now().UTC())
	task.Status.State = a2a.TaskStateWorking
	message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(invalidSequence))
	message.Extensions = []string{a2aext.ExtensionURI}
	task.Status.Message = message
	if _, err := store.Update(context.Background(), &taskstore.UpdateRequest{
		Task: task, Event: message, PrevVersion: version,
	}); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("journal sequence 冲突错误 = %v", err)
	}
	stored, err := store.Get(context.Background(), task.ID)
	if err != nil || stored.Version != version || stored.Task.Status.State != a2a.TaskStateSubmitted {
		t.Fatalf("冲突后 Task 未回滚: %+v, err %v", stored, err)
	}
	events, err := store.ListEvents(context.Background(), binding.ExecutionID, 0, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("冲突后 journal = %+v, err %v", events, err)
	}
}
