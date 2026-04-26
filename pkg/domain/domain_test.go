package domain

import (
	"testing"
	"time"
)

func TestTaskLifecycleEmitsEventsAndRejectsInvalidTransitions(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	task, err := NewTask(NewTaskInput{
		ID:          "task-1",
		Title:       "Implement feature",
		Description: "Do the work",
		ProjectID:   "project-1",
		AgentType:   AgentCodex,
		BaseBranch:  "main",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if task.Status != TaskCreated {
		t.Fatalf("status = %s, want %s", task.Status, TaskCreated)
	}
	if len(task.PullEvents()) != 1 {
		t.Fatalf("created task should emit one event")
	}

	if err := task.Start(now); err == nil {
		t.Fatalf("Start on unassigned task should fail")
	}
	if err := task.AssignWorker("worker-1", now); err != nil {
		t.Fatalf("AssignWorker returned error: %v", err)
	}
	if err := task.Start(now); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := task.MarkRunning("/tmp/worktree", now); err != nil {
		t.Fatalf("MarkRunning returned error: %v", err)
	}
	if err := task.AppendLog("stdout", "hello", now); err != nil {
		t.Fatalf("AppendLog returned error: %v", err)
	}
	if err := task.Complete("done", now); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if err := task.Fail("late failure", now); err == nil {
		t.Fatalf("Fail after completed should fail")
	}
	if task.Status != TaskCompleted {
		t.Fatalf("status = %s, want %s", task.Status, TaskCompleted)
	}
}

func TestWorkerCanAcceptTaskHonorsStatusAgentProjectAndOccupancy(t *testing.T) {
	worker, err := NewWorker(NewWorkerInput{
		ID:                 "worker-1",
		Name:               "local",
		SupportedAgents:    []AgentType{AgentCodex},
		WorkDir:            "/tmp/work",
		ProjectBindingMode: WorkerSpecificProjects,
		BoundProjectIDs:    []string{"project-1"},
		Now:                time.Now(),
	})
	if err != nil {
		t.Fatalf("NewWorker returned error: %v", err)
	}
	worker.Connect(time.Now())

	if !worker.CanAcceptTask(AgentCodex, "project-1") {
		t.Fatalf("worker should accept matching codex task")
	}
	if worker.CanAcceptTask(AgentClaude, "project-1") {
		t.Fatalf("worker should reject unsupported agent")
	}
	if worker.CanAcceptTask(AgentCodex, "project-2") {
		t.Fatalf("worker should reject unbound project")
	}
	if err := worker.AssignTask("task-1", time.Now()); err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}
	if worker.CanAcceptTask(AgentCodex, "project-1") {
		t.Fatalf("worker with current task should not be assignable")
	}
	worker.ReleaseTask(time.Now())
	worker.ShareAcrossAllProjects(time.Now())
	if !worker.CanAcceptTask(AgentCodex, "project-2") {
		t.Fatalf("ALL_PROJECTS worker should accept any project")
	}
}

func TestAgentRuntimeEnvVarsMaskSensitiveValues(t *testing.T) {
	settings := Settings{
		AgentRuntimeEnvVars: []AgentRuntimeEnvVar{
			{Key: "OPENAI_API_KEY", Value: "sk-secret", Enabled: true, Sensitive: true},
			{Key: "LOG_LEVEL", Value: "debug", Enabled: true, Sensitive: false},
			{Key: "DISABLED", Value: "hidden", Enabled: false, Sensitive: true},
		},
	}

	masked := settings.MaskedEnvVars()
	if masked[0].ValueMasked != "********" {
		t.Fatalf("sensitive value masked as %q", masked[0].ValueMasked)
	}
	if masked[1].ValueMasked != "debug" {
		t.Fatalf("plain value masked as %q", masked[1].ValueMasked)
	}

	runtime := settings.EnabledRuntimeEnv()
	if len(runtime) != 2 {
		t.Fatalf("enabled runtime env count = %d, want 2", len(runtime))
	}
	if runtime["OPENAI_API_KEY"] != "sk-secret" {
		t.Fatalf("runtime env should contain real sensitive value")
	}
}
