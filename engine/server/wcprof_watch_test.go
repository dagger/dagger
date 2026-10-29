package server

import (
	"bytes"
	"context"
	"runtime/pprof"
	"strings"
	"sync"
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

// manualTimers stands in for the watch's timers in a test: a watched session's
// timer fires only when the test fires it.
type manualTimers struct {
	mu     sync.Mutex
	timers []*manualTimer
}

type manualTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func manualWatchTimers(t *testing.T) *manualTimers {
	t.Helper()
	m := &manualTimers{}
	prev := watchTimer
	watchTimer = func(d time.Duration, f func()) func() bool {
		tm := &manualTimer{d: d, f: f}
		m.mu.Lock()
		m.timers = append(m.timers, tm)
		m.mu.Unlock()
		return func() bool {
			m.mu.Lock()
			defer m.mu.Unlock()
			was := !tm.stopped
			tm.stopped = true
			return was
		}
	}
	t.Cleanup(func() { watchTimer = prev })
	return m
}

// fire fires the i-th timer started, as time.AfterFunc would unless it was
// stopped, and returns once its capture is done.
func (m *manualTimers) fire(i int) {
	m.mu.Lock()
	tm := m.timers[i]
	stopped := tm.stopped
	tm.stopped = true
	m.mu.Unlock()
	if !stopped {
		tm.f()
	}
}

func (m *manualTimers) durations() []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ds []time.Duration
	for _, tm := range m.timers {
		ds = append(ds, tm.d)
	}
	return ds
}

func captureSessionIDs(c Capture) []string {
	var ids []string
	for _, s := range c.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

// A session that runs past the watch's threshold gets one capture: the
// goroutines' stacks around a CPU profile, naming it and the other sessions
// running, though nothing is profiled.
func TestWatchCapturesLongSession(t *testing.T) {
	shortCaptures(t)
	timers := manualWatchTimers(t)
	srv := &Server{}
	srv.Watch(time.Minute)
	other := srv.beginSessionPhase(context.Background(), "watch-other")
	p := srv.beginSessionPhase(context.Background(), "watch-long")
	require.NotNil(t, p)
	require.Equal(t, []time.Duration{time.Minute, time.Minute}, timers.durations())

	timers.fire(1)
	got := srv.Captures()
	require.Len(t, got, 1)
	c := got[0]
	require.Equal(t, "watch-long", c.Session)
	require.ElementsMatch(t, []string{"watch-long", "watch-other"}, captureSessionIDs(c))
	for _, s := range c.Sessions {
		require.GreaterOrEqual(t, s.RunningMS, int64(0))
	}
	require.True(t, strings.HasPrefix(c.GoroutinesBefore, "goroutine "), "goroutine stacks: %.80q", c.GoroutinesBefore)
	require.True(t, strings.HasPrefix(c.GoroutinesAfter, "goroutine "))
	require.NotEmpty(t, c.CPUProfile)
	require.Empty(t, c.CPUProfileSkipped)

	// A session that crosses its threshold while another capture runs is
	// skipped, not queued.
	srv.captureMu.Lock()
	timers.fire(0)
	srv.captureMu.Unlock()
	require.Len(t, srv.Captures(), 1)
	p.end(srv)
	other.end(srv)
}

// A session that ends before the threshold gets no capture, and a watch
// that's off adds nothing to a session.
func TestWatchNoCapture(t *testing.T) {
	shortCaptures(t)
	timers := manualWatchTimers(t)
	srv := &Server{}
	require.Nil(t, srv.beginSessionPhase(context.Background(), "unwatched"), "an unwatched, unprofiled session got a phase run")

	srv.Watch(time.Minute)
	p := srv.beginSessionPhase(context.Background(), "watch-short")
	require.NotNil(t, p)
	p.end(srv)
	timers.fire(0)
	require.Empty(t, srv.Captures())

	srv.Watch(0)
	require.Zero(t, srv.WatchThreshold())
	require.Nil(t, srv.beginSessionPhase(context.Background(), "watch-off"))
	require.Len(t, timers.durations(), 1)
}

// Switching the watch off stops captures of sessions already watched.
func TestWatchOffStopsPendingCaptures(t *testing.T) {
	shortCaptures(t)
	timers := manualWatchTimers(t)
	srv := &Server{}
	srv.Watch(time.Minute)
	p := srv.beginSessionPhase(context.Background(), "watch-pending")
	srv.Watch(0)
	timers.fire(0)
	require.Empty(t, srv.Captures())
	p.end(srv)
}

// A session keeps the threshold it started under, and a capture names the
// sessions running under every threshold.
func TestWatchThresholdChange(t *testing.T) {
	shortCaptures(t)
	timers := manualWatchTimers(t)
	srv := &Server{}
	srv.Watch(time.Minute)
	a := srv.beginSessionPhase(context.Background(), "watch-a")
	srv.Watch(time.Hour)
	b := srv.beginSessionPhase(context.Background(), "watch-b")
	require.Equal(t, []time.Duration{time.Minute, time.Hour}, timers.durations())

	timers.fire(0)
	got := srv.Captures()
	require.Len(t, got, 1)
	require.Equal(t, "watch-a", got[0].Session)
	require.ElementsMatch(t, []string{"watch-a", "watch-b"}, captureSessionIDs(got[0]))
	a.end(srv)
	b.end(srv)
}

// When Go's CPU profiler is already in use, a capture keeps the goroutines'
// stacks and says why it has no profile.
func TestWatchCaptureWithProfilerBusy(t *testing.T) {
	shortCaptures(t)
	timers := manualWatchTimers(t)
	var other bytes.Buffer
	require.NoError(t, pprof.StartCPUProfile(&other))
	defer pprof.StopCPUProfile()
	srv := &Server{}
	srv.Watch(time.Minute)
	p := srv.beginSessionPhase(context.Background(), "watch-busy")
	timers.fire(0)
	p.end(srv)
	got := srv.Captures()
	require.Len(t, got, 1)
	require.Empty(t, got[0].CPUProfile)
	require.NotEmpty(t, got[0].CPUProfileSkipped)
	require.NotEmpty(t, got[0].GoroutinesBefore)
}

// Captures keep the last few, oldest dropped first.
func TestWatchKeepsLastCaptures(t *testing.T) {
	shortCaptures(t)
	timers := manualWatchTimers(t)
	srv := &Server{}
	srv.Watch(time.Minute)
	for i := range maxCaptures + 2 {
		p := srv.beginSessionPhase(context.Background(), "watch-"+string(rune('a'+i)))
		timers.fire(i)
		p.end(srv)
	}
	got := srv.Captures()
	require.Len(t, got, maxCaptures)
	require.Equal(t, "watch-c", got[0].Session)
	require.Equal(t, "watch-f", got[len(got)-1].Session)
}
