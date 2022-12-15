//go:build !linux

package engineconn

import (
	"os/exec"
)

func setPlatformOpts(proc *exec.Cmd) {
	// no-op
}
