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

type TaskGitTurnSnapshot struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"taskId"`
	BeforeTree string    `json:"beforeTree"`
	AfterTree  string    `json:"afterTree"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type TaskGitBackup struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Paths     []string  `json:"paths"`
	PatchPath string    `json:"patchPath"`
	CreatedAt time.Time `json:"createdAt"`
}
