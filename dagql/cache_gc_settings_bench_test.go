package dagql

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// BenchmarkCacheGCSettings measures what Go GC settings cost the dagql cache's
// call churn: many short sessions each making a burst of distinct calls and
// releasing them, over a long session that keeps a large set of results live
// (as a long-running engine's sessions and cache do). Each sub-benchmark sets
// GOGC and/or the memory limit relative to the measured live heap, and
// reports, beside ns/op, the GC's share of CPU, collections per 1k calls and
// the peak RSS over the live heap.
//
// Run with a fixed GOMAXPROCS, one setting at a time if the host is noisy:
//
//	go test ./dagql -run '^$' -bench BenchmarkCacheGCSettings -cpu 8 -benchtime 400000x -count 3
//
// DAGQL_GC_BENCH_RETAINED sets how many results the long session holds
// (default 100000, ~0.5 GB live).
func BenchmarkCacheGCSettings(b *testing.B) {
	retained := 100000
	if v := os.Getenv("DAGQL_GC_BENCH_RETAINED"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			b.Fatal(err)
		}
		retained = n
	}
	type setting struct {
		name  string
		gogc  int     // -1: off
		limit float64 // memory limit as a multiple of the live heap; 0: none
	}
	for _, s := range []setting{
		{name: "default", gogc: 100},
		{name: "gogc=50", gogc: 50},
		{name: "ceiling-4x-live", gogc: 100, limit: 4},
		{name: "ceiling-1.5x-live", gogc: 100, limit: 1.5},
		{name: "gogc=off,limit-2x-live", gogc: -1, limit: 2},
	} {
		b.Run(s.name, func(b *testing.B) {
			ctx := b.Context()
			c, err := NewCache(ctx, "", nil, nil)
			if err != nil {
				b.Fatal(err)
			}
			payload := strings.Repeat("x", 1024)
			call := func(session string, key string) {
				frame := &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(NewString("").Type()),
					Field: "gcbench",
					Args: []*ResultCallArg{{
						Name:  "k",
						Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: key},
					}},
				}
				_, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
					return NewResultForCall(NewString(string([]byte(payload))), frame)
				})
				if err != nil {
					b.Error(err)
				}
			}
			for i := range retained {
				call("long-session", "retained-"+strconv.Itoa(i))
			}
			runtime.GC()
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			live := ms.HeapAlloc

			oldGOGC := debug.SetGCPercent(s.gogc)
			oldLimit := debug.SetMemoryLimit(math.MaxInt64)
			if s.limit > 0 {
				debug.SetMemoryLimit(int64(float64(live) * s.limit))
			}
			defer func() {
				debug.SetGCPercent(oldGOGC)
				debug.SetMemoryLimit(oldLimit)
			}()

			samples := []metrics.Sample{
				{Name: "/cpu/classes/gc/total:cpu-seconds"},
				{Name: "/cpu/classes/total:cpu-seconds"},
				{Name: "/cpu/classes/idle:cpu-seconds"},
				{Name: "/gc/cycles/total:gc-cycles"},
			}
			metrics.Read(samples)
			gc0, tot0, idle0, cyc0 := samples[0].Value.Float64(), samples[1].Value.Float64(), samples[2].Value.Float64(), samples[3].Value.Uint64()
			var peakRSS atomic.Uint64
			var seq, sessions atomic.Int64

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				const burst = 64
				session := ""
				n := 0
				for pb.Next() {
					if n%burst == 0 {
						if session != "" {
							if err := c.ReleaseSession(ctx, session); err != nil {
								b.Error(err)
							}
						}
						session = fmt.Sprintf("short-%d", sessions.Add(1))
						if rss := benchRSS(); rss > peakRSS.Load() {
							peakRSS.Store(rss)
						}
					}
					call(session, "churn-"+strconv.FormatInt(seq.Add(1), 10))
					n++
				}
				if session != "" {
					if err := c.ReleaseSession(ctx, session); err != nil {
						b.Error(err)
					}
				}
			})
			b.StopTimer()

			metrics.Read(samples)
			gc := samples[0].Value.Float64() - gc0
			used := (samples[1].Value.Float64() - tot0) - (samples[2].Value.Float64() - idle0)
			cycles := samples[3].Value.Uint64() - cyc0
			b.ReportMetric(100*gc/used, "gc-cpu-%")
			b.ReportMetric(float64(cycles)*1000/float64(b.N), "gcs/1kcalls")
			b.ReportMetric(float64(live)/(1<<20), "live-MiB")
			b.ReportMetric(float64(peakRSS.Load())/float64(live), "peak-rss/live")
			runtime.KeepAlive(c)
		})
	}
}

func benchRSS() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, _ := strconv.ParseUint(f[1], 10, 64)
	return pages * uint64(os.Getpagesize())
}
