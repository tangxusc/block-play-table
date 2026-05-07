package review

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxDiffBytes     = 2 * 1024 * 1024
	defaultMaxFileDiffBytes = 500 * 1024
)

type DiffScope string

const (
	ScopeUncommitted DiffScope = "UNCOMMITTED"
	ScopeBranch      DiffScope = "BRANCH"
	ScopeLastTurn    DiffScope = "LAST_TURN"
)

type ReviewRunStatus string

const (
	ReviewRunQueued    ReviewRunStatus = "QUEUED"
	ReviewRunRunning   ReviewRunStatus = "RUNNING"
	ReviewRunCompleted ReviewRunStatus = "COMPLETED"
	ReviewRunFailed    ReviewRunStatus = "FAILED"
)

type FindingSeverity string

const (
	FindingSeverityInfo     FindingSeverity = "INFO"
	FindingSeverityLow      FindingSeverity = "LOW"
	FindingSeverityMedium   FindingSeverity = "MEDIUM"
	FindingSeverityHigh     FindingSeverity = "HIGH"
	FindingSeverityCritical FindingSeverity = "CRITICAL"
)

type FindingStatus string

const (
	FindingOpen      FindingStatus = "OPEN"
	FindingResolved  FindingStatus = "RESOLVED"
	FindingDismissed FindingStatus = "DISMISSED"
)

type Config struct {
	Enabled          bool
	Host             string
	WorkDir          string
	MaxDiffBytes     int
	MaxFileDiffBytes int
	Logger           *slog.Logger
}

type Server struct {
	enabled          bool
	host             string
	workDir          string
	maxDiffBytes     int
	maxFileDiffBytes int
	logger           *slog.Logger

	mu       sync.RWMutex
	addr     string
	port     int
	listener net.Listener
	server   *http.Server
	tasks    map[string]TaskContext
	pending  map[string]TaskGitTurnSnapshot
	backups  map[string]TaskGitBackup
}

type TaskContext struct {
	TaskID        string `json:"taskId"`
	WorktreePath  string `json:"worktreePath"`
	BaseBranch    string `json:"baseBranch,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	AgentType     string `json:"agentType,omitempty"`
	LastBefore    string `json:"lastBefore,omitempty"`
	LastAfter     string `json:"lastAfter,omitempty"`
}

type TaskGitTurnSnapshot struct {
	Token      string    `json:"token"`
	TaskID     string    `json:"taskId"`
	BeforeTree string    `json:"beforeTree"`
	AfterTree  string    `json:"afterTree"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type DiffResponse struct {
	TaskID      string     `json:"taskId"`
	Scope       DiffScope  `json:"scope"`
	BaseRef     string     `json:"baseRef,omitempty"`
	HeadRef     string     `json:"headRef,omitempty"`
	Files       []DiffFile `json:"files"`
	Truncated   bool       `json:"truncated"`
	GeneratedAt time.Time  `json:"generatedAt"`
}

type DiffFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Staged    bool   `json:"staged"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
}

type ActionRequest struct {
	WorktreePath string   `json:"worktreePath,omitempty"`
	BaseBranch   string   `json:"baseBranch,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	Patch        string   `json:"patch,omitempty"`
	BackupID     string   `json:"backupId,omitempty"`
}

type ActionResponse struct {
	OK     bool           `json:"ok"`
	Backup *TaskGitBackup `json:"backup,omitempty"`
	Diff   *DiffResponse  `json:"diff,omitempty"`
}

type ReviewRunRequest struct {
	Scope DiffScope `json:"scope"`
}

type ReviewRunResponse struct {
	Run      TaskReviewRun       `json:"run"`
	Findings []TaskReviewFinding `json:"findings"`
}

