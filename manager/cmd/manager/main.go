package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/httpapi"
	"github.com/tangxusc/block-play-table/manager/migrations"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	addr := getenv("MANAGER_HTTP_ADDR", ":8080")
	heartbeatTimeout := getenvDuration("WORKER_HEARTBEAT_TIMEOUT", 90*time.Second)

	ctx := context.Background()
	st, closeStore, err := openStore(ctx)
	if err != nil {
		logger.Error("manager storage setup failed", "error", err)
		os.Exit(1)
	}
	defer closeStore()

	service := app.NewService(st, employeeProviderOption()...)
	if migrated, err := service.MigrateLegacyA2ATasks(ctx); err != nil {
		logger.Error("manager A2A legacy task migration failed", "error", err)
		os.Exit(1)
	} else if migrated > 0 {
		logger.Info("manager A2A legacy task migration completed", "tasks", migrated)
	}

	apiServer := httpapi.NewServer(
		service,
		httpapi.WithWorkerToken(os.Getenv("WORKER_TOKEN")),
		httpapi.WithTrustModeUserID(getenv("TRUST_MODE_USER_ID", "trust-mode-user-id")),
		httpapi.WithWorkerHeartbeatTimeout(heartbeatTimeout),
	)
	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	reconciler := app.NewReconciler(
		service,
		heartbeatTimeout,
		heartbeatTimeout/3,
		app.WithReconcilerLogger(logger),
		app.WithA2ATransport(apiServer.A2ATransport()),
	)
	go reconciler.Run(monitorCtx)
	server := &http.Server{
		Addr:              addr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("manager listening", "addr", addr, "trustedMode", true, "workerTokenEnabled", os.Getenv("WORKER_TOKEN") != "")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("manager failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func openStore(ctx context.Context) (store.Store, func(), error) {
	driver := strings.ToLower(strings.TrimSpace(getenv("DB_DRIVER", store.SQLDriverSQLite)))
	switch driver {
	case store.SQLDriverMemory:
		return store.NewMemoryStore(), func() {}, nil
	case store.SQLDriverSQLite, "sqlite3":
		dsn := getenv("DB_DSN", "./data/manager.db")
		sqlStore, err := store.OpenSQLStore(ctx, store.SQLDriverSQLite, dsn)
		if err != nil {
			return nil, nil, err
		}
		if err := sqlStore.MigrateVersioned(ctx, toStoreMigrations(migrations.All)); err != nil {
			_ = sqlStore.Close()
			return nil, nil, err
		}
		return sqlStore, func() { _ = sqlStore.Close() }, nil
	case store.SQLDriverPostgres, "postgresql", "pgx":
		dsn := strings.TrimSpace(os.Getenv("DB_DSN"))
		if dsn == "" {
			return nil, nil, errors.New("DB_DSN is required when DB_DRIVER=postgres")
		}
		sqlStore, err := store.OpenSQLStore(ctx, store.SQLDriverPostgres, dsn)
		if err != nil {
			return nil, nil, err
		}
		if err := sqlStore.MigrateVersioned(ctx, toStoreMigrations(migrations.All)); err != nil {
			_ = sqlStore.Close()
			return nil, nil, err
		}
		return sqlStore, func() { _ = sqlStore.Close() }, nil
	default:
		return nil, nil, errors.New("unsupported DB_DRIVER " + driver)
	}
}

func toStoreMigrations(items []migrations.Migration) []store.Migration {
	out := make([]store.Migration, 0, len(items))
	for _, item := range items {
		out = append(out, store.Migration{Version: item.Version, SQL: item.SQL})
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

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func employeeProviderOption() []app.Option {
	if url := strings.TrimSpace(os.Getenv("EMPLOYEE_SERVICE_URL")); url != "" {
		return []app.Option{app.WithEmployeeProvider(app.NewRemoteEmployeeProvider(url))}
	}
	return nil
}
