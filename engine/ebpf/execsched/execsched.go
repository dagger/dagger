//go:build linux && (amd64 || arm64)

package execsched

import (
	"context"
	"errors"
	"fmt"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"

	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
	"github.com/dagger/dagger/engine/ebpf/wcbpf"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_x86 -I../bpf" -target amd64 -type exec_sched execsched ./bpf/execsched.bpf.c
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_arm64 -I../bpf" -target arm64 -type exec_sched execsched ./bpf/execsched.bpf.c

// Program is exec_sched, registered at engine start.
var Program = wcbpf.Program{Name: Name, Load: load}

type loaded struct {
	objs  execschedObjects
	links []link.Link
}

func load() (_ wcbpf.Loaded, rerr error) {
	if err := ebpfutil.Prepare(); err != nil {
		return nil, err
	}
	l := &loaded{}
	if err := loadExecschedObjects(&l.objs, nil); err != nil {
		return nil, ebpfutil.WrapVerifierError(err, "loading exec_sched")
	}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, l.Close())
		}
	}()
	for _, prog := range []*ebpf.Program{
		l.objs.OnWakeup, l.objs.OnWakeupNew, l.objs.OnSwitch,
		l.objs.OnFork, l.objs.OnExec, l.objs.OnExit,
	} {
		lk, err := link.AttachTracing(link.TracingOptions{Program: prog})
		if err != nil {
			return nil, fmt.Errorf("attaching %s: %w", prog, err)
		}
		l.links = append(l.links, lk)
	}
	return l, nil
}

func (l *loaded) WatchExec(e wcbpf.ExecInfo) (wcbpf.ExecWatch, error) {
	var m execschedExecSched
	if e.Init {
		m.HasInit = 1
	}
	if err := l.objs.Execs.Update(e.CgroupID, m, ebpf.UpdateNoExist); err != nil {
		return nil, fmt.Errorf("measuring the exec with cgroup %d: %w", e.CgroupID, err)
	}
	return &watch{l: l, cgroupID: e.CgroupID}, nil
}

func (l *loaded) Close() error {
	var errs []error
	for _, lk := range l.links {
		errs = append(errs, lk.Close())
	}
	errs = append(errs, l.objs.Close())
	return errors.Join(errs...)
}

type watch struct {
	l        *loaded
	cgroupID uint64
}

func (w *watch) Finish(ctx context.Context, opID uint64) {
	var m execschedExecSched
	err := w.l.objs.Execs.Lookup(w.cgroupID, &m)
	if delErr := w.l.objs.Execs.Delete(w.cgroupID); delErr != nil {
		slog.Warn("exec_sched: forgetting an exec", "cgroup", w.cgroupID, "error", delErr)
	}
	if err != nil {
		slog.Warn("exec_sched: reading an exec", "cgroup", w.cgroupID, "error", err)
		return
	}
	record(ctx, opID, m)
}

// record records what was measured of an exec on its op opID: the parts of
// its container's life as ops, and its threads' CPU waits and time on CPU as
// a reading.
func record(ctx context.Context, opID uint64, m execschedExecSched) {
	ctx = wcprof.ContextWithOpID(ctx, opID)
	clock := wcbpf.KernelClock()
	// Without the init, crun's child execs the workload; there are no init
	// parts.
	setupEnd := m.InitExecNs
	if m.HasInit == 0 {
		setupEnd = m.WorkloadExecNs
	}
	for _, part := range []struct {
		class      string
		start, end uint64
	}{
		{ClassContainerSetup, m.ContainerStartNs, setupEnd},
		{ClassInitStart, m.InitExecNs, m.WorkloadExecNs},
		{ClassWorkload, m.WorkloadExecNs, m.WorkloadExitNs},
		{ClassInitExit, m.WorkloadExitNs, m.InitExitNs},
	} {
		if part.start == 0 || part.end < part.start {
			continue
		}
		wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, part.class, wcprof.OpOpts{},
			clock(part.start), clock(part.end), wcprof.OutcomeOK)
	}
	wcprof.RecordUsage(ctx, opID, wcprof.UsageScopeExecSched, wcprof.Usage{
		RunqWaitUS: int64(m.RunqWaitNs / 1000),
		RunqWaits:  int64(m.RunqWaits),
		OnCPUUS:    int64(m.OncpuNs / 1000),
	})
}