type TaskReviewRun struct {
	ID          string          `json:"id"`
	TaskID      string          `json:"taskId"`
	Scope       DiffScope       `json:"scope"`
	Status      ReviewRunStatus `json:"status"`
	AgentType   string          `json:"agentType,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	RawResult   string          `json:"rawResult,omitempty"`
	Error       string          `json:"error,omitempty"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	CompletedAt *time.Time      `json:"completedAt,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type TaskReviewFinding struct {
	ID         string          `json:"id"`
	RunID      string          `json:"runId"`
	TaskID     string          `json:"taskId"`
	Path       string          `json:"path"`
	Line       int             `json:"line"`
	Severity   FindingSeverity `json:"severity"`
	Status     FindingStatus   `json:"status"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	Suggestion string          `json:"suggestion,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

type TaskGitBackup struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Paths     []string  `json:"paths"`
	PatchPath string    `json:"patchPath"`
	CreatedAt time.Time `json:"createdAt"`
}

func NewServer(config Config) *Server {
	host := strings.TrimSpace(config.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if config.MaxDiffBytes <= 0 {
		config.MaxDiffBytes = defaultMaxDiffBytes
	}
	if config.MaxFileDiffBytes <= 0 {
		config.MaxFileDiffBytes = defaultMaxFileDiffBytes
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Server{
		enabled:          config.Enabled,
		host:             host,
		workDir:          config.WorkDir,
		maxDiffBytes:     config.MaxDiffBytes,
		maxFileDiffBytes: config.MaxFileDiffBytes,
		logger:           config.Logger,
		tasks:            map[string]TaskContext{},
		pending:          map[string]TaskGitTurnSnapshot{},
		backups:          map[string]TaskGitBackup{},
	}
}

func (s *Server) Start(ctx context.Context) error {
	if !s.enabled {
		return nil
	}
	if strings.TrimSpace(s.workDir) == "" {
		return fmt.Errorf("worker work dir is required")
	}
	if err := os.MkdirAll(s.workDir, 0o755); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.host, "0"))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	_, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.port = port
	s.listener = ln
	s.server = srv
	s.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed && s.logger != nil {
			s.logger.Warn("review server stopped", "error", err)
		}
	}()
	if ctx != nil {
		go func() {
			<-ctx.Done()
			_ = s.Close()
		}()
	}
	return nil
}

func (s *Server) Close() error {
	s.mu.RLock()
	srv := s.server
	s.mu.RUnlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

func (s *Server) Capabilities() map[string]string {
	out := map[string]string{"review_enabled": "false"}
	if !s.enabled {
		return out
	}
	out["review_enabled"] = "true"
	out["review_host"] = s.host
	if port := s.Port(); port > 0 {
		out["review_port"] = strconv.Itoa(port)
	}
	return out
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/review/tasks/", s.handleTaskReview)
	return mux
}

func (s *Server) RegisterTask(task TaskContext) {
	if strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(task.WorktreePath) == "" {
		return
	}
	s.mu.Lock()
	existing := s.tasks[task.TaskID]
	if task.BaseBranch == "" {
		task.BaseBranch = existing.BaseBranch
	}
	if task.DefaultBranch == "" {
		task.DefaultBranch = existing.DefaultBranch
	}
	if task.AgentType == "" {
		task.AgentType = existing.AgentType
	}
	task.LastBefore = existing.LastBefore
	task.LastAfter = existing.LastAfter
	s.tasks[task.TaskID] = task
	s.mu.Unlock()
}

func (s *Server) BeginTurn(ctx context.Context, task TaskContext) string {
	s.RegisterTask(task)
	token := fmt.Sprintf("turn_%d", time.Now().UnixNano())
	worktree, err := s.validateWorktree(task.WorktreePath)
	before := ""
	if err == nil {
		before, _ = snapshotTree(ctx, worktree)
	}
	now := time.Now().UTC()
	s.mu.Lock()
	s.pending[token] = TaskGitTurnSnapshot{Token: token, TaskID: task.TaskID, BeforeTree: before, CreatedAt: now, UpdatedAt: now}
	s.mu.Unlock()
	return token
}

func (s *Server) EndTurn(ctx context.Context, taskID, token string) {
	s.mu.RLock()
	pending := s.pending[token]
	task := s.tasks[taskID]
	s.mu.RUnlock()
	if pending.TaskID == "" || task.WorktreePath == "" {
		return
	}
	worktree, err := s.validateWorktree(task.WorktreePath)
	after := ""
	if err == nil {
		after, _ = snapshotTree(ctx, worktree)
	}
	now := time.Now().UTC()
	pending.AfterTree = after
	pending.UpdatedAt = now
	s.mu.Lock()
	delete(s.pending, token)
	task.LastBefore = pending.BeforeTree
	task.LastAfter = pending.AfterTree
	s.tasks[taskID] = task
	s.mu.Unlock()
}

func (s *Server) handleTaskReview(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/review/tasks/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	taskID := parts[0]
	action := parts[1]
	switch {
	case r.Method == http.MethodGet && action == "diff":
		s.handleDiff(w, r, taskID)
	case r.Method == http.MethodPost && action == "runs":
		s.handleRun(w, r, taskID)
	case r.Method == http.MethodPost && (action == "stage" || action == "unstage" || action == "discard" || action == "restore"):
		s.handleAction(w, r, taskID, action)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request, taskID string) {
	scope := DiffScope(strings.TrimSpace(r.URL.Query().Get("scope")))
	if scope == "" {
		scope = ScopeUncommitted
	}
	var staged *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("staged")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			http.Error(w, "staged must be true or false", http.StatusBadRequest)
			return
		}
		staged = &value
	}
	task, err := s.resolveTask(taskID, r.URL.Query().Get("cwd"), r.URL.Query().Get("baseBranch"), r.URL.Query().Get("defaultBranch"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	diff, err := s.diff(r.Context(), task, scope, staged)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not available") {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request, taskID, action string) {
	var input ActionRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cwd := firstNonEmpty(input.WorktreePath, r.URL.Query().Get("cwd"))
	baseBranch := firstNonEmpty(input.BaseBranch, r.URL.Query().Get("baseBranch"))
	task, err := s.resolveTask(taskID, cwd, baseBranch, r.URL.Query().Get("defaultBranch"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var response ActionResponse
	switch action {
	case "stage":
		err = s.stage(r.Context(), task, input)
	case "unstage":
		err = s.unstage(r.Context(), task, input)
	case "discard":
		response.Backup, err = s.discard(r.Context(), task, input)
	case "restore":
		err = s.restore(r.Context(), task, input)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	response.OK = true
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request, taskID string) {
	var input ReviewRunRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if input.Scope == "" {
		input.Scope = ScopeUncommitted
	}
	task, err := s.resolveTask(taskID, r.URL.Query().Get("cwd"), r.URL.Query().Get("baseBranch"), r.URL.Query().Get("defaultBranch"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	response, err := s.reviewRun(r.Context(), task, input.Scope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) resolveTask(taskID, cwd, baseBranch, defaultBranch string) (TaskContext, error) {
	s.mu.RLock()
	task := s.tasks[taskID]
	s.mu.RUnlock()
	task.TaskID = taskID
	if strings.TrimSpace(cwd) != "" {
		task.WorktreePath = cwd
	}
	if strings.TrimSpace(baseBranch) != "" {
		task.BaseBranch = baseBranch
	}
	if strings.TrimSpace(defaultBranch) != "" {
		task.DefaultBranch = defaultBranch
	}
	if strings.TrimSpace(task.WorktreePath) == "" {
		return TaskContext{}, fmt.Errorf("review worktree is required")
	}
	worktree, err := s.validateWorktree(task.WorktreePath)
	if err != nil {
		return TaskContext{}, err
	}
	task.WorktreePath = worktree
	s.RegisterTask(task)
	return task, nil
}

func (s *Server) validateWorktree(path string) (string, error) {
	workDir, err := filepath.Abs(s.workDir)
	if err != nil {
		return "", err
	}
	workDir, err = filepath.EvalSymlinks(workDir)
	if err != nil {
		return "", fmt.Errorf("worker work dir does not exist: %w", err)
	}
	candidate, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("review worktree does not exist: %w", err)
	}
	if !stat.IsDir() {
		return "", fmt.Errorf("review worktree is not a directory")
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(workDir, candidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("review worktree must be within worker work dir")
	}
	return candidate, nil
}

func (s *Server) diff(ctx context.Context, task TaskContext, scope DiffScope, staged *bool) (DiffResponse, error) {
	response := DiffResponse{TaskID: task.TaskID, Scope: scope, GeneratedAt: time.Now().UTC()}
	switch scope {
	case ScopeUncommitted:
		files, err := s.uncommittedDiff(ctx, task.WorktreePath, staged)
		response.Files = files
		response.Truncated = truncateDiffFiles(response.Files, s.maxDiffBytes, s.maxFileDiffBytes)
		return response, err
	case ScopeBranch:
		base, err := mergeBase(ctx, task.WorktreePath, firstNonEmpty(task.BaseBranch, task.DefaultBranch, "HEAD"))
		if err != nil {
			return response, err
		}
		tree, err := snapshotTree(ctx, task.WorktreePath)
		if err != nil {
			return response, err
		}
		files, err := treeDiff(ctx, task.WorktreePath, base, tree, false)
		response.BaseRef = base
		response.HeadRef = tree
		response.Files = files
		response.Truncated = truncateDiffFiles(response.Files, s.maxDiffBytes, s.maxFileDiffBytes)
		return response, err
	case ScopeLastTurn:
		s.mu.RLock()
		current := s.tasks[task.TaskID]
		s.mu.RUnlock()
		if current.LastBefore == "" || current.LastAfter == "" {
			return response, fmt.Errorf("last-turn diff is not available")
		}
		files, err := treeDiff(ctx, task.WorktreePath, current.LastBefore, current.LastAfter, false)
		response.BaseRef = current.LastBefore
		response.HeadRef = current.LastAfter
		response.Files = files
		response.Truncated = truncateDiffFiles(response.Files, s.maxDiffBytes, s.maxFileDiffBytes)
		return response, err
	default:
		return response, fmt.Errorf("unsupported diff scope %q", scope)
	}
}

func (s *Server) reviewRun(ctx context.Context, task TaskContext, scope DiffScope) (ReviewRunResponse, error) {
	startedAt := time.Now().UTC()
	diff, err := s.diff(ctx, task, scope, nil)
	completedAt := time.Now().UTC()
	run := TaskReviewRun{
		ID:          fmt.Sprintf("review_run_%d", startedAt.UnixNano()),
		TaskID:      task.TaskID,
		Scope:       scope,
		Status:      ReviewRunCompleted,
		AgentType:   task.AgentType,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		CreatedAt:   startedAt,
		UpdatedAt:   completedAt,
	}
	if err != nil {
		run.Status = ReviewRunFailed
		run.Error = err.Error()
		raw, _ := json.Marshal(map[string]any{"summary": "", "findings": []TaskReviewFinding{}, "error": run.Error})
		run.RawResult = string(raw)
		return ReviewRunResponse{Run: run}, nil
	}
	findings := reviewFindingsFromDiff(task.TaskID, run.ID, diff.Files, completedAt)
	run.Summary = fmt.Sprintf("%d finding(s)", len(findings))
	raw, err := json.Marshal(map[string]any{"summary": run.Summary, "findings": findings})
	if err != nil {
		run.Status = ReviewRunFailed
		run.Error = err.Error()
	} else {
		run.RawResult = string(raw)
	}
	return ReviewRunResponse{Run: run, Findings: findings}, nil
}

func reviewFindingsFromDiff(taskID, runID string, files []DiffFile, now time.Time) []TaskReviewFinding {
	findings := make([]TaskReviewFinding, 0, len(files))
	for index, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			continue
		}
		line := firstAddedLine(file.Patch)
		findings = append(findings, TaskReviewFinding{
			ID:         fmt.Sprintf("%s_finding_%d", runID, index+1),
			RunID:      runID,
			TaskID:     taskID,
			Path:       file.Path,
			Line:       line,
			Severity:   FindingSeverityMedium,
			Status:     FindingOpen,
			Title:      "Review changed file",
			Body:       "Review this changed file for correctness, regressions, and missing tests before merging.",
			Suggestion: "Address any issue found here, or resolve the finding if the change is intentional.",
			CreatedAt:  now,
			UpdatedAt:  now,
		})
	}
	return findings
}

func firstAddedLine(patch string) int {
	current := 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@") {
			plus := strings.Index(line, "+")
			if plus >= 0 {
				end := strings.IndexAny(line[plus+1:], " ,")
				value := line[plus+1:]
				if end >= 0 {
					value = line[plus+1 : plus+1+end]
				}
				parsed, err := strconv.Atoi(strings.TrimSpace(value))
				if err == nil {
					current = parsed
				}
			}
			continue
		}
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			if current > 0 {
				return current
			}
			return 1
		}
		if current > 0 && !strings.HasPrefix(line, "-") {
			current++
		}
	}
	return 0
}

func (s *Server) uncommittedDiff(ctx context.Context, dir string, stagedFilter *bool) ([]DiffFile, error) {
	var out []DiffFile
	if stagedFilter == nil || *stagedFilter {
		staged, err := worktreeDiff(ctx, dir, []string{"diff", "--cached"}, true)
		if err != nil {
			return nil, err
		}
		out = append(out, staged...)
	}
	if stagedFilter == nil || !*stagedFilter {
		unstaged, err := worktreeDiff(ctx, dir, []string{"diff"}, false)
		if err != nil {
			return nil, err
		}
		out = append(out, unstaged...)
		untracked, err := untrackedDiff(ctx, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, untracked...)
	}
	return out, nil
}

func (s *Server) stage(ctx context.Context, task TaskContext, input ActionRequest) error {
	if strings.TrimSpace(input.Patch) != "" {
		if err := validatePatchOperationPaths(task.WorktreePath, input.Paths, input.Patch); err != nil {
			return err
		}
		return applyPatch(ctx, task.WorktreePath, input.Patch, "--cached")
	}
	paths, err := cleanPaths(task.WorktreePath, input.Paths)
	if err != nil {
		return err
	}
	return runGit(ctx, task.WorktreePath, nil, append([]string{"add", "--"}, paths...)...)
}

func (s *Server) unstage(ctx context.Context, task TaskContext, input ActionRequest) error {
	if strings.TrimSpace(input.Patch) != "" {
		if err := validatePatchOperationPaths(task.WorktreePath, input.Paths, input.Patch); err != nil {
			return err
		}
		return applyPatch(ctx, task.WorktreePath, input.Patch, "--cached", "--reverse")
	}
	paths, err := cleanPaths(task.WorktreePath, input.Paths)
	if err != nil {
		return err
	}
	return runGit(ctx, task.WorktreePath, nil, append([]string{"restore", "--staged", "--"}, paths...)...)
}

func (s *Server) discard(ctx context.Context, task TaskContext, input ActionRequest) (*TaskGitBackup, error) {
	paths, err := cleanPaths(task.WorktreePath, input.Paths)
	if err != nil {
		return nil, err
	}
	backup, err := s.createBackup(ctx, task, paths, input.Patch)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Patch) != "" {
		if err := applyPatch(ctx, task.WorktreePath, input.Patch, "--reverse"); err != nil {
			return nil, err
		}
		return backup, nil
	}
	for _, path := range paths {
		if tracked(ctx, task.WorktreePath, path) {
			_ = runGit(ctx, task.WorktreePath, nil, "restore", "--staged", "--", path)
			if err := runGit(ctx, task.WorktreePath, nil, "restore", "--worktree", "--", path); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.RemoveAll(filepath.Join(task.WorktreePath, path)); err != nil {
			return nil, err
		}
	}
	return backup, nil
}

func (s *Server) restore(ctx context.Context, task TaskContext, input ActionRequest) error {
	if strings.TrimSpace(input.BackupID) == "" {
		return fmt.Errorf("backup id is required")
	}
	s.mu.RLock()
	backup := s.backups[input.BackupID]
	s.mu.RUnlock()
	if backup.ID == "" || backup.TaskID != task.TaskID {
		return fmt.Errorf("backup %s was not found for task %s", input.BackupID, task.TaskID)
	}
	raw, err := os.ReadFile(backup.PatchPath)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	return applyPatch(ctx, task.WorktreePath, string(raw))
}

func (s *Server) createBackup(ctx context.Context, task TaskContext, paths []string, patch string) (*TaskGitBackup, error) {
	id := fmt.Sprintf("backup_%d", time.Now().UnixNano())
	if strings.TrimSpace(patch) == "" {
		var builder strings.Builder
		for _, path := range paths {
			if tracked(ctx, task.WorktreePath, path) {
				if out, err := gitOutput(ctx, task.WorktreePath, nil, "diff", "--binary", "--cached", "--", path); err == nil {
					builder.WriteString(out)
				}
				if out, err := gitOutput(ctx, task.WorktreePath, nil, "diff", "--binary", "--", path); err == nil {
					builder.WriteString(out)
				}
			} else if out, err := noIndexPatch(ctx, task.WorktreePath, path); err == nil {
				builder.WriteString(out)
			}
		}
		patch = builder.String()
	}
	dir := filepath.Join(s.workDir, ".review-backups", task.TaskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	patchPath := filepath.Join(dir, id+".patch")
	if err := os.WriteFile(patchPath, []byte(patch), 0o600); err != nil {
		return nil, err
	}
	backup := TaskGitBackup{ID: id, TaskID: task.TaskID, Paths: append([]string(nil), paths...), PatchPath: patchPath, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	s.backups[id] = backup
	s.mu.Unlock()
	return &backup, nil
}

func worktreeDiff(ctx context.Context, dir string, baseArgs []string, staged bool) ([]DiffFile, error) {
	nameArgs := append(append([]string(nil), baseArgs...), "--name-status", "--")
	raw, err := gitOutput(ctx, dir, nil, nameArgs...)
	if err != nil {
		return nil, err
	}
	var out []DiffFile
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		status, path, oldPath := parseNameStatus(line)
		patchArgs := append(append([]string(nil), baseArgs...), "--", path)
		patch, err := gitOutput(ctx, dir, nil, patchArgs...)
		if err != nil {
			return nil, err
		}
		add, del := patchStats(patch)
		out = append(out, DiffFile{Path: path, OldPath: oldPath, Status: status, Staged: staged, Patch: patch, Additions: add, Deletions: del})
	}
	return out, nil
}

func treeDiff(ctx context.Context, dir, base, head string, staged bool) ([]DiffFile, error) {
	raw, err := gitOutput(ctx, dir, nil, "diff", "--name-status", base, head, "--")
	if err != nil {
		return nil, err
	}
	var out []DiffFile
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		status, path, oldPath := parseNameStatus(line)
		patch, err := gitOutput(ctx, dir, nil, "diff", base, head, "--", path)
		if err != nil {
			return nil, err
		}
		add, del := patchStats(patch)
		out = append(out, DiffFile{Path: path, OldPath: oldPath, Status: status, Staged: staged, Patch: patch, Additions: add, Deletions: del})
	}
	return out, nil
}

func untrackedDiff(ctx context.Context, dir string) ([]DiffFile, error) {
	raw, err := gitOutput(ctx, dir, nil, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var out []DiffFile
	for _, path := range strings.Split(strings.TrimSpace(raw), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		patch, err := noIndexPatch(ctx, dir, path)
		if err != nil {
			return nil, err
		}
		add, del := patchStats(patch)
		out = append(out, DiffFile{Path: path, Status: "A", Patch: patch, Additions: add, Deletions: del})
	}
	return out, nil
}

func snapshotTree(ctx context.Context, dir string) (string, error) {
	tmp, err := os.CreateTemp("", "bpt-review-index-*")
	if err != nil {
		return "", err
	}
	indexPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)
	env := map[string]string{"GIT_INDEX_FILE": indexPath}
	if err := runGit(ctx, dir, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if err := runGit(ctx, dir, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	tree, err := gitOutput(ctx, dir, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(tree), nil
}

func mergeBase(ctx context.Context, dir, base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "HEAD"
	}
	if runGit(ctx, dir, nil, "rev-parse", "--verify", base+"^{commit}") != nil {
		remote := "origin/" + strings.TrimPrefix(base, "origin/")
		if runGit(ctx, dir, nil, "rev-parse", "--verify", remote+"^{commit}") == nil {
			base = remote
		}
	}
	out, err := gitOutput(ctx, dir, nil, "merge-base", "HEAD", base)
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out), nil
	}
	return base, nil
}

func noIndexPatch(ctx context.Context, dir, path string) (string, error) {
	out, err := gitOutputAllowExit(ctx, dir, nil, []int{0, 1}, "diff", "--no-index", "--", "/dev/null", path)
	if err != nil {
		return "", err
	}
	return out, nil
}

func applyPatch(ctx context.Context, dir, patch string, args ...string) error {
	tmp, err := os.CreateTemp("", "bpt-review-patch-*.diff")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(patch); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	fullArgs := append([]string{"apply", "--whitespace=nowarn"}, args...)
	fullArgs = append(fullArgs, tmp.Name())
	return runGit(ctx, dir, nil, fullArgs...)
}

func cleanPaths(dir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one path is required")
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, ".."+string(filepath.Separator)) || path == ".." {
			return nil, fmt.Errorf("review path %q is invalid", path)
		}
		full := filepath.Join(dir, path)
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return nil, err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("review path %q must be within worktree", path)
		}
		out = append(out, filepath.ToSlash(path))
	}
	return out, nil
}

func validatePatchOperationPaths(dir string, paths []string, patch string) error {
	candidates := append([]string(nil), paths...)
	candidates = append(candidates, patchPaths(patch)...)
	_, err := cleanPaths(dir, uniqueStrings(candidates))
	return err
}

func patchPaths(patch string) []string {
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				paths = append(paths, trimPatchPath(parts[2]))
				paths = append(paths, trimPatchPath(parts[3]))
			}
		case strings.HasPrefix(line, "+++ "):
			paths = append(paths, trimPatchPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ "))))
		case strings.HasPrefix(line, "--- "):
			paths = append(paths, trimPatchPath(strings.TrimSpace(strings.TrimPrefix(line, "--- "))))
		}
	}
	return uniqueStrings(paths)
}

func trimPatchPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return path
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func tracked(ctx context.Context, dir, path string) bool {
	return runGit(ctx, dir, nil, "ls-files", "--error-unmatch", "--", path) == nil
}

func gitOutput(ctx context.Context, dir string, env map[string]string, args ...string) (string, error) {
	return gitOutputAllowExit(ctx, dir, env, []int{0}, args...)
}

func gitOutputAllowExit(ctx context.Context, dir string, env map[string]string, allowed []int, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), nil
	}
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		for _, code := range allowed {
			if exitErr.ExitCode() == code {
				return string(out), nil
			}
		}
	}
	return string(out), fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, out)
}

func runGit(ctx context.Context, dir string, env map[string]string, args ...string) error {
	_, err := gitOutput(ctx, dir, env, args...)
	return err
}

func mergeEnv(values map[string]string) []string {
	env := os.Environ()
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func parseNameStatus(line string) (status, path, oldPath string) {
	parts := strings.Split(line, "\t")
	if len(parts) == 0 {
		return "M", strings.TrimSpace(line), ""
	}
	status = parts[0]
	if strings.HasPrefix(status, "R") && len(parts) >= 3 {
		return "R", parts[2], parts[1]
	}
	if len(parts) >= 2 {
		return string(status[0]), parts[1], ""
	}
	return string(status[0]), "", ""
}

func patchStats(patch string) (int, int) {
	additions := 0
	deletions := 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			additions++
		} else if strings.HasPrefix(line, "-") {
			deletions++
		}
	}
	return additions, deletions
}

func truncateDiffFiles(files []DiffFile, maxTotal, maxFile int) bool {
	total := 0
	truncated := false
	for index := range files {
		if len(files[index].Patch) > maxFile {
			files[index].Patch = files[index].Patch[:maxFile]
			files[index].Truncated = true
			truncated = true
		}
		total += len(files[index].Patch)
		if total > maxTotal {
			files[index].Patch = ""
			files[index].Truncated = true
			truncated = true
		}
	}
	return truncated
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (d DiffResponse) File(path string) *DiffFile {
	for index := range d.Files {
		if d.Files[index].Path == path {
			return &d.Files[index]
		}
	}
	return nil
}

func (d DiffResponse) HasFile(path string) bool {
	return d.File(path) != nil
}
