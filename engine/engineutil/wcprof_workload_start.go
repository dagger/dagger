package engineutil

import (
	"os"
)

// workloadReleasedNS returns when runc released the container's workload, as
// a wcprof timestamp, or 0 when that is unknown. It splits runc's container
// start-up from the user's process run in wcprof.
//
// The go-runc started callback fires when the runc process is forked, before
// runc has created the container. `runc run --pid-file` writes the pid file
// only after it has created the container and released the container's
// process to exec the workload (runc's utils_linux.go runner.run: the file is
// written after container.Run, which ends by unblocking the init through the
// exec fifo). The file's modification time, read once runc has exited, is
// placed relative to the started callback, whose wcprof and wall-clock times
// (startedNS, startedWallNS) were taken together, so only that short interval
// is measured on the wall clock. File times come from the kernel's coarse
// clock, so the result can be early by up to a clock tick (a few ms).
func workloadReleasedNS(pidFile string, startedNS, startedWallNS int64) int64 {
	if pidFile == "" || startedNS == 0 || startedWallNS == 0 {
		return 0
	}
	fi, err := os.Stat(pidFile)
	if err != nil {
		return 0
	}
	return startedNS + fi.ModTime().UnixNano() - startedWallNS
}
