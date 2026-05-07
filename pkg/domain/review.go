package domain

import "time"

type TaskGitDiffScope string

const (
	TaskGitDiffScopeUncommitted TaskGitDiffScope = "UNCOMMITTED"
	TaskGitDiffScopeBranch      TaskGitDiffScope = "BRANCH"
	TaskGitDiffScopeLastTurn    TaskGitDiffScope = "LAST_TURN"
)

func (s TaskGitDiffScope) Valid() bool {
	return s == "" || s == TaskGitDiffScopeUncommitted || s == TaskGitDiffScopeBranch || s == TaskGitDiffScopeLastTurn
}

type TaskReviewRunStatus string

const (
	TaskReviewRunQueued    TaskReviewRunStatus = "QUEUED"
	TaskReviewRunRunning   TaskReviewRunStatus = "RUNNING"
	TaskReviewRunCompleted TaskReviewRunStatus = "COMPLETED"
	TaskReviewRunFailed    TaskReviewRunStatus = "FAILED"
)

func (s TaskReviewRunStatus) Valid() bool {
	return s == "" || s == TaskReviewRunQueued || s == TaskReviewRunRunning || s == TaskReviewRunCompleted || s == TaskReviewRunFailed
}

type TaskReviewSeverity string

const (
	TaskReviewSeverityInfo     TaskReviewSeverity = "INFO"
	TaskReviewSeverityLow      TaskReviewSeverity = "LOW"
	TaskReviewSeverityMedium   TaskReviewSeverity = "MEDIUM"
	TaskReviewSeverityHigh     TaskReviewSeverity = "HIGH"
	TaskReviewSeverityCritical TaskReviewSeverity = "CRITICAL"
)

func (s TaskReviewSeverity) Valid() bool {
	return s == "" || s == TaskReviewSeverityInfo || s == TaskReviewSeverityLow || s == TaskReviewSeverityMedium || s == TaskReviewSeverityHigh || s == TaskReviewSeverityCritical
}

type TaskReviewFindingStatus string

const (
	TaskReviewFindingOpen      TaskReviewFindingStatus = "OPEN"
	TaskReviewFindingResolved  TaskReviewFindingStatus = "RESOLVED"
	TaskReviewFindingDismissed TaskReviewFindingStatus = "DISMISSED"
)

func (s TaskReviewFindingStatus) Valid() bool {
	return s == "" || s == TaskReviewFindingOpen || s == TaskReviewFindingResolved || s == TaskReviewFindingDismissed
}

type TaskGitTurnSnapshot struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"taskId"`
	BeforeTree string    `json:"beforeTree"`
	AfterTree  string    `json:"afterTree"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type TaskReviewRun struct {
	ID          string              `json:"id"`
	TaskID      string              `json:"taskId"`
	Scope       TaskGitDiffScope    `json:"scope"`
	Status      TaskReviewRunStatus `json:"status"`
	AgentType   AgentType           `json:"agentType,omitempty"`
	Summary     string              `json:"summary,omitempty"`
	RawResult   string              `json:"rawResult,omitempty"`
	Error       string              `json:"error,omitempty"`
	StartedAt   *time.Time          `json:"startedAt,omitempty"`
	CompletedAt *time.Time          `json:"completedAt,omitempty"`
	CreatedAt   time.Time           `json:"createdAt"`
	UpdatedAt   time.Time           `json:"updatedAt"`
}

type TaskReviewFinding struct {
	ID         string                  `json:"id"`
	RunID      string                  `json:"runId"`
	TaskID     string                  `json:"taskId"`
	Path       string                  `json:"path"`
	Line       int                     `json:"line"`
	Severity   TaskReviewSeverity      `json:"severity"`
	Status     TaskReviewFindingStatus `json:"status"`
	Title      string                  `json:"title"`
	Body       string                  `json:"body"`
	Suggestion string                  `json:"suggestion,omitempty"`
	CreatedAt  time.Time               `json:"createdAt"`
	UpdatedAt  time.Time               `json:"updatedAt"`
}

type TaskReviewComment struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	Body      string    `json:"body"`
	Resolved  bool      `json:"resolved"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TaskGitBackup struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Paths     []string  `json:"paths"`
	PatchPath string    `json:"patchPath"`
	CreatedAt time.Time `json:"createdAt"`
}
