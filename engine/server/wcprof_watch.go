package server

import (
	"bytes"
	"runtime/pprof"
	"sort"
	"time"

	"github.com/dagger/dagger/engine/slog"
)

// A session watch captures what the engine was doing when a session runs far
// longer than it should, with or without profiling: once a watched session
// runs past the watch's threshold, the engine takes every goroutine's stack, a
// CPU profile over captureCPUDuration, and every goroutine's stack again. It
// is switched on and off through the debug endpoint; while it's off, a
// session's start costs one atomic load. A session keeps the threshold it
// started under; switching the watch off stops captures that haven't started.

// watchTimer starts the timer of a watched session: f runs after d unless
// the returned stop is called first. Tests replace it to fire timers
// themselves.
var watchTimer = func(d time.Duration, f func()) (stop func() bool) {
	return time.AfterFunc(d, f).Stop
}

// captureCPUDuration is how long a capture profiles the engine's CPU.
var captureCPUDuration = 15 * time.Second

// maxCaptures is how many captures the engine keeps, the oldest dropped
// first.
const maxCaptures = 4

// Capture is what the engine was doing when Session ran past the watch's
// threshold.
type Capture struct {
	At time.Time `json:"at"`
	// Session is the session that ran past the threshold, and Sessions every
	// watched session running then, with how long each had run.
	Session  string           `json:"session"`
	Sessions []CaptureSession `json:"sessions"`
	// GoroutinesBefore and GoroutinesAfter are every goroutine's stack, as
	// /debug/pprof/goroutine?debug=2 writes them, before and after the CPU
	// profile.
	GoroutinesBefore string `json:"goroutines_before"`
	GoroutinesAfter  string `json:"goroutines_after"`
	// CPUProfile is a pprof CPU profile over captureCPUDuration. When Go's CPU
	// profiler was already in use, it's empty and CPUProfileSkipped says why.
	CPUProfile        []byte `json:"cpu_profile,omitempty"`
	CPUProfileSkipped string `json:"cpu_profile_skipped,omitempty"`
}

// CaptureSession is a session running at a capture.
type CaptureSession struct {
	ID        string `json:"id"`
	RunningMS int64  `json:"running_ms"`
}

// Watch switches the session watch on with threshold, or off when threshold
// is 0. Sessions already running keep their threshold, or stay unwatched.
func (srv *Server) Watch(threshold time.Duration) {
	srv.watchThreshold.Store(int64(max(threshold, 0)))
	if threshold <= 0 {
		slog.Info("wcprof: session watch off")
		return
	}
	slog.Info("wcprof: session watch on", "threshold", threshold)
}

// WatchThreshold is the session watch's threshold, 0 when it's off.
func (srv *Server) WatchThreshold() time.Duration {
	return time.Duration(srv.watchThreshold.Load())
}

// Captures returns the captures the engine kept, oldest first.
func (srv *Server) Captures() []Capture {
	srv.capturesMu.Lock()
	defer srv.capturesMu.Unlock()
	return append([]Capture(nil), srv.captures...)
}

// watchSession starts watching a session that just began, when the watch is
// on, and returns what stops watching it at its end; nil when the watch is
// off.
func (srv *Server) watchSession(sessionID string) func() {
	threshold := srv.WatchThreshold()
	if threshold == 0 {
		return nil
	}
	srv.watchMu.Lock()
	if srv.watchStarted == nil {
		srv.watchStarted = map[string]time.Time{}
	}
	srv.watchStarted[sessionID] = time.Now()
	srv.watchMu.Unlock()
	stop := watchTimer(threshold, func() { srv.capture(sessionID, threshold) })
	return func() {
		stop()
		srv.watchMu.Lock()
		delete(srv.watchStarted, sessionID)
		srv.watchMu.Unlock()
	}
}

// capture records what the engine is doing as session runs past threshold,
// unless the watch has been switched off since. One capture runs at a time;
// a session that crosses its threshold while another capture runs is logged
// and not captured.
func (srv *Server) capture(session string, threshold time.Duration) {
	if srv.WatchThreshold() == 0 {
		return
	}
	if !srv.captureMu.TryLock() {
		slog.Info("wcprof: session ran past the watch's threshold during another capture", "session", session)
		return
	}
	defer srv.captureMu.Unlock()
	now := time.Now()
	c := Capture{At: now, Session: session}
	srv.watchMu.Lock()
	for id, start := range srv.watchStarted {
		c.Sessions = append(c.Sessions, CaptureSession{ID: id, RunningMS: now.Sub(start).Milliseconds()})
	}
	srv.watchMu.Unlock()
	sort.Slice(c.Sessions, func(i, j int) bool { return c.Sessions[i].RunningMS > c.Sessions[j].RunningMS })
	slog.Info("wcprof: session ran past the watch's threshold; capturing", "session", session, "threshold", threshold)

	c.GoroutinesBefore = goroutineStacks()
	var prof bytes.Buffer
	if err := pprof.StartCPUProfile(&prof); err != nil {
		c.CPUProfileSkipped = err.Error()
	} else {
		time.Sleep(captureCPUDuration)
		pprof.StopCPUProfile()
		c.CPUProfile = prof.Bytes()
	}
	c.GoroutinesAfter = goroutineStacks()

	srv.capturesMu.Lock()
	defer srv.capturesMu.Unlock()
	srv.captures = append(srv.captures, c)
	if len(srv.captures) > maxCaptures {
		srv.captures = srv.captures[len(srv.captures)-maxCaptures:]
	}
}

func goroutineStacks() string {
	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 2); err != nil {
		return "goroutine stacks: " + err.Error()
	}
	return buf.String()
}
