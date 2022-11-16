//go:build !linux

package dockerprovision

import (
	"os/exec"
)

func setPlatformOpts(proc *exec.Cmd) {
	// no-op
}
