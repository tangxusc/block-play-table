//go:build windows

package a2aadapter

import "os/exec"

func configureCommandForCancel(_ *exec.Cmd) {}

func killCommandProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
