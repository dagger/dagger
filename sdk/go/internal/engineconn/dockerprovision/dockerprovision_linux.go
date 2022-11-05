//go:build linux

package dockerprovision

import (
	"os/exec"
	"syscall"
)

func setPlatformOpts(proc *exec.Cmd) {
	if proc.SysProcAttr == nil {
		proc.SysProcAttr = &syscall.SysProcAttr{}
	}
	proc.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
