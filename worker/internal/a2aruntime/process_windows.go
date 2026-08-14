//go:build windows

package a2aruntime

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func killProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
