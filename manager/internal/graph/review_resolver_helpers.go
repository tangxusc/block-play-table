package graph

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/tangxusc/block-play-table/manager/internal/graph/model"
)

func (r *Resolver) taskGitDiff(ctx context.Context, taskID string, scope model.TaskGitDiffScope, staged *bool) (*model.TaskGitDiff, error) {
	if r.ReviewProxy == nil {
		return nil, fmt.Errorf("task review proxy is not configured")
	}
	query := url.Values{}
	query.Set("scope", string(scope))
	if staged != nil {
		query.Set("staged", fmt.Sprintf("%t", *staged))
	}
	var response TaskGitDiffResponse
	if err := r.ReviewProxy.ProxyTaskReview(ctx, taskID, http.MethodGet, "/diff?"+query.Encode(), nil, &response); err != nil {
		return nil, err
	}
	return toModelTaskGitDiff(&response), nil
}

func (r *Resolver) taskGitStatus(ctx context.Context, taskID string, remote *string, branch *string) (*model.TaskGitStatus, error) {
	if r.ReviewProxy == nil {
		return nil, fmt.Errorf("task review proxy is not configured")
	}
	query := url.Values{}
	if remote != nil {
		query.Set("remote", *remote)
	}
	if branch != nil {
		query.Set("branch", *branch)
	}
	path := "/git-status"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response TaskGitStatusResponse
	if err := r.ReviewProxy.ProxyTaskReview(ctx, taskID, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return toModelTaskGitStatus(&response), nil
}

func (r *Resolver) gitChange(ctx context.Context, action string, input model.TaskGitChangeInput) (*model.TaskGitChangeResult, error) {
	if r.ReviewProxy == nil {
		return nil, fmt.Errorf("task review proxy is not configured")
	}
	request := TaskGitChangeRequest{
		Paths: append([]string(nil), input.Paths...),
		Patch: valueOrEmpty(input.Patch),
	}
	if input.BackupID != nil {
		request.BackupID = *input.BackupID
	}
	var response TaskGitChangeResponse
	if err := r.ReviewProxy.ProxyTaskReview(ctx, input.TaskID, http.MethodPost, "/"+action, request, &response); err != nil {
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
	if r.ReviewProxy == nil {
		return nil, fmt.Errorf("task review proxy is not configured")
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
	if err := r.ReviewProxy.ProxyTaskReview(ctx, input.TaskID, http.MethodPost, "/git-command", request, &response); err != nil {
		return nil, err
	}
	return toModelTaskGitCommandResult(&response), nil
}
