package client

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/pkg/domain"
)

func TestNewPreservesExplicitConfigAndAppliesDefaults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	explicit := New(Config{
		HeartbeatEvery: time.Minute, BindingMode: domain.WorkerSpecificProjects,
		SupportedAgents: []domain.AgentType{domain.AgentClaude}, Logger: logger,
	})
	if explicit.config.HeartbeatEvery != time.Minute || explicit.config.BindingMode != domain.WorkerSpecificProjects || explicit.logger != logger || len(explicit.config.SupportedAgents) != 1 {
		t.Fatalf("显式配置被覆盖: %+v", explicit.config)
	}
	defaults := New(Config{})
	if defaults.config.HeartbeatEvery != 10*time.Second || defaults.config.BindingMode != domain.WorkerAllProjects || defaults.logger == nil || len(defaults.config.SupportedAgents) != 2 {
		t.Fatalf("默认配置错误: %+v", defaults.config)
	}
}

func TestClientURLAndConnectionErrorBranches(t *testing.T) {
	client := New(Config{WorkerID: "worker", Name: "Worker"})
	if _, err := client.frpURL("%gh&%ij"); err == nil {
		t.Fatal("非法 FRP URL 应失败")
	}
	rootURL, err := client.frpURL("ws://manager.example/")
	if err != nil || !strings.Contains(rootURL, "/worker/frp") || strings.Contains(rootURL, "token=") {
		t.Fatalf("根路径 FRP URL=%q err=%v", rootURL, err)
	}
	if err := client.connectAndServe(context.Background()); err == nil {
		t.Fatal("空 Manager URL 应失败")
	}
	if err := client.connectFRP(context.Background(), "%gh&%ij"); err == nil {
		t.Fatal("非法 FRP Manager URL 应失败")
	}
	if err := client.connectFRP(context.Background(), "ws://127.0.0.1:1/worker/ws"); err == nil {
		t.Fatal("不可达 FRP 应失败")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	retrying := New(Config{ManagerWSURL: "ws://127.0.0.1:1/worker/ws", WorkerID: "worker"})
	if err := retrying.runSingleManager(ctx); err == nil || ctx.Err() == nil {
		t.Fatalf("断线重试未随 context 停止: err=%v ctx=%v", err, ctx.Err())
	}
}

func TestReadAndHeartbeatLoopsReturnConnectionErrors(t *testing.T) {
	client := New(Config{WorkerID: "worker", HeartbeatEvery: time.Millisecond})
	if err := client.readLoop(context.Background()); err == nil {
		t.Fatal("断线 readLoop 应失败")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := client.heartbeatLoop(ctx); err == nil || strings.Contains(err.Error(), "deadline") {
		t.Fatalf("断线 heartbeat 应返回发送错误: %v", err)
	}
}
