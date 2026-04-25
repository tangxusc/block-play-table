//go:build windows

package executor

import "os/exec"

func configureCommandForCancel(cmd *exec.Cmd) {}

func killCommandProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
