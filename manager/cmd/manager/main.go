package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tangxusc/block-play-table/manager/internal/app"
	"github.com/tangxusc/block-play-table/manager/internal/httpapi"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	addr := getenv("MANAGER_HTTP_ADDR", ":8080")

	service := app.NewService(store.NewMemoryStore())
	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewServer(service).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("manager listening", "addr", addr, "trustedMode", true)
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

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
