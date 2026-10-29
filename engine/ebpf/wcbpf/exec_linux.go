package wcbpf

import (
	"os"

	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
)

// WatchExec starts measuring a profiled exec by every attached program, given
// the path of its resource cgroup, before its runtime starts. It creates the
// cgroup when the runtime hasn't yet, as the runtime adopts an existing one,
// so the programs know it before the exec's first process. Nil when no
// program is attached.
func WatchExec(resourceCgroup string) *Exec {
	if !attached() {
		return nil
	}
	if err := os.Mkdir(resourceCgroup, 0o755); err != nil && !os.IsExist(err) {
		slog.Debug("wcbpf: exec not measured", "cgroup", resourceCgroup, "error", err)
		return nil
	}
	id, err := ebpfutil.CgroupID(resourceCgroup)
	if err != nil {
		slog.Debug("wcbpf: exec not measured", "cgroup", resourceCgroup, "error", err)
		return nil
	}
	return watchExec(id)
}

// RecorderNS converts a time read by eBPF's bpf_ktime_get_ns, on the kernel's
// monotonic clock, to wcprof's clock, as wcprof.NowNS reads it. 0 when ns is
// 0 or nothing is recorded.
func RecorderNS(ns uint64) int64 {
	now := wcprof.NowNS()
	if ns == 0 || now == 0 {
		return 0
	}
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0
	}
	return now - (ts.Nano() - int64(ns))
}
