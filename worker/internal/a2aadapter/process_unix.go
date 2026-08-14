//go:build !windows

package a2aadapter

import (
	"os/exec"
	"syscall"
)

func configureCommandForCancel(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killCommandProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
