package graph

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/graph/model"
	"github.com/tangxusc/block-play-table/pkg/domain"
)

func (r *Resolver) taskGitDiff(ctx context.Context, taskID string, scope model.TaskGitDiffScope, staged *bool) (*model.TaskGitDiff, error) {
	if r.WorkerSender == nil {
		return nil, fmt.Errorf("worker sender is not configured")
	}
	query := url.Values{}
	query.Set("scope", string(scope))
	if staged != nil {
		query.Set("staged", fmt.Sprintf("%t", *staged))
	}
	var response TaskGitDiffResponse
	if err := r.WorkerSender.ProxyTaskReview(ctx, taskID, http.MethodGet, "/diff?"+query.Encode(), nil, &response); err != nil {
		return nil, err
	}
	return toModelTaskGitDiff(&response), nil
}

func (r *Resolver) startTaskReview(ctx context.Context, input model.StartTaskReviewInput) (*model.TaskReviewRun, error) {
	if r.WorkerSender == nil {
		return nil, fmt.Errorf("worker sender is not configured")
	}
	var response TaskReviewRunResponse
	if err := r.WorkerSender.ProxyTaskReview(ctx, input.TaskID, http.MethodPost, "/runs", map[string]string{"scope": string(input.Scope)}, &response); err != nil {
		return nil, err
	}
	run, err := r.Service.SaveTaskReviewRun(ctx, response.Run, response.Findings)
	if err != nil {
		return nil, err
	}
	findings, err := r.Service.Store().TaskReviewFindings(ctx, input.TaskID, "")
	if err != nil {
		return nil, err
	}
	return toModelTaskReviewRun(run, filterFindingsByRun(findings, run.ID)), nil
}

func (r *Resolver) gitChange(ctx context.Context, action string, input model.TaskGitChangeInput) (*model.TaskGitChangeResult, error) {
	if r.WorkerSender == nil {
		return nil, fmt.Errorf("worker sender is not configured")
	}
	request := TaskGitChangeRequest{
		Paths: append([]string(nil), input.Paths...),
		Patch: valueOrEmpty(input.Patch),
	}
	if input.BackupID != nil {
		request.BackupID = *input.BackupID
	}
	var response TaskGitChangeResponse
	if err := r.WorkerSender.ProxyTaskReview(ctx, input.TaskID, http.MethodPost, "/"+action, request, &response); err != nil {
		return nil, err
	}
	if response.Backup != nil {
		if err := r.Service.SaveTaskGitBackup(ctx, *response.Backup); err != nil {
			return nil, err
		}
	}
	return toModelTaskGitChangeResult(&response), nil
}

func (r *Resolver) runTaskGitCommand(ctx context.Context, input model.TaskGitCommandInput) (*model.TaskGitCommandResult, error) {
	if r.WorkerSender == nil {
		return nil, fmt.Errorf("worker sender is not configured")
	}
	request := TaskGitCommandRequest{
		Command: string(input.Command),
		Message: valueOrEmpty(input.Message),
		Remote:  valueOrEmpty(input.Remote),
		Branch:  valueOrEmpty(input.Branch),
	}
	if input.PublishStrategy != nil {
		request.PublishStrategy = string(*input.PublishStrategy)
	}
	var response TaskGitCommandResponse
	if err := r.WorkerSender.ProxyTaskReview(ctx, input.TaskID, http.MethodPost, "/git-command", request, &response); err != nil {
		return nil, err
	}
	return toModelTaskGitCommandResult(&response), nil
}

func (r *Resolver) continueWithReviewFeedback(ctx context.Context, input model.ContinueTaskWithReviewFeedbackInput) (*model.Task, error) {
	task, payload, err := r.Service.ContinueTaskWithReviewFeedback(ctx, app.ContinueTaskWithReviewFeedbackInput{
		TaskID:     input.TaskID,
		FindingIDs: append([]string(nil), input.FindingIds...),
		CommentIDs: append([]string(nil), input.CommentIds...),
		Message:    valueOrEmpty(input.Message),
	})
	if err != nil {
		return nil, err
	}
	if r.WorkerSender != nil {
		if err := r.WorkerSender.SendTaskContinue(task.WorkerID, task.ID, payload); err != nil {
			_, _ = r.Service.ApplyWorkerTaskFailed(ctx, "review-feedback-delivery-failed-"+task.ID, task.ID, "task continue delivery failed: "+err.Error())
			return nil, err
		}
	}
	return toModelTask(task), nil
}

func reviewFindingsByRun(findings []domain.TaskReviewFinding) map[string][]domain.TaskReviewFinding {
	out := map[string][]domain.TaskReviewFinding{}
	for _, finding := range findings {
		out[finding.RunID] = append(out[finding.RunID], finding)
	}
	return out
}

func filterFindingsByRun(findings []domain.TaskReviewFinding, runID string) []domain.TaskReviewFinding {
	out := make([]domain.TaskReviewFinding, 0)
	for _, finding := range findings {
		if finding.RunID == runID {
			out = append(out, finding)
		}
	}
	return out
}
