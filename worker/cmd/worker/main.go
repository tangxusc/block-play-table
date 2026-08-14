package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/pkg/domain"
	"github.com/tangxusc/block-play-table/worker/internal/a2aadapter"
	"github.com/tangxusc/block-play-table/worker/internal/a2aruntime"
	"github.com/tangxusc/block-play-table/worker/internal/a2aserver"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
	"github.com/tangxusc/block-play-table/worker/internal/client"
	"github.com/tangxusc/block-play-table/worker/internal/review"
	"github.com/tangxusc/block-play-table/worker/internal/terminal"
)

const (
	a2aRetention          = 90 * 24 * time.Hour
	a2aCompactionInterval = 24 * time.Hour
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	if ctx == nil || logger == nil {
		return errors.New("Worker 缺少 context 或 logger")
	}
	if err := rejectLegacyManagerList(); err != nil {
		return err
	}
	workerID := getenv("WORKER_ID", "worker-local")
	workDir := getenv("WORKER_WORK_DIR", "./worker-data")
	managerURL := getenv("MANAGER_WS_URL", "ws://localhost:8080/worker/ws")
	workerToken := os.Getenv("WORKER_TOKEN")

	databasePath := getenv("WORKER_A2A_DB_PATH", filepath.Join(workDir, "a2a.db"))
	store, err := a2astore.Open(ctx, a2astore.Config{
		Path: databasePath, Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})
	if err != nil {
		return err
	}
	defer store.Close()
	storeCtx := authenticatedStoreContext(ctx, workerID)
	recovery, err := store.RecoverStartup(storeCtx)
	if err != nil {
		return fmt.Errorf("清理 Worker A2A 启动状态: %w", err)
	}
	failedTasks, err := store.FailNonTerminal(storeCtx)
	if err != nil {
		return fmt.Errorf("恢复 Worker 未终态 Task: %w", err)
	}
	compaction, err := store.CompactBefore(storeCtx, time.Now().UTC().Add(-a2aRetention))
	if err != nil {
		return fmt.Errorf("压缩 Worker A2A 历史: %w", err)
	}
	logger.Info("worker A2A store recovered",
		"database", databasePath, "pendingCommands", recovery.Commands, "orphanBindings", recovery.Bindings,
		"failedTasks", failedTasks, "compactedTasks", compaction.Tasks, "compactedEvents", compaction.Events,
		"compactedCommands", compaction.Commands,
	)
	adapters, versions, err := probeRequiredAdapters(ctx)
	if err != nil {
		return err
	}
	go runA2ACompactionLoop(storeCtx, a2aCompactionInterval, a2aRetention, time.Now, store.CompactBefore, logger)

	terminalServer := terminal.NewServer(terminal.Config{
		Enabled: parseBoolEnv("WORKER_TERMINAL_ENABLED", true), Host: getenv("WORKER_TERMINAL_HOST", "127.0.0.1"),
		WorkDir: workDir, Shell: os.Getenv("WORKER_TERMINAL_SHELL"), Logger: logger,
	})
	if err := terminalServer.Start(ctx); err != nil {
		return fmt.Errorf("启动 Worker Terminal: %w", err)
	}
	reviewServer := review.NewServer(review.Config{
		Enabled: parseBoolEnv("WORKER_REVIEW_ENABLED", true), Host: getenv("WORKER_REVIEW_HOST", "127.0.0.1"),
		WorkDir: workDir, Logger: logger,
	})
	if err := reviewServer.Start(ctx); err != nil {
		return fmt.Errorf("启动 Worker Review: %w", err)
	}
	runtime, err := a2aruntime.New(a2aruntime.Config{
		Context: ctx, WorkerID: workerID, WorkDir: workDir, Store: store,
		Adapters: adapters, Review: reviewRecorder{server: reviewServer},
	})
	if err != nil {
		return err
	}
	executor := a2aserver.NewAgentExecutor(workerID, store, runtime)
	port, err := parsePortEnv("WORKER_A2A_PORT", 0)
	if err != nil {
		return err
	}
	a2aServer := a2aserver.New(a2aserver.Config{
		Host: getenv("WORKER_A2A_HOST", "127.0.0.1"), Port: port, WorkerID: workerID, WorkerToken: workerToken,
		AgentTypes: []a2aext.AgentType{a2aext.AgentCodex, a2aext.AgentClaude}, AgentExecutor: executor, TaskStore: store, Logger: logger,
	})
	if err := a2aServer.Start(ctx); err != nil {
		return err
	}
	defer a2aServer.Close()

	capabilities := mergeCapabilities(terminalServer.Capabilities(), reviewServer.Capabilities(), a2aServer.Capabilities(), versions)
	workerClient := client.New(client.Config{
		ManagerWSURL: managerURL, WorkerID: workerID, WorkerToken: workerToken,
		Name: getenv("WORKER_NAME", "local-worker"), WorkDir: workDir, StartupCommand: os.Getenv("WORKER_STARTUP_COMMAND"),
		SupportedAgents: []domain.AgentType{domain.AgentCodex, domain.AgentClaude},
		BindingMode:     client.ParseProjectBindingMode(os.Getenv("WORKER_PROJECT_BINDING_MODE")),
		BoundProjectIDs: client.ParseCSV(os.Getenv("WORKER_BOUND_PROJECT_IDS")), Capabilities: capabilities, Logger: logger,
	})
	logger.Info("worker starting", "manager", managerURL, "workerId", workerID, "trustedMode", true,
		"terminal", capabilities["terminal_enabled"], "review", capabilities["review_enabled"],
		"codexVersion", versions["codex_version"], "claudeVersion", versions["claude_version"],
	)
	clientErrors := make(chan error, 1)
	go func() { clientErrors <- workerClient.Run(ctx) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-clientErrors:
		return err
	case err := <-a2aServer.Errors():
		return fmt.Errorf("Worker A2A Server 异常退出: %w", err)
	}
}

