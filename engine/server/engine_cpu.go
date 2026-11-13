package server

import (
	"bytes"
	"context"
	"log/slog"
	"runtime/pprof"
	"sort"
	"sync"
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

// engineCPUWindowDuration is how long one window samples.
var engineCPUWindowDuration = 10 * time.Second

// engineCPUIdlePoll is how often the sampler checks whether wcprof records.
var engineCPUIdlePoll = time.Second

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

// cpuProfiler lets one CPU profile run at a time between the engine CPU
// sampler and wcprof's own profiles, which take precedence: a take asks the
// sampler to end its window and waits for it.
type cpuProfiler struct {
	mu    sync.Mutex
	yield chan struct{}
	once  sync.Once
}

func (p *cpuProfiler) yieldCh() chan struct{} {
	p.once.Do(func() { p.yield = make(chan struct{}, 1) })
	return p.yield
}

// take waits for the profiler, asking the sampler to give it up.
func (p *cpuProfiler) take() {
	select {
	case p.yieldCh() <- struct{}{}:
	default:
	}
	p.mu.Lock()
}

func (p *cpuProfiler) release() { p.mu.Unlock() }

// startEngineCPU runs the sampler until ctx ends.
func (srv *Server) startEngineCPU(ctx context.Context) {
	on := srv.engineCPUOn
	if on == nil {
		on = wcprof.GloballyEnabled
	}
	go func() {
		for ctx.Err() == nil {
			if !on() {
				select {
				case <-time.After(engineCPUIdlePoll):
				case <-ctx.Done():
				}
				continue
			}
			w, ok := srv.sampleEngineCPU(ctx)
			if !ok {
				continue
			}
			logEngineCPU(w)
			if srv.testEngineCPUWindow != nil {
				srv.testEngineCPUWindow(w)
			}
		}
	}()
}

// sampleEngineCPU samples one window, and reports whether it got one.
func (srv *Server) sampleEngineCPU(ctx context.Context) (engineCPUWindow, bool) {
	srv.cpuProfile.mu.Lock()
	// A take that asked before this window began isn't for it.
	select {
	case <-srv.cpuProfile.yieldCh():
	default:
	}
	var buf bytes.Buffer
	before, ok := readProcessCPU()
	gcBefore := readGoGC()
	start := time.Now()
	if !ok || pprof.StartCPUProfile(&buf) != nil {
		srv.cpuProfile.mu.Unlock()
		select {
		case <-time.After(engineCPUIdlePoll):
		case <-ctx.Done():
		}
		return engineCPUWindow{}, false
	}
	timer := time.NewTimer(engineCPUWindowDuration)
	select {
	case <-timer.C:
	case <-srv.cpuProfile.yieldCh():
	case <-ctx.Done():
	}
	timer.Stop()
	pprof.StopCPUProfile()
	end := time.Now()
	after, _ := readProcessCPU()
	gcAfter := readGoGC()
	srv.cpuProfile.mu.Unlock()

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
