package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/wcprof"
)

func armedOf(srv *Server) *armedProfile {
	srv.armedMu.Lock()
	defer srv.armedMu.Unlock()
	return srv.armed
}

// burnCPU keeps this process busy for d.
func burnCPU(d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
	}
}

// touchFreshMemory maps n bytes of new anonymous memory, without huge pages,
// and writes one byte per page, so the process takes a page fault for each
// page.
func touchFreshMemory(t *testing.T, n int) {
	b, err := unix.Mmap(-1, 0, n, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	require.NoError(t, err)
	defer func() { require.NoError(t, unix.Munmap(b)) }()
	// A kernel without transparent huge pages refuses the advice, and can't
	// back the mapping with huge pages anyway.
	if err := unix.Madvise(b, unix.MADV_NOHUGEPAGE); !errors.Is(err, unix.EINVAL) {
		require.NoError(t, err)
	}
	for i := 0; i < n; i += os.Getpagesize() {
		b[i] = 1
	}
}

// A profiled phase is an op carrying the engine's CPU activity during it,
// with the engine process's page faults and Go runtime counters.
func TestPhaseRecordsEngineCPU(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	srv := &Server{}
	p := srv.beginSessionPhase(ctx, "phase-test-session")
	require.NotNil(t, p)
	burnCPU(50 * time.Millisecond)
	touchFreshMemory(t, 16<<20)
	const allocated = 64 << 20
	mem := make([]byte, allocated)
	runtime.GC()
	p.end(srv)
	runtime.KeepAlive(mem)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var opID uint64
	for _, ev := range events {
		if ev.Type == "op" && header.Strings[ev.ClassID] == PhaseSession && header.Strings[ev.IdentID] == "phase-test-session" {
			opID = ev.OpID
		}
	}
	require.NotZero(t, opID, "no session phase op")
	usage := map[string]wcprof.DumpEvent{}
	for _, ev := range events {
		if ev.Type == wcprof.DumpTypeUsage && ev.OpID == opID {
			usage[header.Strings[ev.ClassID]] = ev
		}
	}
	proc, ok := usage[UsageScopeEngineProcess]
	require.True(t, ok, "no engine process reading: %+v", usage)
	require.GreaterOrEqual(t, proc.CPUUserUS+proc.CPUSystemUS, int64(25_000), "50ms of busy work: %+v", proc)
	require.GreaterOrEqual(t, proc.MinorFaults, int64(16<<20/os.Getpagesize()), "16 MiB of fresh memory touched: %+v", proc)
	require.GreaterOrEqual(t, proc.GoAllocBytes, int64(allocated), "64 MiB allocated: %+v", proc)
	require.GreaterOrEqual(t, proc.GoGCCycles, int64(1), "a forced GC: %+v", proc)
	require.Positive(t, proc.GoGCCPUUS, "a forced GC: %+v", proc)
	require.Contains(t, usage, UsageScopeEngineChildren)
	machine, ok := usage[UsageScopeMachine]
	require.True(t, ok, "no machine reading: %+v", usage)
	total := machine.CPUUserUS + machine.CPUSystemUS + machine.CPUIowaitUS + machine.CPUStealUS + machine.CPUIdleUS
	require.GreaterOrEqual(t, total, int64(25_000), "50ms of the machine's time: %+v", machine)
}

// A profiled phase samples the machine's CPU while it runs: readings in time
// order, each covering the stretch since the previous one, the last ending with
// the phase, and none after it.
func TestPhaseSamplesMachineCPU(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	srv := &Server{}
	p := srv.beginSessionPhase(ctx, "phase-sample-session")
	require.NotNil(t, p)
	samples := func() (wcprof.DumpEvent, []wcprof.DumpEvent) {
		var buf bytes.Buffer
		require.NoError(t, wcprof.Active().WriteDump(&buf, false))
		header, events, err := wcprof.ReadDump(&buf)
		require.NoError(t, err)
		var op wcprof.DumpEvent
		var got []wcprof.DumpEvent
		for _, ev := range events {
			switch {
			case ev.Type == "op" && ev.OpID == p.op.ID():
				op = ev
			case ev.Type == wcprof.DumpTypeUsage && ev.OpID == p.op.ID() && header.Strings[ev.ClassID] == UsageScopeMachineSample:
				got = append(got, ev)
			}
		}
		return op, got
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(machineSampleInterval) {
		if _, got := samples(); len(got) >= 2 {
			break
		}
		require.True(t, time.Now().Before(deadline), "the running phase took no readings")
	}
	p.end(srv)
	op, got := samples()
	time.Sleep(2 * machineSampleInterval)
	_, after := samples()
	require.Len(t, after, len(got), "readings after the phase ended")

	require.NotZero(t, op.OpID, "no session phase op")
	require.GreaterOrEqual(t, len(got), 3, "readings while it ran, and the last at its end")
	prev := op.StartNS
	for _, s := range got {
		require.GreaterOrEqual(t, s.StartNS, prev, "readings out of order")
		require.LessOrEqual(t, s.StartNS, op.EndNS, "a reading after the phase ended")
		prev = s.StartNS
	}

}

// A profiled phase reads the engine's cgroup: the bytes it read and wrote
// while the phase ran, and its memory with each machine reading.
func TestPhaseRecordsEngineCgroup(t *testing.T) {
	dir := t.TempDir()
	// Each file is replaced whole, as the phase's sampler may be reading it.
	write := func(name, body string) {
		t.Helper()
		tmp := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.WriteFile(tmp, []byte(body), 0o644))
		require.NoError(t, os.Rename(tmp, filepath.Join(dir, name)))
	}
	write("cpu.stat", "usage_usec 3000\nuser_usec 2000\nsystem_usec 1000\n")
	write("io.stat", "259:0 rbytes=100 wbytes=200 rios=1 wios=1\n")
	write("memory.stat", "anon 1000\nfile 2000\nanon_thp 0\n")
	orig := engineCgroupDir
	engineCgroupDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { engineCgroupDir = orig })

	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	srv := &Server{}
	p := srv.beginSessionPhase(ctx, "phase-cgroup-session")
	require.NotNil(t, p)
	readings := func() (cgroup *wcprof.DumpEvent, mem []wcprof.DumpEvent) {
		var buf bytes.Buffer
		require.NoError(t, wcprof.Active().WriteDump(&buf, false))
		header, events, err := wcprof.ReadDump(&buf)
		require.NoError(t, err)
		for _, ev := range events {
			if ev.Type != wcprof.DumpTypeUsage || ev.OpID != p.op.ID() {
				continue
			}
			switch header.Strings[ev.ClassID] {
			case UsageScopeEngineCgroup:
				cgroup = &ev
			case UsageScopeEngineCgroupSample:
				mem = append(mem, ev)
			}
		}
		return cgroup, mem
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(machineSampleInterval) {
		if _, mem := readings(); len(mem) > 0 {
			break
		}
		require.True(t, time.Now().Before(deadline), "the running phase sampled no memory")
	}
	write("cpu.stat", "usage_usec 9000\nuser_usec 6000\nsystem_usec 3000\n")
	write("io.stat", "259:0 rbytes=1100 wbytes=4200 rios=2 wios=3\n259:1 rbytes=50 wbytes=0 rios=1 wios=0\n")
	write("memory.stat", "anon 5000\nfile 7000\nanon_thp 4000\nfile_thp 1000\n")
	p.end(srv)

	cgroup, mem := readings()
	require.NotNil(t, cgroup, "no engine cgroup reading")
	require.Equal(t, int64(4000), cgroup.CPUUserUS)
	require.Equal(t, int64(2000), cgroup.CPUSystemUS)
	require.Equal(t, int64(1050), cgroup.IOReadBytes, "read over both devices, since the phase began")
	require.Equal(t, int64(4000), cgroup.IOWriteBytes, "written since the phase began")
	first, last := mem[0], mem[len(mem)-1]
	require.Equal(t, [3]int64{1000, 2000, 0}, [3]int64{first.MemAnonBytes, first.MemFileBytes, first.MemHugeBytes})
	require.Equal(t, [3]int64{5000, 7000, 5000}, [3]int64{last.MemAnonBytes, last.MemFileBytes, last.MemHugeBytes}, "the last sample, at the phase's end")
}

// The machine's CPU times come from /proc/stat's first line, in its 10 ms
// ticks, with nice counted as user and interrupts as system.
func TestParseMachineCPU(t *testing.T) {
	u, ok := parseMachineCPU([]byte("cpu  100 2 30 4000 5 6 7 8 0 0\ncpu0 50 1 15 2000 2 3 3 4 0 0\n"))
	require.True(t, ok)
	require.Equal(t, wcprof.Usage{
		CPUUserUS:   1_020_000,
		CPUSystemUS: 430_000,
		CPUIdleUS:   40_000_000,
		CPUIowaitUS: 50_000,
		CPUStealUS:  80_000,
	}, u)
	for _, bad := range []string{"", "cpu0 1 2 3 4 5 6 7 8\n", "cpu 1 2 3\n", "cpu 1 x 3 4 5 6 7 8\n"} {
		_, ok := parseMachineCPU([]byte(bad))
		require.False(t, ok, "%q", bad)
	}
}

// An armed profile covers the phase's next run and is delivered at its end.
func TestProfilePhase(t *testing.T) {
	srv := &Server{}
	type result struct {
		prof []byte
		err  error
	}
	res := make(chan result, 1)
	go func() {
		prof, err := srv.ProfilePhase(context.Background(), PhaseSession)
		res <- result{prof, err}
	}()
	require.Eventually(t, func() bool { return armedOf(srv) != nil }, 5*time.Second, time.Millisecond)

	_, err := srv.ProfilePhase(context.Background(), PhaseSession)
	require.ErrorIs(t, err, errProfileArmed)

	// Not profiled: the run exists only for the armed profile.
	p := srv.beginSessionPhase(context.Background(), "s")
	require.NotNil(t, p)
	burnCPU(50 * time.Millisecond)
	p.end(srv)

	r := <-res
	require.NoError(t, r.err)
	require.True(t, bytes.HasPrefix(r.prof, []byte{0x1f, 0x8b}), "not a gzipped pprof profile: % x", r.prof[:min(len(r.prof), 8)])
	require.Nil(t, armedOf(srv))
}

// A client that leaves before the run starts disarms the profile.
func TestProfilePhaseDisarms(t *testing.T) {
	srv := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := srv.ProfilePhase(ctx, PhaseSession)
		res <- err
	}()
	require.Eventually(t, func() bool { return armedOf(srv) != nil }, 5*time.Second, time.Millisecond)
	cancel()
	require.True(t, errors.Is(<-res, context.Canceled))
	require.Nil(t, armedOf(srv))
	if p := srv.beginSessionPhase(context.Background(), "s"); p != nil {
		require.Nil(t, p.prof, "a disarmed profile started")
		p.end(srv)
	}
}

