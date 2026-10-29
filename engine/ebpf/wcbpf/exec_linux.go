package wcbpf

import (
	"os"

	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
)

// WatchExec starts measuring a profiled exec by every program a recording
// asks for, given the path of its resource cgroup, before any of its
// processes (mount helpers included) starts; init is whether the engine's
// init starts its workload. It creates the cgroup, which the mount helpers'
// accounting and the runtime adopt, so the programs know it before the
// exec's first process. Nil when no program is attached.
func WatchExec(resourceCgroup string, init bool) *Exec {
	if !asked() {
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
	return watchExec(ExecInfo{CgroupID: id, Init: init})
}

// KernelClock returns a conversion of times read by eBPF's bpf_ktime_get_ns,
// on the kernel's monotonic clock, to wcprof's clock, as wcprof.NowNS reads
// it. It's calibrated once, so converted times keep their kernel order and
// intervals. It converts 0 to 0, and every time to 0 when nothing is
// recorded.
func KernelClock() func(ns uint64) int64 {
	now := wcprof.NowNS()
	var ts unix.Timespec
	if now == 0 || unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts) != nil {
		return func(uint64) int64 { return 0 }
	}
	offset := now - ts.Nano()
	return func(ns uint64) int64 {
		if ns == 0 {
			return 0
		}
		return int64(ns) + offset
	}
}
