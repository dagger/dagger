package server

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

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
	p := srv.beginPhase(ctx, PhaseSession, "phase-test-session")
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
	require.Eventually(t, func() bool { return srv.armedProfile.Load() != nil }, 5*time.Second, time.Millisecond)

	_, err := srv.ProfilePhase(context.Background(), PhaseSession)
	require.ErrorIs(t, err, errProfileArmed)

	// Not profiled: the run exists only for the armed profile.
	p := srv.beginPhase(context.Background(), PhaseSession, "s")
	require.NotNil(t, p)
	burnCPU(50 * time.Millisecond)
	p.end(srv)

	r := <-res
	require.NoError(t, r.err)
	require.True(t, bytes.HasPrefix(r.prof, []byte{0x1f, 0x8b}), "not a gzipped pprof profile: % x", r.prof[:min(len(r.prof), 8)])
	require.Nil(t, srv.armedProfile.Load())
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
	require.Eventually(t, func() bool { return srv.armedProfile.Load() != nil }, 5*time.Second, time.Millisecond)
	cancel()
	require.True(t, errors.Is(<-res, context.Canceled))
	require.Nil(t, srv.armedProfile.Load())
	if p := srv.beginPhase(context.Background(), PhaseSession, "s"); p != nil {
		require.Nil(t, p.prof, "a disarmed profile started")
		p.end(srv)
	}
}
