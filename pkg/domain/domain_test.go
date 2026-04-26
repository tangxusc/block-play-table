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

func TestTaskDisplayDatesDefaultExplicitUpdateAndValidation(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 30, 0, 0, time.UTC)
	defaultDate := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	task, err := NewTask(NewTaskInput{
		ID:        "task-dates",
		Title:     "Dates",
		ProjectID: "project-1",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if !task.StartDate.Equal(defaultDate) || !task.EndDate.Equal(defaultDate) {
		t.Fatalf("default display dates = %s %s, want %s", task.StartDate, task.EndDate, defaultDate)
	}

	start := time.Date(2026, 5, 1, 18, 0, 0, 0, time.FixedZone("test", 8*60*60))
	end := time.Date(2026, 5, 3, 6, 0, 0, 0, time.UTC)
	explicit, err := NewTask(NewTaskInput{
		ID:        "task-explicit-dates",
		Title:     "Explicit Dates",
		ProjectID: "project-1",
		StartDate: start,
		EndDate:   end,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewTask explicit returned error: %v", err)
	}
	if want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC); !explicit.StartDate.Equal(want) {
		t.Fatalf("explicit start date = %s, want %s", explicit.StartDate, want)
	}
	if want := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC); !explicit.EndDate.Equal(want) {
		t.Fatalf("explicit end date = %s, want %s", explicit.EndDate, want)
	}

	if err := task.Update(NewTaskInput{Title: "Dates updated", ProjectID: "project-1", Now: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Update without dates returned error: %v", err)
	}
	if !task.StartDate.Equal(defaultDate) || !task.EndDate.Equal(defaultDate) {
		t.Fatalf("update without dates should preserve dates: %s %s", task.StartDate, task.EndDate)
	}
	if err := task.Update(NewTaskInput{
		Title:     "Dates moved",
		ProjectID: "project-1",
		StartDate: time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC),
		Now:       now.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("Update with dates returned error: %v", err)
	}
	if !task.StartDate.Equal(time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)) || !task.EndDate.Equal(time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("updated display dates = %s %s", task.StartDate, task.EndDate)
	}

	if _, err := NewTask(NewTaskInput{
		ID:        "task-bad-dates",
		Title:     "Bad Dates",
		ProjectID: "project-1",
		StartDate: time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC),
		Now:       now,
	}); err == nil {
		t.Fatal("NewTask should reject endDate before startDate")
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

func TestWorkerAgentRuntimeEnvMasksFiltersAndPreservesSensitiveValues(t *testing.T) {
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	worker, err := NewWorker(NewWorkerInput{
		ID:              "worker-env",
		Name:            "env worker",
		SupportedAgents: []AgentType{AgentCodex, AgentClaude},
		WorkDir:         "/tmp/work",
		AgentRuntimeEnv: []WorkerAgentRuntimeEnv{
			{
				AgentType: AgentCodex,
				Vars: []AgentRuntimeEnvVar{
					{Key: "OPENAI_API_KEY", Value: "sk-secret", Enabled: true, Sensitive: true},
					{Key: "LOG_LEVEL", Value: "debug", Enabled: true, Sensitive: false},
					{Key: "DISABLED", Value: "hidden", Enabled: false, Sensitive: true},
				},
			},
			{
				AgentType: AgentClaude,
				Vars: []AgentRuntimeEnvVar{
					{Key: "ANTHROPIC_BASE_URL", Value: "https://claude.example", Enabled: true, Sensitive: false},
				},
			},
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("NewWorker returned error: %v", err)
	}

	masked := worker.MaskedAgentRuntimeEnv()
	if masked[0].Vars[0].ValueMasked != "********" || masked[0].Vars[0].Value != "" {
		t.Fatalf("sensitive value should be masked and cleared: %+v", masked[0].Vars[0])
	}
	if masked[0].Vars[1].ValueMasked != "debug" || masked[0].Vars[1].Value != "" {
		t.Fatalf("public value should be visible but clear raw value: %+v", masked[0].Vars[1])
	}

	runtime := worker.EnabledRuntimeEnv(AgentCodex)
	if len(runtime) != 2 {
		t.Fatalf("enabled codex runtime env count = %d, want 2", len(runtime))
	}
	if runtime[0].Key != "OPENAI_API_KEY" || runtime[0].Value != "sk-secret" || !runtime[0].Sensitive {
		t.Fatalf("runtime env should contain real sensitive value: %+v", runtime)
	}
	if claudeRuntime := worker.EnabledRuntimeEnv(AgentClaude); len(claudeRuntime) != 1 || claudeRuntime[0].Key != "ANTHROPIC_BASE_URL" {
		t.Fatalf("claude runtime env = %+v", claudeRuntime)
	}

	if err := worker.Update(NewWorkerInput{
		Name:                   "env worker updated",
		SupportedAgents:        []AgentType{AgentCodex, AgentClaude},
		WorkDir:                "/tmp/work",
		ReplaceAgentRuntimeEnv: true,
		AgentRuntimeEnv: []WorkerAgentRuntimeEnv{
			{
				AgentType: AgentCodex,
				Vars: []AgentRuntimeEnvVar{
					{Key: "OPENAI_API_KEY", Enabled: true, Sensitive: true},
					{Key: "LOG_LEVEL", Enabled: true, Sensitive: false},
				},
			},
		},
		Now: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	runtime = worker.EnabledRuntimeEnv(AgentCodex)
	if runtime[0].Value != "sk-secret" {
		t.Fatalf("sensitive blank value should preserve old secret: %+v", runtime)
	}
	if runtime[1].Value != "" {
		t.Fatalf("public blank value should remain blank: %+v", runtime)
	}
}
