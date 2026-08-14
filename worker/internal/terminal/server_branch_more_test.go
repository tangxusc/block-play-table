//go:build !windows

package terminal

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestServerExplicitConfigAndValidationBranches(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	server := NewServer(Config{Enabled: true, Host: " localhost ", WorkDir: root, Shell: " /bin/sh ", Logger: logger})
	if server.host != "localhost" || server.shell != "/bin/sh" || server.logger != logger {
		t.Fatalf("显式 Terminal 配置被覆盖: %+v", server)
	}
	if caps := server.Capabilities(); caps["terminal_enabled"] != "true" || caps["terminal_port"] != "" {
		t.Fatalf("未启动 capabilities=%v", caps)
	}
	if _, err := server.validateCwd(""); err == nil {
		t.Fatal("空 cwd 应失败")
	}
	missingRoot := NewServer(Config{Enabled: true, WorkDir: filepath.Join(root, "missing-root")})
	if _, err := missingRoot.validateCwd(root); err == nil {
		t.Fatal("不存在的 Worker work dir 应失败")
	}
	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := server.validateCwd(filePath); err == nil {
		t.Fatal("普通文件不应作为 cwd")
	}

	req := httptest.NewRequest(http.MethodGet, "/terminal/ws?cwd="+url.QueryEscape(root), nil)
	recorder := httptest.NewRecorder()
	server.handleTerminal(recorder, req)
	if recorder.Code == http.StatusSwitchingProtocols {
		t.Fatal("普通 HTTP 请求不应升级成功")
	}
}

func TestShellSessionDefaultsResizeWaitAndCloseBranches(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	session, err := startShellSession(shellConfig{Cwd: t.TempDir(), Rows: 12, Cols: 34})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Resize(0, 20); err != nil {
		t.Fatalf("非法 rows 应幂等忽略: %v", err)
	}
	if err := session.Resize(20, 0); err != nil {
		t.Fatalf("非法 cols 应幂等忽略: %v", err)
	}
	if err := session.Resize(20, 40); err != nil {
		t.Fatalf("合法 resize: %v", err)
	}
	if err := session.Close(); err != nil && !terminalReadClosed(err) {
		t.Fatalf("关闭 session: %v", err)
	}

	t.Setenv("SHELL", "")
	fallback, err := startShellSession(shellConfig{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_ = fallback.Close()
	if _, err := startShellSession(shellConfig{Cwd: t.TempDir(), Shell: "/missing/shell"}); err == nil {
		t.Fatal("不存在的 shell 应失败")
	}

	exitCommand := exec.Command("sh", "-c", "exit 7")
	if err := exitCommand.Start(); err != nil {
		t.Fatal(err)
	}
	if code := (&shellSession{cmd: exitCommand}).Wait(); code != 7 {
		t.Fatalf("退出码=%d", code)
	}
	if code := (&shellSession{cmd: &exec.Cmd{}}).Wait(); code != 1 {
		t.Fatalf("未启动命令退出码=%d", code)
	}
	if err := (&shellSession{}).Close(); err != nil {
		t.Fatalf("空 shellSession Close=%v", err)
	}
	if !terminalReadClosed(syscall.EIO) || terminalReadClosed(errors.New("other")) {
		t.Fatal("PTY 关闭错误识别失败")
	}
}

func TestTerminalWebSocketMalformedResizeAndCloseBranches(t *testing.T) {
	root := t.TempDir()
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1", Shell: "/bin/sh"})
	if err := server.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	u := url.URL{Scheme: "ws", Host: server.Addr(), Path: "/terminal/ws"}
	query := u.Query()
	query.Set("cwd", root)
	query.Set("rows", "10")
	query.Set("cols", "20")
	u.RawQuery = query.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not-json")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var response serverMessage
	if err := conn.ReadJSON(&response); err != nil || response.Type != "error" {
		t.Fatalf("畸形消息响应=%+v err=%v", response, err)
	}
	if err := conn.WriteJSON(clientMessage{Type: "resize", Rows: 30, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(clientMessage{Type: "resize", Rows: 0, Cols: 0}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(clientMessage{Type: "unknown"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(clientMessage{Type: "close"}); err != nil {
		t.Fatal(err)
	}
}

func TestStartRejectsInvalidListenHost(t *testing.T) {
	server := NewServer(Config{Enabled: true, WorkDir: t.TempDir(), Host: "bad host"})
	if err := server.Start(context.Background()); err == nil {
		t.Fatalf("非法监听 host 错误=%v", err)
	}
}
