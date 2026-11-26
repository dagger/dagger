package server

import (
	"bytes"
	"cmp"
	"context"
	"log/slog"
	"runtime/pprof"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/pprof/profile"

	"github.com/dagger/dagger/engine/wcprof"
)

// While wcprof records engine-wide, the engine samples its own CPU with Go's
// CPU profiler in windows, and logs each window's CPU split by the kind of
// remote cache work its goroutines are labeled with (wcprof.CPUKindLabel).
// The window's total is the process's CPU time as the kernel counts it; the
// samples only split it. Samples of no kind are reported as unattributed.
// Only one CPU profile can run in a Go process: a window ends early, and the
// next waits, while one of wcprof's own profiles wants the profiler.

// engineCPUWindowDuration is how long one window samples, and
// engineCPUIdlePoll how often the sampler checks whether wcprof records.
// A test sets its server's engineCPUWindowLen and engineCPUPoll instead.
const (
	engineCPUWindowDuration = 10 * time.Second
	engineCPUIdlePoll       = time.Second
)

// unattributedCPU names a window's CPU that no kind's samples account for.
const unattributedCPU = "unattributed"

// engineCPUWindow is the engine's own CPU over one window.
type engineCPUWindow struct {
	Start, End time.Time
	// ProcessUS is the process's user and system CPU time in the window
	// (getrusage), the total the kinds split.
	ProcessUS int64
	// GCUS is the CPU the Go runtime estimates its GC used in the window,
	// beside the kinds: GC work runs on whatever goroutine triggers it.
	GCUS int64
	// Samples is the window's CPU profile samples, of every kind.
	Samples int64
	// Kinds is ProcessUS split by kind in proportion to the samples, with
	// unattributedCPU for the samples of no kind, or all of it when there
	// were none.
	Kinds map[string]int64
}

// cpuProfiler keeps the engine CPU sampler off the process's one CPU
// profiler while one of wcprof's own profiles wants it. wcprof's profiles
// start the profiler themselves, as they always have, and fail as they
// always have when another of them holds it; a pause only asks the sampler
// to end its window and start no other until every pause is resumed.
type cpuProfiler struct {
	// mu is held by the sampler while it profiles, and by pause while it
	// waits for that to end.
	mu     sync.Mutex
	paused atomic.Int32
	yield  chan struct{}
	once   sync.Once
}

func (p *cpuProfiler) yieldCh() chan struct{} {
	p.once.Do(func() { p.yield = make(chan struct{}, 1) })
	return p.yield
}

// pause ends the sampler's window, if one runs, and keeps it from starting
// another until resume. It waits only for the sampler, never for another of
// wcprof's profiles.
func (p *cpuProfiler) pause() {
	p.paused.Add(1)
	select {
	case p.yieldCh() <- struct{}{}:
	default:
	}
	p.mu.Lock()
	//nolint:staticcheck // the lock only waits out the sampler's window
	p.mu.Unlock()
}

func (p *cpuProfiler) resume() { p.paused.Add(-1) }

// startEngineCPU runs the sampler until ctx ends, and returns a func that
// waits for it to stop.
func (srv *Server) startEngineCPU(ctx context.Context) (wait func()) {
	on := srv.engineCPUOn
	if on == nil {
		on = wcprof.GloballyEnabled
	}
	window, poll := cmp.Or(srv.engineCPUWindowLen, engineCPUWindowDuration), cmp.Or(srv.engineCPUPoll, engineCPUIdlePoll)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			w, ok := srv.sampleEngineCPU(ctx, on, window, poll)
			if !ok {
				select {
				case <-time.After(poll):
				case <-ctx.Done():
				}
				continue
			}
			logEngineCPU(w)
			if srv.testEngineCPUWindow != nil {
				srv.testEngineCPUWindow(w)
			}
		}
	}()
	return func() { <-done }
}

// sampleEngineCPU samples one window, and reports whether it got one: not
// while wcprof is off or paused, nor while the profiler is in use. A window
// ends early when wcprof is turned off, or a pause asks for the profiler.
func (srv *Server) sampleEngineCPU(ctx context.Context, on func() bool, windowLen, pollEvery time.Duration) (engineCPUWindow, bool) {
	p := &srv.cpuProfile
	p.mu.Lock()
	defer p.mu.Unlock()
	// A pause that asked before this window began isn't for it.
	select {
	case <-p.yieldCh():
	default:
	}
	if !on() || p.paused.Load() > 0 || ctx.Err() != nil {
		return engineCPUWindow{}, false
	}
	var buf bytes.Buffer
	before, ok := readProcessCPU()
	gcBefore := readGoGC()
	start := time.Now()
	if !ok || pprof.StartCPUProfile(&buf) != nil {
		return engineCPUWindow{}, false
	}
	window := time.NewTimer(windowLen)
	poll := time.NewTicker(pollEvery)
wait:
	for {
		select {
		case <-window.C:
			break wait
		case <-p.yieldCh():
			break wait
		case <-ctx.Done():
			break wait
		case <-poll.C:
			if !on() {
				break wait
			}
		}
	}
	window.Stop()
	poll.Stop()
	pprof.StopCPUProfile()
	end := time.Now()
	after, _ := readProcessCPU()
	gcAfter := readGoGC()

	w := engineCPUWindow{
		Start:     start,
		End:       end,
		ProcessUS: (after.CPUUserUS + after.CPUSystemUS) - (before.CPUUserUS + before.CPUSystemUS),
		GCUS: (gcAfter.GCAssistUS + gcAfter.GCDedicatedUS + gcAfter.GCIdleUS + gcAfter.GCPauseUS) -
			(gcBefore.GCAssistUS + gcBefore.GCDedicatedUS + gcBefore.GCIdleUS + gcBefore.GCPauseUS),
	}
	counts := map[string]int64{}
	if prof, err := profile.Parse(&buf); err == nil {
		for _, s := range prof.Sample {
			if len(s.Value) == 0 {
				continue
			}
			kind := unattributedCPU
			if v := s.Label[wcprof.CPUKindLabel]; len(v) > 0 {
				kind = v[0]
			}
			counts[kind] += s.Value[0]
			w.Samples += s.Value[0]
		}
	}
	w.Kinds = splitCPU(w.ProcessUS, counts)
	return w, true
}

// splitCPU splits total in proportion to each kind's samples, the remainder
// of the rounding to unattributedCPU, as is all of it when there are no
// samples.
func splitCPU(total int64, counts map[string]int64) map[string]int64 {
	var all int64
	for _, n := range counts {
		all += n
	}
	out := map[string]int64{}
	if all == 0 || total <= 0 {
		out[unattributedCPU] = max(total, 0)
		return out
	}
	var given int64
	for kind, n := range counts {
		if kind == unattributedCPU {
			continue
		}
		us := total * n / all
		out[kind] = us
		given += us
	}
	out[unattributedCPU] = total - given
	return out
}

// logEngineCPU logs a window as one line: the window, the process's CPU, the
// GC's estimate, and each kind's share, in milliseconds.
func logEngineCPU(w engineCPUWindow) {
	kinds := make([]string, 0, len(w.Kinds))
	for kind := range w.Kinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	args := []any{
		"start", w.Start.UTC().Format(time.RFC3339Nano),
		"end", w.End.UTC().Format(time.RFC3339Nano),
		"process_ms", w.ProcessUS / 1000,
		"gc_ms", w.GCUS / 1000,
		"samples", w.Samples,
	}
	for _, kind := range kinds {
		args = append(args, "cpu_ms."+kind, w.Kinds[kind]/1000)
	}
	slog.Info("engine cpu", args...)
}