// A profile whose run started stays armed through its client leaving, so no
// other profile can arm until the run ends, and it is still delivered then.
func TestProfilePhaseStartedKeepsOwnership(t *testing.T) {
	srv := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		prof []byte
		err  error
	}
	res := make(chan result, 1)
	go func() {
		prof, err := srv.ProfilePhase(ctx, PhaseSession)
		res <- result{prof, err}
	}()
	require.Eventually(t, func() bool { return armedOf(srv) != nil }, 5*time.Second, time.Millisecond)
	p := srv.beginSessionPhase(context.Background(), "s")
	require.NotNil(t, p)
	require.NotNil(t, p.prof)
	cancel()

	_, err := srv.ProfilePhase(context.Background(), PhaseSession)
	require.ErrorIs(t, err, errProfileArmed)

	burnCPU(20 * time.Millisecond)
	p.end(srv)
	r := <-res
	require.NoError(t, r.err)
	require.True(t, bytes.HasPrefix(r.prof, []byte{0x1f, 0x8b}))
	require.Nil(t, armedOf(srv))
}

// A profile that fails to start (another CPU profile is running) gives way
// to the next one armed, and its request disarming late leaves that one be.
func TestProfilePhaseFailedStartLeavesReplacementArmed(t *testing.T) {
	require.NoError(t, pprof.StartCPUProfile(io.Discard))
	srv := &Server{}
	resA := make(chan error, 1)
	go func() {
		_, err := srv.ProfilePhase(context.Background(), PhaseSession)
		resA <- err
	}()
	require.Eventually(t, func() bool { return armedOf(srv) != nil }, 5*time.Second, time.Millisecond)
	a := armedOf(srv)
	if p := srv.beginSessionPhase(context.Background(), "s"); p != nil {
		require.Nil(t, p.prof, "started while another CPU profile ran")
		p.end(srv)
	}
	require.Error(t, <-resA)
	pprof.StopCPUProfile()
	require.Nil(t, armedOf(srv))

	ctx, cancel := context.WithCancel(context.Background())
	resB := make(chan error, 1)
	go func() {
		_, err := srv.ProfilePhase(ctx, PhaseSession)
		resB <- err
	}()
	require.Eventually(t, func() bool { return armedOf(srv) != nil }, 5*time.Second, time.Millisecond)
	b := armedOf(srv)
	require.NotSame(t, a, b)
	require.False(t, srv.disarm(a))
	require.Same(t, b, armedOf(srv), "the failed profile's late disarm removed its replacement")
	cancel()
	require.ErrorIs(t, <-resB, context.Canceled)
}

