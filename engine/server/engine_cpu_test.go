package server

import (
	"bytes"
	"context"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// spin keeps one goroutine busy for d of wall time.
func spin(d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
	}
}

// engineCPUTest runs srv's engine CPU sampler with short windows while on
// says, and returns its windows as they come.
func engineCPUTest(t *testing.T, srv *Server, on func() bool) <-chan engineCPUWindow {
	t.Helper()
	if srv.engineCPUWindowLen == 0 {
		srv.engineCPUWindowLen = 500 * time.Millisecond
	}
	srv.engineCPUPoll = 20 * time.Millisecond
	windows := make(chan engineCPUWindow, 64)
	srv.engineCPUOn = on
	srv.testEngineCPUWindow = func(w engineCPUWindow) {
		select {
		case windows <- w:
		default:
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := srv.startEngineCPU(ctx)
	// The sampler has stopped, and released the profiler, before the next
	// test or the settings' restore.
	t.Cleanup(func() { cancel(); wait() })
	return windows
}

// While wcprof records, the engine reports its own CPU in windows, split by
// the kind of remote cache work its goroutines are labeled with, with the
// CPU of no kind reported as unattributed, and the kinds summing to the
// process's CPU as the kernel counts it.
func TestEngineCPUByKind(t *testing.T) {
	srv := &Server{}
	windows := engineCPUTest(t, srv, func() bool { return true })

	var wg sync.WaitGroup
	wg.Go(func() {
		_, unlabel := wcprof.WithCPUKind(context.Background(), wcprof.CPUKindMerge)
		defer unlabel()
		// A goroutine the labeled one starts counts with it.
		var inner sync.WaitGroup
		inner.Go(func() { spin(1500 * time.Millisecond) })
		inner.Wait()
	})
	wg.Go(func() { spin(1500 * time.Millisecond) })
	wg.Wait()

	var merge, unattributed, total int64
	deadline := time.After(10 * time.Second)
	for total < 2_000_000 {
		select {
		case w := <-windows:
			var sum int64
			for _, us := range w.Kinds {
				sum += us
			}
			require.Equal(t, w.ProcessUS, sum, "a window's kinds sum to its process CPU: %+v", w)
			merge += w.Kinds[wcprof.CPUKindMerge]
			unattributed += w.Kinds[unattributedCPU]
			total += w.ProcessUS
		case <-deadline:
			t.Fatalf("windows covered %d µs of CPU, not the 3 s spun", total)
		}
	}
	require.Greater(t, merge, total/5, "the merge goroutines' share: merge %d, unattributed %d, total %d", merge, unattributed, total)
	require.Greater(t, unattributed, total/5, "the unlabeled goroutine's share: merge %d, unattributed %d, total %d", merge, unattributed, total)
}

// The sampler gives the profiler to a phase's armed CPU profile, and to the
// session watch's capture, instead of making them fail.
func TestEngineCPUYieldsToWcprofProfiles(t *testing.T) {
	srv := &Server{}
	windows := engineCPUTest(t, srv, func() bool { return true })
	<-windows // a window has run: the sampler holds the profiler now or soon

	res := make(chan []byte, 1)
	errs := make(chan error, 1)
	go func() {
		prof, err := srv.ProfilePhase(context.Background(), PhaseSession)
		res <- prof
		errs <- err
	}()
	require.Eventually(t, func() bool { return armedOf(srv) != nil }, 5*time.Second, time.Millisecond)
	p := srv.beginSessionPhase(context.Background(), "s")
	require.NotNil(t, p)
	require.NotNil(t, p.prof, "the armed profile started while the sampler ran")
	spin(50 * time.Millisecond)
	p.end(srv)
	prof := <-res
	require.NoError(t, <-errs)
	require.True(t, bytes.HasPrefix(prof, []byte{0x1f, 0x8b}), "not a gzipped pprof profile")

	shortCaptures(t)
	srv.watchThreshold.Store(int64(time.Second))
	srv.capture("watched", time.Second)
	srv.capturesMu.Lock()
	defer srv.capturesMu.Unlock()
	require.Len(t, srv.captures, 1)
	require.Empty(t, srv.captures[0].CPUProfileSkipped)
	require.NotEmpty(t, srv.captures[0].CPUProfile)
}

// While wcprof doesn't record, the sampler takes no profile: the engine's
// pprof endpoint and anyone debugging the engine have the profiler.
func TestEngineCPUOffTakesNoProfile(t *testing.T) {
	srv := &Server{}
	windows := engineCPUTest(t, srv, func() bool { return false })
	time.Sleep(200 * time.Millisecond)
	var buf bytes.Buffer
	require.NoError(t, pprof.StartCPUProfile(&buf), "the profiler is free")
	pprof.StopCPUProfile()
	select {
	case w := <-windows:
		t.Fatalf("a window while off: %+v", w)
	default:
	}
}

func TestSplitCPU(t *testing.T) {
	require.Equal(t, map[string]int64{unattributedCPU: 900}, splitCPU(900, nil))
	require.Equal(t, map[string]int64{"merge": 600, unattributedCPU: 300}, splitCPU(900, map[string]int64{"merge": 2, unattributedCPU: 1}))
	require.Equal(t, map[string]int64{"merge": 333, "lookup": 333, unattributedCPU: 334}, splitCPU(1000, map[string]int64{"merge": 1, "lookup": 1, unattributedCPU: 1}))
}

// A session watch's capture while a phase's armed profile holds the
// profiler doesn't wait for it: the capture's profile is skipped, as it was
// before the sampler, and the capture ends.
func TestEngineCPUWatchDoesntWaitForAnArmedProfile(t *testing.T) {
	srv := &Server{}
	engineCPUTest(t, srv, func() bool { return true })
	go func() { _, _ = srv.ProfilePhase(context.Background(), PhaseSession) }()
	require.Eventually(t, func() bool { return armedOf(srv) != nil }, 5*time.Second, time.Millisecond)
	p := srv.beginSessionPhase(context.Background(), "hung")
	require.NotNil(t, p)
	require.NotNil(t, p.prof)
	defer p.end(srv)

	shortCaptures(t)
	srv.watchThreshold.Store(int64(time.Second))
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.capture("watched", time.Second)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch's capture waited for the armed profile")
	}
	srv.capturesMu.Lock()
	defer srv.capturesMu.Unlock()
	require.Len(t, srv.captures, 1)
	require.NotEmpty(t, srv.captures[0].CPUProfileSkipped)
}

// Turning wcprof off ends the sampler's window within its poll, and frees
// the profiler.
func TestEngineCPUOffEndsTheWindow(t *testing.T) {
	srv := &Server{engineCPUWindowLen: time.Hour}
	var on atomic.Bool
	on.Store(true)
	engineCPUTest(t, srv, on.Load)
	// An hour-long window is running, or about to.
	time.Sleep(200 * time.Millisecond)
	on.Store(false)
	require.Eventually(t, func() bool {
		var buf bytes.Buffer
		if pprof.StartCPUProfile(&buf) != nil {
			return false
		}
		pprof.StopCPUProfile()
		return true
	}, 2*time.Second, 10*time.Millisecond, "the profiler is freed once wcprof is off")
}
