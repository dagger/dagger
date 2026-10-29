//go:build linux && (amd64 || arm64)

package execsched

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/wcprof"
)

// recordedExec records m on a new exec.run op and returns the dump's ops
// under it, by class, and its readings, by scope.
func recordedExec(t *testing.T, m execschedExecSched) (map[string]wcprof.DumpEvent, map[string]wcprof.DumpEvent) {
	t.Helper()
	wcprof.EnableGlobal()
	t.Cleanup(wcprof.DisableGlobal)
	rec := wcprof.Active()
	require.NoError(t, rec.WriteDump(io.Discard, true))

	ctx, op := wcprof.BeginOp(context.Background(), wcprof.OpKindExec, "exec.run", wcprof.OpOpts{})
	record(ctx, op.ID(), m)
	op.End(wcprof.OutcomeOK)

	var buf bytes.Buffer
	require.NoError(t, rec.WriteDump(&buf, true))
	h, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	ops, usage := map[string]wcprof.DumpEvent{}, map[string]wcprof.DumpEvent{}
	for _, ev := range events {
		switch {
		case ev.Type == "op" && ev.ParentID == op.ID():
			require.Equal(t, "exec_phase", ev.OpKind)
			ops[h.Strings[ev.ClassID]] = ev
		case ev.Type == wcprof.DumpTypeUsage && ev.OpID == op.ID():
			usage[h.Strings[ev.ClassID]] = ev
		}
	}
	return ops, usage
}

func monotonicNow(t *testing.T) int64 {
	t.Helper()
	var ts unix.Timespec
	require.NoError(t, unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts))
	return ts.Nano()
}

// An exec's measurements become the parts of its container's life, at the
// times the kernel saw them, and a reading.
func TestRecord(t *testing.T) {
	now := monotonicNow(t)
	at := func(ago time.Duration) uint64 { return uint64(now - int64(ago)) }
	nowNS := wcprof.NowNS()
	ops, usage := recordedExec(t, execschedExecSched{
		ContainerStartNs: at(500 * time.Millisecond),
		InitExecNs:       at(498 * time.Millisecond),
		WorkloadExecNs:   at(495 * time.Millisecond),
		WorkloadExitNs:   at(100 * time.Millisecond),
		InitExitNs:       at(99 * time.Millisecond),
		RunqWaitNs:       7_500_000,
		RunqWaits:        12,
		OncpuNs:          300_000_000,
	})
	want := map[string]time.Duration{
		ClassContainerSetup: 2 * time.Millisecond,
		ClassInitStart:      3 * time.Millisecond,
		ClassWorkload:       395 * time.Millisecond,
		ClassInitExit:       time.Millisecond,
	}
	require.Len(t, ops, len(want))
	for class, d := range want {
		ev := ops[class]
		require.InDelta(t, int64(d), ev.EndNS-ev.StartNS, float64(10*time.Microsecond), class)
	}
	if nowNS != 0 {
		// The recorder existed before the reading: its end is 99 ms before
		// then, give or take the time between the two clocks' reads.
		require.InDelta(t, nowNS-int64(99*time.Millisecond), ops[ClassInitExit].EndNS, float64(5*time.Millisecond))
	}
	u := usage[wcprof.UsageScopeExecSched]
	require.Equal(t, int64(7500), u.RunqWaitUS)
	require.Equal(t, int64(12), u.RunqWaits)
	require.Equal(t, int64(300_000), u.OnCPUUS)
}

// A part whose end the kernel never saw is left out.
func TestRecordPartial(t *testing.T) {
	now := uint64(monotonicNow(t))
	ops, usage := recordedExec(t, execschedExecSched{
		ContainerStartNs: now - 2_000_000,
		InitExecNs:       now - 1_000_000,
	})
	require.Len(t, ops, 1)
	require.Contains(t, ops, ClassContainerSetup)
	require.Contains(t, usage, wcprof.UsageScopeExecSched)
}
