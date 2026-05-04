//go:build !windows

package terminal

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type shellConfig struct {
	Cwd   string
	Shell string
	Rows  int
	Cols  int
}

type shellSession struct {
	cmd  *exec.Cmd
	file *os.File
}

func startShellSession(config shellConfig) (*shellSession, error) {
	shell := config.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "sh"
	}
	rows := config.Rows
	if rows <= 0 {
		rows = 24
	}
	cols := config.Cols
	if cols <= 0 {
		cols = 80
	}
	cmd := exec.Command(shell)
	cmd.Dir = config.Cwd
	cmd.Env = os.Environ()
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	return &shellSession{cmd: cmd, file: file}, nil
}

func (s *shellSession) Read(p []byte) (int, error) {
	return s.file.Read(p)
}

func (s *shellSession) Write(value string) error {
	_, err := s.file.Write([]byte(value))
	return err
}

func (s *shellSession) Resize(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return nil
	}
	return pty.Setsize(s.file, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (s *shellSession) Wait() int {
	err := s.cmd.Wait()
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func (s *shellSession) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func terminalReadClosed(err error) bool {
	return errors.Is(err, syscall.EIO)
}
