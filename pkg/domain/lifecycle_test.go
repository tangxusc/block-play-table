package domain

import (
	"testing"
	"time"
)

func TestTaskInterruptArchiveWaitingAndConversation(t *testing.T) {
	now := time.Now().UTC()
	task, err := NewTask(NewTaskInput{ID: "task-2", Title: "T", ProjectID: "project-1", AgentType: AgentClaude, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = task.PullEvents()
	if err := task.AssignWorker("worker-1", now); err != nil {
		t.Fatal(err)
	}
	if err := task.Start(now); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkRunning("/tmp/w", now); err != nil {
		t.Fatal(err)
	}
	if err := task.AppendConversation("assistant", "hello", now); err != nil {
		t.Fatal(err)
	}
	if err := task.WaitForInput(now); err != nil {
		t.Fatal(err)
	}
	if err := task.Resume(now); err != nil {
		t.Fatal(err)
	}
	if err := task.RequestInterrupt(now); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkInterrupted(now); err != nil {
		t.Fatal(err)
	}
	if err := task.Archive(now); err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskArchived {
		t.Fatalf("status = %s, want archived", task.Status)
	}
	if len(task.PullEvents()) == 0 {
		t.Fatal("expected lifecycle events")
	}
}

func TestTaskConstructorValidation(t *testing.T) {
	tests := []NewTaskInput{
		{ID: "", Title: "T", ProjectID: "p", AgentType: AgentCodex},
		{ID: "t", Title: "", ProjectID: "p", AgentType: AgentCodex},
		{ID: "t", Title: "T", ProjectID: "", AgentType: AgentCodex},
		{ID: "t", Title: "T", ProjectID: "p", AgentType: AgentType("bad")},
	}
	for _, input := range tests {
		if _, err := NewTask(input); err == nil {
			t.Fatalf("NewTask(%+v) should fail", input)
		}
	}
}

func TestWorkerLifecycleControlsAvailability(t *testing.T) {
	now := time.Now().UTC()
	worker, err := NewWorker(NewWorkerInput{ID: "worker-2", Name: "W", SupportedAgents: []AgentType{AgentCodex, AgentClaude}, WorkDir: "/tmp", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	worker.Connect(now)
	worker.Heartbeat(now)
	worker.BindProjects([]string{"project-1"}, now)
	if !worker.CanAcceptTask(AgentClaude, "project-1") {
		t.Fatal("worker should accept bound claude task")
	}
	worker.Disable(now)
	if worker.CanAcceptTask(AgentClaude, "project-1") {
		t.Fatal("disabled worker should not accept task")
	}
	worker.Enable(now)
	worker.Connect(now)
	worker.MarkOffline(now)
	if worker.Status != WorkerOffline {
		t.Fatalf("status = %s, want offline", worker.Status)
	}
	if len(worker.PullEvents()) == 0 {
		t.Fatal("expected worker events")
	}
}

func TestProjectUpdateArchiveAndValidation(t *testing.T) {
	now := time.Now().UTC()
	project, err := NewProject(NewProjectInput{ID: "project-1", Name: "P", GitURL: "git://repo", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if project.DefaultBranch != "main" || project.WorktreeNamePrefix != "project-1" {
		t.Fatalf("defaults not applied: %+v", project)
	}
	if err := project.Update("P2", "git://repo2", "develop", "prefix", []string{"make"}, now); err != nil {
		t.Fatal(err)
	}
	project.Archive(now)
	if !project.Archived {
		t.Fatal("project should be archived")
	}
	if _, err := NewProject(NewProjectInput{ID: "", Name: "P", GitURL: "git://repo"}); err == nil {
		t.Fatal("blank project id should fail")
	}
}
