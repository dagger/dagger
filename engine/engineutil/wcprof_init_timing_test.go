package engineutil

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/wcprof"
)

func TestInitTimingOnlyWhenProfilingInit(t *testing.T) {
	wcprof.EnsureRecorder()
	profiled := wcprof.ContextWithProfiling(t.Context())
	initSpec := &specs.Spec{Process: &specs.Process{Args: []string{initPath, "true"}, Env: []string{"A=1"}}}

	timing, err := newInitTiming(t.Context(), initSpec)
	require.NoError(t, err)
	require.Nil(t, timing, "not profiling")
	timing, err = newInitTiming(profiled, &specs.Spec{Process: &specs.Process{Args: []string{"true"}}})
	require.NoError(t, err)
	require.Nil(t, timing, "no init injected")

	timing, err = newInitTiming(profiled, initSpec)
	require.NoError(t, err)
	require.NotNil(t, timing)
	defer timing.close()
	require.NotNil(t, timing.writeEnd())
}

func TestInitTimingRead(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(t.Context())
	timing, err := newInitTiming(ctx, &specs.Spec{Process: &specs.Process{Args: []string{initPath, "true"}}})
	require.NoError(t, err)
	defer timing.close()

	// What /.init writes: CLOCK_MONOTONIC nanoseconds.
	before := wcprof.NowNS()
	started := monotonicNS()
	spawned := started + int64(time.Millisecond)
	exited := started + int64(5*time.Millisecond)
	_, err = fmt.Fprintf(timing.w, "%d %d %d\n", started, spawned, exited)
	require.NoError(t, err)

	gotStarted, gotSpawned, gotExited, ok := timing.read()
	require.True(t, ok)
	require.GreaterOrEqual(t, gotStarted, before)
	require.LessOrEqual(t, gotStarted, wcprof.NowNS())
	require.Equal(t, int64(time.Millisecond), gotSpawned-gotStarted)
	require.Equal(t, int64(5*time.Millisecond), gotExited-gotStarted)
}

func TestInitTimingReadNoReport(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(t.Context())
	timing, err := newInitTiming(ctx, &specs.Spec{Process: &specs.Process{Args: []string{initPath, "true"}}})
	require.NoError(t, err)
	defer timing.close()
	// /.init died before reaping the command: nothing was written, and the
	// read doesn't block.
	_, _, exited, ok := timing.read()
	require.False(t, ok)
	require.Zero(t, exited)

	var nilTiming *initTiming
	_, _, exited, ok = nilTiming.read()
	require.False(t, ok)
	require.Zero(t, exited)
}

func TestInitTimingReadLeakedWriter(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(t.Context())
	for _, report := range []bool{false, true} {
		timing, err := newInitTiming(ctx, &specs.Spec{Process: &specs.Process{Args: []string{initPath, "true"}}})
		require.NoError(t, err)
		// A copy of the write end that outlives runc, e.g. leaked to a
		// process left running in the container.
		leaked, err := unix.Dup(int(timing.w.Fd()))
		require.NoError(t, err)
		if report {
			_, err = fmt.Fprintf(timing.w, "%d %d %d\n", monotonicNS(), monotonicNS(), monotonicNS())
			require.NoError(t, err)
		}
		done := make(chan bool)
		go func() {
			_, _, _, ok := timing.read()
			done <- ok
		}()
		select {
		case ok := <-done:
			require.Equal(t, report, ok)
		case <-time.After(5 * time.Second):
			t.Fatalf("read blocked on the leaked writer (report=%v)", report)
		}
		unix.Close(leaked)
		timing.close()
	}
}

// phases returns the exec_phase ops recorded under parent, by class.
func phases(t *testing.T, parent uint64) map[string]wcprof.DumpEvent {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	got := map[string]wcprof.DumpEvent{}
	for _, ev := range events {
		if ev.Type == "op" && ev.ParentID == parent {
			got[header.Strings[ev.ClassID]] = ev
		}
	}
	return got
}

func TestRecordProcessRunSplit(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(t.Context())
	ms := int64(time.Millisecond)
	runID := wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.processRun", wcprof.OpOpts{WorkType: wcprof.WorkTypeUser}, 10*ms, 100*ms, wcprof.OutcomeOK)
	recordProcessRunSplit(ctx, runID, "exec-id", []string{"go", "build"}, 10*ms, 100*ms, 12*ms, 15*ms, 90*ms, wcprof.OutcomeOK)

	got := phases(t, runID)
	require.Len(t, got, 4)
	for class, want := range map[string][2]int64{
		"exec.initStart":    {10 * ms, 12 * ms},
		"exec.processSpawn": {12 * ms, 15 * ms},
		"exec.workload":     {15 * ms, 90 * ms},
		"exec.processExit":  {90 * ms, 100 * ms},
	} {
		require.Equal(t, want, [2]int64{got[class].StartNS, got[class].EndNS}, class)
	}
	require.Equal(t, wcprof.WorkTypeUser.String(), got["exec.workload"].WorkType)
	require.Equal(t, wcprof.WorkTypeEngine.String(), got["exec.processSpawn"].WorkType)
}

func TestRecordProcessRunSplitClamped(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(t.Context())
	ms := int64(time.Millisecond)

	// /.init started before the coarse release time: the phases stay ordered.
	runID := wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.processRun", wcprof.OpOpts{}, 10*ms, 100*ms, wcprof.OutcomeOK)
	recordProcessRunSplit(ctx, runID, "exec-id", nil, 10*ms, 100*ms, 8*ms, 15*ms, 90*ms, wcprof.OutcomeOK)
	got := phases(t, runID)
	require.Equal(t, [2]int64{10 * ms, 10 * ms}, [2]int64{got["exec.initStart"].StartNS, got["exec.initStart"].EndNS})
	require.Equal(t, [2]int64{10 * ms, 15 * ms}, [2]int64{got["exec.processSpawn"].StartNS, got["exec.processSpawn"].EndNS})

	// A report that ends after the run is not recorded.
	runID = wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.processRun", wcprof.OpOpts{}, 10*ms, 100*ms, wcprof.OutcomeOK)
	recordProcessRunSplit(ctx, runID, "exec-id", nil, 10*ms, 100*ms, 12*ms, 15*ms, 101*ms, wcprof.OutcomeOK)
	require.Empty(t, phases(t, runID))
}
