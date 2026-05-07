package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/worker/internal/client"
	"github.com/tangxusc/block-play-table/worker/internal/review"
	"github.com/tangxusc/block-play-table/worker/internal/terminal"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	agents := client.ParseAgents(getenv("WORKER_SUPPORTED_AGENTS", "codex,claude"))
	if len(agents) == 0 {
		agents = []domain.AgentType{domain.AgentCodex, domain.AgentClaude}
	}
	cfg := client.Config{
		ManagerWSURLs:   client.ParseManagerWSURLs(os.Getenv("MANAGER_WS_URLS"), getenv("MANAGER_WS_URL", "ws://localhost:8080/worker/ws")),
		WorkerID:        getenv("WORKER_ID", "worker-local"),
		WorkerToken:     os.Getenv("WORKER_TOKEN"),
		Name:            getenv("WORKER_NAME", "local-worker"),
		WorkDir:         getenv("WORKER_WORK_DIR", "./worker-data"),
		StartupCommand:  os.Getenv("WORKER_STARTUP_COMMAND"),
		SupportedAgents: agents,
		BindingMode:     client.ParseProjectBindingMode(os.Getenv("WORKER_PROJECT_BINDING_MODE")),
		BoundProjectIDs: client.ParseCSV(os.Getenv("WORKER_BOUND_PROJECT_IDS")),
		Logger:          logger,
	}
	terminalServer := terminal.NewServer(terminal.Config{
		Enabled: parseBoolEnv("WORKER_TERMINAL_ENABLED", true),
		Host:    getenv("WORKER_TERMINAL_HOST", "127.0.0.1"),
		WorkDir: cfg.WorkDir,
		Shell:   os.Getenv("WORKER_TERMINAL_SHELL"),
		Logger:  logger,
	})
	if err := terminalServer.Start(ctx); err != nil {
		logger.Error("worker terminal failed", "error", err)
		os.Exit(1)
	}
	reviewServer := review.NewServer(review.Config{
		Enabled: parseBoolEnv("WORKER_REVIEW_ENABLED", true),
		Host:    getenv("WORKER_REVIEW_HOST", "127.0.0.1"),
		WorkDir: cfg.WorkDir,
		Logger:  logger,
	})
	if err := reviewServer.Start(ctx); err != nil {
		logger.Error("worker review failed", "error", err)
		os.Exit(1)
	}
	cfg.ReviewRecorder = reviewRecorder{server: reviewServer}
	cfg.Capabilities = mergeCapabilities(terminalServer.Capabilities(), reviewServer.Capabilities())
	logger.Info("worker starting", "managers", strings.Join(cfg.ManagerWSURLs, ","), "workerId", cfg.WorkerID, "trustedMode", true, "terminal", cfg.Capabilities["terminal_enabled"], "review", cfg.Capabilities["review_enabled"])
	if err := client.New(cfg).Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

type reviewRecorder struct {
	server *review.Server
}

func (r reviewRecorder) BeginTurn(ctx context.Context, taskID, worktreePath, baseBranch, defaultBranch string) string {
	if r.server == nil {
		return ""
	}
	return r.server.BeginTurn(ctx, review.TaskContext{TaskID: taskID, WorktreePath: worktreePath, BaseBranch: baseBranch, DefaultBranch: defaultBranch})
}

func (r reviewRecorder) EndTurn(ctx context.Context, taskID, token string) {
	if r.server != nil {
		r.server.EndTurn(ctx, taskID, token)
	}
}

func mergeCapabilities(groups ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, group := range groups {
		for key, value := range group {
			out[key] = value
		}
	}
	return out
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func parseBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value != "0" && value != "false" && value != "no" && value != "off"
}
