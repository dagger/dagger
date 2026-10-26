package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

// A profiled phase is an op carrying the engine's CPU activity during it.
func TestPhaseRecordsEngineCPU(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	srv := &Server{}
	p := srv.beginSessionPhase(ctx, "phase-test-session")
	require.NotNil(t, p)
	burnCPU(50 * time.Millisecond)
	p.end(srv)

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
	require.Contains(t, usage, UsageScopeEngineChildren)
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
