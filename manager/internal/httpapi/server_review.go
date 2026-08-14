package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/pkg/frp"
)

func (g *WorkerGateway) ProxyTaskReview(ctx context.Context, taskID, method, path string, input any, out any) error {
	target, err := g.resolveTaskReview(ctx, taskID)
	if err != nil {
		return err
	}
	targetPath, err := taskReviewPath(taskID, path, target.task.WorktreePath, target.task.BaseBranch, target.projectDefaultBranch)
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://worker"+targetPath, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := frp.ProxyHTTP(ctx, target.tunnel.session, req, frp.ProxyTarget{
		Host: target.host,
		Port: target.port,
		Path: targetPath,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("worker review returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}

type taskReviewTarget struct {
	task                 *domain.Task
	projectDefaultBranch string
	tunnel               *workerProxyTunnel
	host                 string
	port                 int
}

func (g *WorkerGateway) resolveTaskReview(ctx context.Context, taskID string) (taskReviewTarget, error) {
	task, err := g.service.Task(ctx, taskID)
	if err != nil {
		return taskReviewTarget{}, err
	}
	if strings.TrimSpace(task.WorktreePath) == "" {
		return taskReviewTarget{}, fmt.Errorf("%w: task %s has no worktree", domain.ErrConflict, task.ID)
	}
	if task.Status == domain.TaskArchived {
		return taskReviewTarget{}, fmt.Errorf("%w: archived task %s has no review", domain.ErrConflict, task.ID)
	}
	worker, err := g.service.Worker(ctx, task.WorkerID)
	if err != nil {
		return taskReviewTarget{}, err
	}
	if worker.Status != domain.WorkerOnline {
		return taskReviewTarget{}, fmt.Errorf("%w: worker %s is not online", domain.ErrConflict, worker.ID)
	}
	host, port, err := reviewCapabilityTarget(worker.Capabilities)
	if err != nil {
		return taskReviewTarget{}, err
	}
	tunnel := g.proxyTunnelByWorkerID(worker.ID)
	if tunnel == nil || tunnel.session == nil || tunnel.session.IsClosed() {
		return taskReviewTarget{}, fmt.Errorf("worker review proxy tunnel is not connected")
	}
	project, err := g.service.Project(ctx, task.ProjectID)
	if err != nil {
		return taskReviewTarget{}, err
	}
	return taskReviewTarget{task: task, projectDefaultBranch: project.DefaultBranch, tunnel: tunnel, host: host, port: port}, nil
}

func reviewCapabilityTarget(capabilities map[string]string) (string, int, error) {
	if !strings.EqualFold(strings.TrimSpace(capabilities["review_enabled"]), "true") {
		return "", 0, fmt.Errorf("worker review is disabled")
	}
	host := strings.TrimSpace(capabilities["review_host"])
	if host == "" {
		host = "127.0.0.1"
	}
	portText := strings.TrimSpace(capabilities["review_port"])
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("worker review port is invalid")
	}
	return host, port, nil
}

func taskReviewPath(taskID, path, worktree, baseBranch, defaultBranch string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "/diff"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	parsed.Path = "/review/tasks/" + url.PathEscape(taskID) + parsed.Path
	query := parsed.Query()
	query.Set("cwd", worktree)
	if strings.TrimSpace(baseBranch) != "" {
		query.Set("baseBranch", baseBranch)
	}
	if strings.TrimSpace(defaultBranch) != "" {
		query.Set("defaultBranch", defaultBranch)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
