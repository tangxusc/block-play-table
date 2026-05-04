//go:build windows

package terminal

import "fmt"

type shellConfig struct {
	Cwd   string
	Shell string
	Rows  int
	Cols  int
}

type shellSession struct{}

func startShellSession(shellConfig) (*shellSession, error) {
	return nil, fmt.Errorf("worker terminal is not supported on windows")
}

func (s *shellSession) Read([]byte) (int, error) {
	return 0, fmt.Errorf("worker terminal is not supported on windows")
}

func (s *shellSession) Write(string) error {
	return fmt.Errorf("worker terminal is not supported on windows")
}

func (s *shellSession) Resize(int, int) error {
	return nil
}

func (s *shellSession) Wait() int {
	return 1
}

func (s *shellSession) Close() error {
	return nil
}

func terminalReadClosed(error) bool {
	return true
}
