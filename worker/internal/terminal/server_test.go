package terminal

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDisabledServerReportsTerminalDisabled(t *testing.T) {
	server := NewServer(Config{Enabled: false, WorkDir: t.TempDir()})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start disabled terminal server: %v", err)
	}
	if server.Addr() != "" {
		t.Fatalf("disabled terminal addr = %q, want empty", server.Addr())
	}
	if got := server.Capabilities()["terminal_enabled"]; got != "false" {
		t.Fatalf("terminal_enabled capability = %q, want false", got)
	}
}

func TestValidateCwdRequiresExistingDirectoryInsideWorkDir(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "task-worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{Enabled: true, WorkDir: root})
	expectedWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := server.validateCwd(worktree); err != nil || got != expectedWorktree {
		t.Fatalf("validate worktree = %q, %v; want %q", got, err, expectedWorktree)
	}
	if _, err := server.validateCwd(filepath.Dir(root)); err == nil || !strings.Contains(err.Error(), "within worker work dir") {
		t.Fatalf("outside work dir error = %v, want within worker work dir", err)
	}
	if _, err := server.validateCwd(filepath.Join(root, "missing")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing cwd error = %v, want does not exist", err)
	}
}

func TestTerminalCheckReportsMissingCwdBeforeWebSocketUpgrade(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "task-worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	if err := server.Start(ctx); err != nil {
		t.Fatalf("start terminal server: %v", err)
	}
	defer server.Close()

	validURL := url.URL{Scheme: "http", Host: server.Addr(), Path: "/terminal/check"}
	q := validURL.Query()
	q.Set("cwd", worktree)
	validURL.RawQuery = q.Encode()
	res, err := http.Get(validURL.String())
	if err != nil {
		t.Fatalf("check valid cwd: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("valid cwd status = %d, want 200", res.StatusCode)
	}

	missingURL := url.URL{Scheme: "http", Host: server.Addr(), Path: "/terminal/check"}
	q = missingURL.Query()
	q.Set("cwd", filepath.Join(root, "missing"))
	missingURL.RawQuery = q.Encode()
	res, err = http.Get(missingURL.String())
	if err != nil {
		t.Fatalf("check missing cwd: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "does not exist") {
		t.Fatalf("missing cwd response = %d %q, want 400 does not exist", res.StatusCode, body)
	}
}

func TestTerminalWebSocketRunsShellInRequestedCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty shell sessions are not supported on windows")
	}
	root := t.TempDir()
	worktree := filepath.Join(root, "task-worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(Config{Enabled: true, WorkDir: root, Host: "127.0.0.1"})
	if err := server.Start(ctx); err != nil {
		t.Fatalf("start terminal server: %v", err)
	}
	defer server.Close()

	u := url.URL{Scheme: "ws", Host: server.Addr(), Path: "/terminal/ws"}
	q := u.Query()
	q.Set("cwd", worktree)
	u.RawQuery = q.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial terminal websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "input", "data": "pwd\necho BPT_TERMINAL_TEST\nexit\n"}); err != nil {
		t.Fatalf("write terminal input: %v", err)
	}

	deadline := time.After(5 * time.Second)
	output := ""
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for terminal output; got %q", output)
		default:
		}
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read terminal message: %v", err)
		}
		switch message["type"] {
		case "output":
			output += message["data"].(string)
		case "exit":
			if !strings.Contains(output, worktree) || !strings.Contains(output, "BPT_TERMINAL_TEST") {
				t.Fatalf("terminal output = %q, want cwd %q and marker", output, worktree)
			}
			return
		case "error":
			t.Fatalf("terminal error: %v", message["data"])
		}
	}
}