// A profiled run's steps are ops under its op, nested as begun; an
// unprofiled run's are no-ops.
func TestPhaseSteps(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	srv := &Server{}
	p := srv.beginSessionPhase(ctx, "phase-steps-session")
	require.NotNil(t, p)
	teardown := p.beginTeardown()
	step := teardown.beginStep("session.releaseCache")
	step.end()
	teardown.end()
	p.end(srv)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	parents := map[uint64]uint64{}
	var sessionID, teardownID, stepID uint64
	for _, ev := range events {
		if ev.Type != "op" {
			continue
		}
		parents[ev.OpID] = ev.ParentID
		switch {
		case header.Strings[ev.ClassID] == PhaseSession && header.Strings[ev.IdentID] == "phase-steps-session":
			sessionID = ev.OpID
		case header.Strings[ev.ClassID] == "session.teardown":
			teardownID = ev.OpID
		case header.Strings[ev.ClassID] == "session.releaseCache":
			stepID = ev.OpID
		}
	}
	require.NotZero(t, sessionID, "no session phase op")
	require.NotZero(t, teardownID, "no teardown op")
	require.NotZero(t, stepID, "no step op")
	require.Equal(t, sessionID, parents[teardownID], "teardown under the session")
	require.Equal(t, teardownID, parents[stepID], "step under the teardown")

	// Not profiled: no run, or a run only for an armed profile; its steps do
	// nothing.
	var none *phaseRun
	none.beginTeardown().beginStep("x").end()
	(&phaseRun{}).beginTeardown().end()
}

