package graph

import (
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

type TaskGitDiffResponse struct {
	TaskID      string            `json:"taskId"`
	Scope       string            `json:"scope"`
	BaseRef     string            `json:"baseRef,omitempty"`
	HeadRef     string            `json:"headRef,omitempty"`
	Files       []TaskGitDiffFile `json:"files"`
	Truncated   bool              `json:"truncated"`
	GeneratedAt time.Time         `json:"generatedAt"`
}

type TaskGitDiffFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Staged    bool   `json:"staged"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
}

type TaskGitChangeRequest struct {
	Paths    []string `json:"paths,omitempty"`
	Patch    string   `json:"patch,omitempty"`
	BackupID string   `json:"backupId,omitempty"`
}

type TaskGitChangeResponse struct {
	OK     bool                  `json:"ok"`
	Backup *domain.TaskGitBackup `json:"backup,omitempty"`
	Diff   *TaskGitDiffResponse  `json:"diff,omitempty"`
}

type TaskGitCommandRequest struct {
	Command         string `json:"command"`
	Message         string `json:"message,omitempty"`
	Remote          string `json:"remote,omitempty"`
	Branch          string `json:"branch,omitempty"`
	PublishStrategy string `json:"publishStrategy,omitempty"`
}

type TaskGitCommandResponse struct {
	OK      bool                 `json:"ok"`
	Command string               `json:"command"`
	Output  string               `json:"output,omitempty"`
	HeadRef string               `json:"headRef,omitempty"`
	BaseRef string               `json:"baseRef,omitempty"`
	Diff    *TaskGitDiffResponse `json:"diff,omitempty"`
}

type TaskGitStatusResponse struct {
	TaskID             string    `json:"taskId"`
	Remote             string    `json:"remote"`
	Branch             string    `json:"branch"`
	CurrentBranch      string    `json:"currentBranch"`
	HeadRef            string    `json:"headRef,omitempty"`
	TargetRef          string    `json:"targetRef,omitempty"`
	Ahead              int       `json:"ahead"`
	Behind             int       `json:"behind"`
	HasStagedChanges   bool      `json:"hasStagedChanges"`
	HasUnstagedChanges bool      `json:"hasUnstagedChanges"`
	HasUntrackedFiles  bool      `json:"hasUntrackedFiles"`
	GeneratedAt        time.Time `json:"generatedAt"`
}
