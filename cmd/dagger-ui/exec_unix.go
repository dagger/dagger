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

	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			return err
		}
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