// A profiled phase samples the engine's Go GC while it runs: the CPU its GC
// used in each stretch, and its heap and scan sizes.
func TestPhaseSamplesGoRuntime(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	srv := &Server{}
	p := srv.beginSessionPhase(ctx, "phase-gc-session")
	require.NotNil(t, p)
	var keep [][]byte
	for range 5 {
		keep = append(keep, make([]byte, 8<<20))
		runtime.GC()
		time.Sleep(machineSampleInterval)
	}
	p.end(srv)
	runtime.KeepAlive(keep)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var samples []wcprof.DumpEvent
	for _, ev := range events {
		if ev.Type == wcprof.DumpTypeUsage && ev.OpID == p.op.ID() && header.Strings[ev.ClassID] == UsageScopeGoRuntimeSample {
			samples = append(samples, ev)
		}
	}
	require.GreaterOrEqual(t, len(samples), 2, "samples while it ran, and the last at its end")
	var gcCPU int64
	for _, s := range samples {
		gcCPU += s.GCAssistUS + s.GCDedicatedUS + s.GCIdleUS + s.GCPauseUS
		require.Positive(t, s.HeapGoalBytes, "%+v", s)
		require.Positive(t, s.ScanStackBytes, "%+v", s)
	}
	require.Positive(t, gcCPU, "five forced GCs used no GC CPU: %+v", samples)
	last := samples[len(samples)-1]
	require.GreaterOrEqual(t, last.HeapLiveBytes, int64(40<<20), "40 MiB kept live: %+v", last)
}

// A profiled phase samples the engine process's CPU while it runs: stretches
// that together cover the CPU it used, so a window of the phase can sum them.
func TestPhaseSamplesEngineProcessCPU(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	srv := &Server{}
	p := srv.beginSessionPhase(ctx, "phase-process-session")
	require.NotNil(t, p)
	for range 3 {
		burnCPU(100 * time.Millisecond)
	}
	p.end(srv)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var sampled int64
	var n int
	var total wcprof.DumpEvent
	for _, ev := range events {
		if ev.Type != wcprof.DumpTypeUsage || ev.OpID != p.op.ID() {
			continue
		}
		switch header.Strings[ev.ClassID] {
		case UsageScopeEngineProcessSample:
			sampled += ev.CPUUserUS + ev.CPUSystemUS
			n++
		case UsageScopeEngineProcess:
			total = ev
		}
	}
	require.GreaterOrEqual(t, n, 2, "samples while it ran, and the last at its end")
	require.GreaterOrEqual(t, sampled, int64(200_000), "300 ms of busy work: %d us sampled", sampled)
	// The samples cover the phase from its first reading to its last, as
	// the whole-phase reading does, read a moment later.
	require.InDelta(t, total.CPUUserUS+total.CPUSystemUS, sampled, 50_000)
}
