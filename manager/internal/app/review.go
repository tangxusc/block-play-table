package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/protocol"
)

type AddTaskReviewCommentInput struct {
	TaskID string
	Path   string
	Line   int
	Body   string
}

type ContinueTaskWithReviewFeedbackInput struct {
	TaskID     string
	FindingIDs []string
	CommentIDs []string
	Message    string
}

func (s *Service) AddTaskReviewComment(ctx context.Context, input AddTaskReviewCommentInput) (domain.TaskReviewComment, error) {
	if strings.TrimSpace(input.TaskID) == "" {
		return domain.TaskReviewComment{}, fmt.Errorf("taskId is required")
	}
	if strings.TrimSpace(input.Path) == "" {
		return domain.TaskReviewComment{}, fmt.Errorf("path is required")
	}
	if strings.TrimSpace(input.Body) == "" {
		return domain.TaskReviewComment{}, fmt.Errorf("body is required")
	}
	if _, err := s.store.Task(ctx, input.TaskID); err != nil {
		return domain.TaskReviewComment{}, err
	}
	now := s.clock()
	comment := domain.TaskReviewComment{
		ID:        "review_comment_" + uuid.NewString(),
		TaskID:    input.TaskID,
		Path:      input.Path,
		Line:      input.Line,
		Body:      input.Body,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return comment, s.store.SaveTaskReviewComment(ctx, comment)
}

func (s *Service) SaveTaskReviewRun(ctx context.Context, run domain.TaskReviewRun, findings []domain.TaskReviewFinding) (domain.TaskReviewRun, error) {
	if run.ID == "" {
		run.ID = "review_run_" + uuid.NewString()
	}
	if run.Status == "" {
		run.Status = domain.TaskReviewRunQueued
	}
	if run.Scope == "" {
		run.Scope = domain.TaskGitDiffScopeUncommitted
	}
	now := s.clock()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = now
	}
	if err := s.store.SaveTaskReviewRun(ctx, run); err != nil {
		return domain.TaskReviewRun{}, err
	}
	for _, finding := range findings {
		if finding.ID == "" {
			finding.ID = "review_finding_" + uuid.NewString()
		}
		if finding.RunID == "" {
			finding.RunID = run.ID
		}
		if finding.TaskID == "" {
			finding.TaskID = run.TaskID
		}
		if finding.Status == "" {
			finding.Status = domain.TaskReviewFindingOpen
		}
		if finding.Severity == "" {
			finding.Severity = domain.TaskReviewSeverityMedium
		}
		if finding.CreatedAt.IsZero() {
			finding.CreatedAt = now
		}
		if finding.UpdatedAt.IsZero() {
			finding.UpdatedAt = now
		}
		if err := s.store.SaveTaskReviewFinding(ctx, finding); err != nil {
			return domain.TaskReviewRun{}, err
		}
	}
	return run, nil
}

func (s *Service) SaveTaskGitBackup(ctx context.Context, backup domain.TaskGitBackup) error {
	if backup.ID == "" {
		backup.ID = "git_backup_" + uuid.NewString()
	}
	if backup.CreatedAt.IsZero() {
		backup.CreatedAt = s.clock()
	}
	return s.store.SaveTaskGitBackup(ctx, backup)
}

func (s *Service) UpdateTaskReviewFindingStatus(ctx context.Context, id string, status domain.TaskReviewFindingStatus) (domain.TaskReviewFinding, error) {
	if !status.Valid() || status == "" {
		return domain.TaskReviewFinding{}, fmt.Errorf("invalid finding status %q", status)
	}
	finding, err := s.store.TaskReviewFinding(ctx, id)
	if err != nil {
		return domain.TaskReviewFinding{}, err
	}
	finding.Status = status
	finding.UpdatedAt = s.clock()
	if err := s.store.SaveTaskReviewFinding(ctx, *finding); err != nil {
		return domain.TaskReviewFinding{}, err
	}
	return *finding, nil
}

func (s *Service) ContinueTaskWithReviewFeedback(ctx context.Context, input ContinueTaskWithReviewFeedbackInput) (*domain.Task, protocol.TaskContinuePayload, error) {
	message, err := s.reviewFeedbackMessage(ctx, input)
	if err != nil {
		return nil, protocol.TaskContinuePayload{}, err
	}
	return s.ContinueTask(ctx, ContinueTaskInput{TaskID: input.TaskID, Message: message})
}

func (s *Service) reviewFeedbackMessage(ctx context.Context, input ContinueTaskWithReviewFeedbackInput) (string, error) {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(input.Message) != "" {
		parts = append(parts, strings.TrimSpace(input.Message))
	}
	if len(input.FindingIDs) > 0 {
		var builder strings.Builder
		builder.WriteString("Selected review findings:\n")
		for _, id := range input.FindingIDs {
			finding, err := s.store.TaskReviewFinding(ctx, id)
			if err != nil {
				return "", err
			}
			if finding.TaskID != input.TaskID {
				return "", fmt.Errorf("%w: finding %s does not belong to task %s", domain.ErrConflict, id, input.TaskID)
			}
			builder.WriteString("- ")
			builder.WriteString(finding.Path)
			if finding.Line > 0 {
				builder.WriteString(fmt.Sprintf(":%d", finding.Line))
			}
			builder.WriteString(" [")
			builder.WriteString(string(finding.Severity))
			builder.WriteString("] ")
			builder.WriteString(finding.Title)
			if strings.TrimSpace(finding.Body) != "" {
				builder.WriteString(" - ")
				builder.WriteString(strings.TrimSpace(finding.Body))
			}
			if strings.TrimSpace(finding.Suggestion) != "" {
				builder.WriteString(" Suggestion: ")
				builder.WriteString(strings.TrimSpace(finding.Suggestion))
			}
			builder.WriteByte('\n')
		}
		parts = append(parts, strings.TrimSpace(builder.String()))
	}
	if len(input.CommentIDs) > 0 {
		var builder strings.Builder
		builder.WriteString("Selected inline comments:\n")
		for _, id := range input.CommentIDs {
			comment, err := s.store.TaskReviewComment(ctx, id)
			if err != nil {
				return "", err
			}
			if comment.TaskID != input.TaskID {
				return "", fmt.Errorf("%w: comment %s does not belong to task %s", domain.ErrConflict, id, input.TaskID)
			}
			builder.WriteString("- ")
			builder.WriteString(comment.Path)
			if comment.Line > 0 {
				builder.WriteString(fmt.Sprintf(":%d", comment.Line))
			}
			builder.WriteString(" - ")
			builder.WriteString(strings.TrimSpace(comment.Body))
			builder.WriteByte('\n')
		}
		parts = append(parts, strings.TrimSpace(builder.String()))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("review feedback selection is required")
	}
	return strings.Join(parts, "\n\n"), nil
}
