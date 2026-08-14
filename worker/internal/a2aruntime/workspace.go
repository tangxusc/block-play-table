package a2aruntime

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

type gitWorktreeEntry struct {
	path   string
	branch string
}

func (r *Runtime) prepareWorkspace(ctx context.Context, request *a2aext.ExecutionRequest) (string, string, error) {
	if request.Worktree.Mode == a2aext.WorktreeResume {
		path, err := a2aext.ValidateWorktreePath(r.workDir, request.Resume.WorktreePath)
		if err != nil {
			return "", "", err
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return "", "", fmt.Errorf("续接 worktree 不可用: %s", path)
		}
		return path, taskBranch(request), nil
	}
	if err := os.MkdirAll(r.workDir, 0o755); err != nil {
		return "", "", fmt.Errorf("创建 Worker 工作目录: %w", err)
	}
	target := filepath.Join(r.workDir, worktreeName(request))
	target, err := a2aext.ValidateWorktreePath(r.workDir, target)
	if err != nil {
		return "", "", err
	}
	branch := taskBranch(request)
	if request.Project.GitURL == "" {
		if _, statErr := os.Stat(target); statErr == nil {
			if request.Worktree.Mode != a2aext.WorktreeRecreate {
				return "", "", fmt.Errorf("worktree 目标已存在: %s", target)
			}
			if err := os.RemoveAll(target); err != nil {
				return "", "", fmt.Errorf("重建 worktree: %w", err)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", "", statErr
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return "", "", fmt.Errorf("创建 worktree: %w", err)
		}
		return target, branch, nil
	}
	cacheDir := filepath.Join(r.workDir, ".repos", repositoryCacheName(request))
	repositoryLock := r.repositoryLock(cacheDir)
	repositoryLock.Lock()
	defer repositoryLock.Unlock()
	if err := r.ensureRepositoryCache(ctx, request.Project.GitURL, cacheDir); err != nil {
		return "", "", err
	}
	if _, err := runProcess(ctx, cacheDir, nil, "git", "worktree", "prune"); err != nil {
		return "", "", err
	}
	if err := removeWorktreesForBranch(ctx, cacheDir, branch); err != nil {
		return "", "", err
	}
	if _, statErr := os.Stat(target); statErr == nil {
		if request.Worktree.Mode != a2aext.WorktreeRecreate {
			return "", "", fmt.Errorf("worktree 目标已存在: %s", target)
		}
		if err := os.RemoveAll(target); err != nil {
			return "", "", fmt.Errorf("删除旧 worktree 目录: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", statErr
	}
	base := strings.TrimSpace(request.Task.BaseBranch)
	if base == "" {
		base = strings.TrimSpace(request.Project.DefaultBranch)
	}
	if base == "" {
		base = "HEAD"
	}
	ref := resolveGitRef(ctx, cacheDir, base)
	if _, err := runProcess(ctx, cacheDir, nil, "git", "worktree", "add", "-B", branch, target, ref); err != nil {
		return "", "", err
	}
	return target, branch, nil
}

func (r *Runtime) repositoryLock(cacheDir string) *sync.Mutex {
	value, _ := r.repositoryLocks.LoadOrStore(filepath.Clean(cacheDir), &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (r *Runtime) ensureRepositoryCache(ctx context.Context, gitURL, cacheDir string) error {
	if info, err := os.Stat(filepath.Join(cacheDir, ".git")); err == nil && info.IsDir() {
		_, err = runProcess(ctx, cacheDir, nil, "git", "fetch", "--all", "--prune")
		return err
	}
	if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
		return fmt.Errorf("仓库缓存存在但不是 Git 仓库: %s", cacheDir)
	}
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return fmt.Errorf("创建仓库缓存目录: %w", err)
	}
	_, err := runProcess(ctx, "", nil, "git", "clone", gitURL, cacheDir)
	return err
}

func resolveGitRef(ctx context.Context, repoDir, ref string) string {
	if ref == "" || ref == "HEAD" {
		return "HEAD"
	}
	if _, err := runProcess(ctx, repoDir, nil, "git", "rev-parse", "--verify", ref+"^{commit}"); err == nil {
		return ref
	}
	remote := "origin/" + ref
	if _, err := runProcess(ctx, repoDir, nil, "git", "rev-parse", "--verify", remote+"^{commit}"); err == nil {
		return remote
	}
	return ref
}

func removeWorktreesForBranch(ctx context.Context, repoDir, branch string) error {
	out, err := runProcess(ctx, repoDir, nil, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	want := "refs/heads/" + branch
	for _, entry := range parseGitWorktreeList(out) {
		if entry.branch != want {
			continue
		}
		if samePath(entry.path, repoDir) {
			return fmt.Errorf("任务分支 %s 正被仓库缓存签出", branch)
		}
		if _, err := runProcess(ctx, repoDir, nil, "git", "worktree", "remove", "--force", entry.path); err != nil {
			return err
		}
	}
	return nil
}

func parseGitWorktreeList(raw []byte) []gitWorktreeEntry {
	var result []gitWorktreeEntry
	var current gitWorktreeEntry
	flush := func() {
		if current.path != "" {
			result = append(result, current)
		}
		current = gitWorktreeEntry{}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "worktree":
			if current.path != "" {
				flush()
			}
			current.path = value
		case "branch":
			current.branch = value
		}
	}
	flush()
	return result
}

func worktreeName(request *a2aext.ExecutionRequest) string {
	prefix := firstNonEmpty(request.Project.WorktreeNamePrefix, request.Project.ID, "task")
	sum := sha1.Sum([]byte(request.Scope.ExecutionID))
	return safePathPart(prefix + "-" + request.Scope.LocalTaskID + "-" + hex.EncodeToString(sum[:])[:12])
}

func repositoryCacheName(request *a2aext.ExecutionRequest) string {
	label := firstNonEmpty(request.Project.WorktreeNamePrefix, request.Project.ID, "project")
	sum := sha1.Sum([]byte(request.Project.ID + "\x00" + request.Project.GitURL))
	return safePathPart(label) + "-" + hex.EncodeToString(sum[:])[:12]
}

func taskBranch(request *a2aext.ExecutionRequest) string {
	sum := sha1.Sum([]byte(request.Scope.ExecutionID))
	return "task/" + safePathPart(request.Scope.LocalTaskID) + "-" + hex.EncodeToString(sum[:])[:10]
}

func safePathPart(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, char := range strings.TrimSpace(value) {
		allowed := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-'
		if allowed {
			builder.WriteRune(char)
			lastDash = false
		} else if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-.")
	if result == "" {
		return "item"
	}
	if len(result) > 80 {
		return result[:80]
	}
	return result
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	if leftErr != nil {
		leftAbs = left
	}
	rightAbs, rightErr := filepath.Abs(right)
	if rightErr != nil {
		rightAbs = right
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
