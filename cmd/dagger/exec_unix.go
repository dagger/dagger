//go:build unix
// +build unix

package main

import (
	"os/exec"
	"syscall"
)

func ensureChildProcessesAreKilled(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.Cancel = func() error {
		return syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = waitDelay
}
