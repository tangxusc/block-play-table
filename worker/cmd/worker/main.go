package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/worker/internal/client"
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
		ManagerWSURL:    getenv("MANAGER_WS_URL", "ws://localhost:8080/worker/ws"),
		WorkerID:        getenv("WORKER_ID", "worker-local"),
		Name:            getenv("WORKER_NAME", "local-worker"),
		WorkDir:         getenv("WORKER_WORK_DIR", "./worker-data"),
		SupportedAgents: agents,
		Logger:          logger,
	}
	logger.Info("worker starting", "manager", cfg.ManagerWSURL, "workerId", cfg.WorkerID, "trustedMode", true)
	if err := client.New(cfg).Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
