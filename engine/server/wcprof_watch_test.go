package server

import (
	"bytes"
	"context"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func shortCaptures(t *testing.T) {
	t.Helper()
	prev := captureCPUDuration
	captureCPUDuration = 50 * time.Millisecond
	t.Cleanup(func() { captureCPUDuration = prev })
}

func waitCaptures(t *testing.T, srv *Server, n int) []Capture {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if got := srv.Captures(); len(got) >= n {
			return got
		}
		require.True(t, time.Now().Before(deadline), "no capture")
	}
}

// A session that runs past the watch's threshold gets one capture: the
// goroutines' stacks around a CPU profile, naming it and the other sessions
// running, though nothing is profiled.
func TestWatchCapturesLongSession(t *testing.T) {
	shortCaptures(t)
	srv := &Server{}
	srv.Watch(20 * time.Millisecond)
	other := srv.beginSessionPhase(context.Background(), "watch-other")
	p := srv.beginSessionPhase(context.Background(), "watch-long")
	require.NotNil(t, p)

	got := waitCaptures(t, srv, 1)
	p.end(srv)
	other.end(srv)
	c := got[0]
	require.Contains(t, []string{"watch-long", "watch-other"}, c.Session)
	var ids []string
	for _, s := range c.Sessions {
		ids = append(ids, s.ID)
		require.GreaterOrEqual(t, s.RunningMS, int64(0))
	}
	require.ElementsMatch(t, []string{"watch-long", "watch-other"}, ids)
	require.True(t, strings.HasPrefix(c.GoroutinesBefore, "goroutine "), "goroutine stacks: %.80q", c.GoroutinesBefore)
	require.True(t, strings.HasPrefix(c.GoroutinesAfter, "goroutine "))
	require.NotEmpty(t, c.CPUProfile)
	require.Empty(t, c.CPUProfileSkipped)
	// Each session captures at most once; the other session's capture, if
	// its timer fired during this one, was skipped rather than queued.
	time.Sleep(100 * time.Millisecond)
	require.LessOrEqual(t, len(srv.Captures()), 2)
}

// A session that ends before the threshold gets no capture, and a watch
// that's off adds nothing to a session.
func TestWatchNoCapture(t *testing.T) {
	shortCaptures(t)
	srv := &Server{}
	require.Nil(t, srv.beginSessionPhase(context.Background(), "unwatched"), "an unwatched, unprofiled session got a phase run")

	srv.Watch(200 * time.Millisecond)
	p := srv.beginSessionPhase(context.Background(), "watch-short")
	require.NotNil(t, p)
	p.end(srv)
	time.Sleep(400 * time.Millisecond)
	require.Empty(t, srv.Captures())

	srv.Watch(0)
	require.Zero(t, srv.WatchThreshold())
	require.Nil(t, srv.beginSessionPhase(context.Background(), "watch-off"))
}

// When Go's CPU profiler is already in use, a capture keeps the goroutines'
// stacks and says why it has no profile.
func TestWatchCaptureWithProfilerBusy(t *testing.T) {
	shortCaptures(t)
	var other bytes.Buffer
	require.NoError(t, pprof.StartCPUProfile(&other))
	defer pprof.StopCPUProfile()
	srv := &Server{}
	srv.Watch(10 * time.Millisecond)
	p := srv.beginSessionPhase(context.Background(), "watch-busy")
	c := waitCaptures(t, srv, 1)[0]
	p.end(srv)
	require.Empty(t, c.CPUProfile)
	require.NotEmpty(t, c.CPUProfileSkipped)
	require.NotEmpty(t, c.GoroutinesBefore)
}

// Captures keep the last few, oldest dropped first.
func TestWatchKeepsLastCaptures(t *testing.T) {
	shortCaptures(t)
	srv := &Server{}
	srv.Watch(5 * time.Millisecond)
	for i := range maxCaptures + 2 {
		id := "watch-" + string(rune('a'+i))
		p := srv.beginSessionPhase(context.Background(), id)
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			got := srv.Captures()
			if len(got) > 0 && got[len(got)-1].Session == id {
				break
			}
			require.True(t, time.Now().Before(deadline), "no capture of %s", id)
		}
		p.end(srv)
	}
	got := srv.Captures()
	require.Len(t, got, maxCaptures)
	require.Equal(t, "watch-c", got[0].Session)
	require.Equal(t, "watch-f", got[len(got)-1].Session)
}