type compactBeforeFunc func(context.Context, time.Time) (a2astore.CompactionStats, error)

func runA2ACompactionLoop(ctx context.Context, interval, retention time.Duration, now func() time.Time, compact compactBeforeFunc, logger *slog.Logger) {
	if ctx == nil || interval <= 0 || retention <= 0 || now == nil || compact == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := compact(ctx, now().UTC().Add(-retention))
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					logger.Warn("worker A2A compaction failed", "error", err)
				}
				continue
			}
			if stats != (a2astore.CompactionStats{}) {
				logger.Info("worker A2A history compacted", "tasks", stats.Tasks, "events", stats.Events, "commands", stats.Commands)
			}
		}
	}
}

func probeRequiredAdapters(ctx context.Context) (map[a2aext.AgentType]a2aadapter.Adapter, map[string]string, error) {
	candidates := []struct {
		typeID a2aext.AgentType
		name   string
		value  a2aadapter.Adapter
	}{
		{typeID: a2aext.AgentCodex, name: "codex", value: a2aadapter.NewCodex(getenv("CODEX_BINARY", "codex"))},
		{typeID: a2aext.AgentClaude, name: "claude", value: a2aadapter.NewClaude(getenv("CLAUDE_BINARY", "claude"))},
	}
	adapters := make(map[a2aext.AgentType]a2aadapter.Adapter, len(candidates))
	versions := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		info, err := candidate.value.Probe(probeCtx)
		cancel()
		if err != nil {
			return nil, nil, fmt.Errorf("%s Adapter readiness 失败: %w", candidate.name, err)
		}
		adapters[candidate.typeID] = candidate.value
		versions[candidate.name+"_version"] = info.Version
	}
	return adapters, versions, nil
}

func authenticatedStoreContext(ctx context.Context, workerID string) context.Context {
	storeCtx, callCtx := a2asrv.NewCallContext(ctx, a2asrv.NewServiceParams(nil))
	callCtx.User = a2asrv.NewAuthenticatedUser("manager@"+workerID, map[string]any{"startupRecovery": true})
	return storeCtx
}

func rejectLegacyManagerList() error {
	key := strings.Join([]string{"MANAGER", "WS", "URLS"}, "_")
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return fmt.Errorf("%s 已移除；每个 Worker 只能配置 MANAGER_WS_URL", key)
	}
	return nil
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

func parsePortEnv(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("%s 必须是 0 到 65535 的整数", key)
	}
	return port, nil
}
