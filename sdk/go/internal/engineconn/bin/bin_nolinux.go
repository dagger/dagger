//go:build !linux

package bin

import "os/exec"

func setPlatformOpts(proc *exec.Cmd) {
	// no-op
}
