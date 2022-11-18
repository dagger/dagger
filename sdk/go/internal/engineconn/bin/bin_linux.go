//go:build linux

package bin

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
