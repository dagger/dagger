// gc-tuning measures how Go GC settings (GOGC, GOMEMLIMIT and a few dynamic
// controllers) trade CPU and throughput for memory in an engine-like workload:
// many goroutines churning through dagql-style calls (an ID built of strings
// and args, hashed and encoded, a result built and maybe kept), over a
// retained, pointer-heavy set of results whose size is held steady or ramped.
//
// Each run executes in its own process, so GOGC/GOMEMLIMIT come from the
// environment exactly as they would for the engine:
//
//	go run ./hack/gc-tuning sweep -scenario steady
//	go run ./hack/gc-tuning sweep -scenario ramp
//	GOGC=off GOMEMLIMIT=3GiB go run ./hack/gc-tuning run -live 2GiB -csv
//
// `run` prints one JSON summary line (and with -csv, a sample per interval on
// stderr); `sweep` runs a matrix of configurations and prints a table.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"hash/maphash"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gc-tuning run|sweep [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "sweep":
		sweepCmd(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "usage: gc-tuning run|sweep [flags]")
		os.Exit(2)
	}
}

// ---------------------------------------------------------------------------
// Workload

type callArg struct {
	name  string
	value string
}

// callID mimics a dagql call frame: a receiver chain, a field and args.
type callID struct {
	receiver *callID
	typ      string
	field    string
	args     []callArg
	digest   uint64
}

// result mimics a cached dagql result: its call, a digest, a payload and
// edges to other retained results, so the retained set is a pointer graph
// the GC has to trace rather than a flat byte slab.
type result struct {
	call    *callID
	digest  string
	payload []byte
	fields  map[string]string
	// deps share call frames from a fixed pool, as dagql IDs share common
	// receivers. (Pointing at other results would keep evicted results
	// reachable through their successors, so the live heap would drift.)
	deps [4]*callID
}

var fieldNames = []string{"container", "withExec", "directory", "file", "withMountedCache", "from", "withEnvVariable", "sync", "stdout", "export"}

var seed = maphash.MakeSeed()

type wireResult struct {
	ID     string            `json:"id"`
	Field  string            `json:"field"`
	Args   map[string]string `json:"args"`
	Digest string            `json:"digest"`
}

// call does one unit of engine-like work and returns the result built.
func call(rng *rand.Rand, parent *callID) *result {
	field := fieldNames[rng.IntN(len(fieldNames))]
	nargs := 2 + rng.IntN(5)
	id := &callID{receiver: parent, typ: "Container", field: field, args: make([]callArg, nargs)}
	for i := range id.args {
		id.args[i] = callArg{
			name:  fieldNames[(i+3)%len(fieldNames)] + "Arg",
			value: "value-" + strconv.FormatUint(rng.Uint64(), 36),
		}
	}
	// "encode" the ID and digest it, like recipe digests are computed
	var buf []byte
	for c := id; c != nil; c = c.receiver {
		buf = append(buf, c.typ...)
		buf = append(buf, '.')
		buf = append(buf, c.field...)
		for _, a := range c.args {
			buf = append(buf, a.name...)
			buf = append(buf, '=')
			buf = append(buf, a.value...)
		}
	}
	id.digest = maphash.Bytes(seed, buf)
	var d [8]byte
	binary.LittleEndian.PutUint64(d[:], id.digest)
	digest := fmt.Sprintf("xxh3:%x", d)

	// a response, JSON-encoded as the API would
	args := make(map[string]string, nargs)
	for _, a := range id.args {
		args[a.name] = a.value
	}
	wire, _ := json.Marshal(wireResult{ID: digest, Field: field, Args: args, Digest: digest})

	payload := make([]byte, 64+rng.IntN(448))
	copy(payload, wire)
	return &result{call: id, digest: digest, payload: payload, fields: args}
}

// store is the retained set: shards of results, replaced at random once full.
type store struct {
	shards []storeShard
	pool   []*callID
}

type storeShard struct {
	mu      sync.Mutex
	results []*result
	_       [40]byte
}

func newStore(n int) *store {
	rng := rand.New(rand.NewPCG(3, 4))
	pool := make([]*callID, 4096)
	for i := range pool {
		pool[i] = call(rng, nil).call
	}
	return &store{shards: make([]storeShard, n), pool: pool}
}

func (s *store) len() int {
	n := 0
	for i := range s.shards {
		s.shards[i].mu.Lock()
		n += len(s.shards[i].results)
		s.shards[i].mu.Unlock()
	}
	return n
}

// put keeps r. While the shard is under its target it grows; at target it
// replaces a random result (which becomes garbage), so long-lived objects
// die too, as cache entries are pruned and sessions end.
func (s *store) put(rng *rand.Rand, r *result, targetPerShard int) {
	sh := &s.shards[rng.IntN(len(s.shards))]
	for i := range r.deps {
		r.deps[i] = s.pool[rng.IntN(len(s.pool))]
	}
	sh.mu.Lock()
	if len(sh.results) < targetPerShard {
		sh.results = append(sh.results, r)
	} else if len(sh.results) > 0 {
		sh.results[rng.IntN(len(sh.results))] = r
	}
	sh.mu.Unlock()
}

// trim drops results over target (for a shrinking ramp).
func (s *store) trim(targetPerShard int) {
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		if len(sh.results) > targetPerShard {
			clear(sh.results[targetPerShard:])
			sh.results = sh.results[:targetPerShard]
		}
		sh.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// Metrics

var metricNames = []string{
	"/cpu/classes/gc/total:cpu-seconds",
	"/cpu/classes/user:cpu-seconds",
	"/cpu/classes/idle:cpu-seconds",
	"/cpu/classes/total:cpu-seconds",
	"/cpu/classes/scavenge/total:cpu-seconds",
	"/gc/heap/live:bytes",
	"/gc/heap/goal:bytes",
	"/gc/cycles/total:gc-cycles",
	"/memory/classes/total:bytes",
	"/memory/classes/heap/released:bytes",
	"/gc/gomemlimit:bytes",
	"/gc/gogc:percent",
	"/gc/limiter/last-enabled:gc-cycle",
	"/gc/heap/allocs:bytes",
}

type snap struct {
	t                                   time.Time
	calls                               int64
	gcCPU, userCPU, idleCPU, totCPU     float64
	scavCPU                             float64
	live, goal, cycles, mapped, release uint64
	limit                               int64
	gogc                                int64
	limiterCycle                        uint64
	rss                                 uint64
	allocs                              uint64
	cpu                                 float64
}

func readSnap(samples []metrics.Sample, calls int64) snap {
	metrics.Read(samples)
	s := snap{t: time.Now(), calls: calls, rss: rss(), cpu: cpuTime()}
	for _, m := range samples {
		switch m.Name {
		case "/cpu/classes/gc/total:cpu-seconds":
			s.gcCPU = m.Value.Float64()
		case "/cpu/classes/user:cpu-seconds":
			s.userCPU = m.Value.Float64()
		case "/cpu/classes/idle:cpu-seconds":
			s.idleCPU = m.Value.Float64()
		case "/cpu/classes/total:cpu-seconds":
			s.totCPU = m.Value.Float64()
		case "/cpu/classes/scavenge/total:cpu-seconds":
			s.scavCPU = m.Value.Float64()
		case "/gc/heap/live:bytes":
			s.live = m.Value.Uint64()
		case "/gc/heap/goal:bytes":
			s.goal = m.Value.Uint64()
		case "/gc/cycles/total:gc-cycles":
			s.cycles = m.Value.Uint64()
		case "/memory/classes/total:bytes":
			s.mapped = m.Value.Uint64()
		case "/memory/classes/heap/released:bytes":
			s.release = m.Value.Uint64()
		case "/gc/gomemlimit:bytes":
			s.limit = int64(m.Value.Uint64())
		case "/gc/gogc:percent":
			s.gogc = int64(m.Value.Uint64())
		case "/gc/limiter/last-enabled:gc-cycle":
			s.limiterCycle = m.Value.Uint64()
		case "/gc/heap/allocs:bytes":
			s.allocs = m.Value.Uint64()
		}
	}
	return s
}

// cpuTime is the CPU time the process actually consumed (user+system), which,
// unlike runtime/metrics' P-time estimates, is not inflated when the host is
// busy and the OS deschedules running threads.
func cpuTime() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()).Seconds()
}

func rss() uint64 {
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

// ---------------------------------------------------------------------------
// Controllers

// limitController sets the memory limit after each GC to live×(1+frac),
// at least live+minHeadroom, at most ceiling: "collect when allocation strays
// far ahead of what is in use", with an absolute cap on the overshoot.
func limitController(frac float64, minHeadroom, maxHeadroom, ceiling int64, stop <-chan struct{}) {
	samples := []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}}
	var lastCycle uint64
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		metrics.Read(samples)
		if c := samples[1].Value.Uint64(); c == lastCycle {
			continue
		} else {
			lastCycle = c
		}
		live := int64(samples[0].Value.Uint64())
		head := int64(float64(live) * frac)
		head = max(head, minHeadroom)
		if maxHeadroom > 0 {
			head = min(head, maxHeadroom)
		}
		limit := live + head
		if ceiling > 0 {
			limit = min(limit, ceiling)
		}
		debug.SetMemoryLimit(limit)
	}
}

// gogcController sets GOGC after each GC so the heap may grow by at most
// maxHeadroom bytes past the live heap (and by at most maxPercent), at least
// minPercent: a byte cap on the overshoot without a memory limit.
func gogcController(minPercent, maxPercent int, maxHeadroom int64, stop <-chan struct{}) {
	samples := []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}}
	var lastCycle uint64
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		metrics.Read(samples)
		if c := samples[1].Value.Uint64(); c == lastCycle {
			continue
		} else {
			lastCycle = c
		}
		live := max(int64(samples[0].Value.Uint64()), 1)
		pct := int(float64(maxHeadroom) / float64(live) * 100)
		pct = min(max(pct, minPercent), maxPercent)
		debug.SetGCPercent(pct)
	}
}

// ---------------------------------------------------------------------------
// run

type summary struct {
	Name       string  `json:"name"`
	Scenario   string  `json:"scenario"`
	GOGC       string  `json:"gogc"`
	GOMEMLIMIT string  `json:"gomemlimit"`
	Controller string  `json:"controller"`
	Procs      int     `json:"procs"`
	Workers    int     `json:"workers"`
	Seconds    float64 `json:"seconds"`
	CallsPerS  float64 `json:"calls_per_s"`
	// GC CPU as a fraction of all CPU the process used (not idle).
	GCFrac float64 `json:"gc_frac"`
	// GC cores: GC CPU seconds per wall second.
	GCCores      float64 `json:"gc_cores"`
	CPUCores     float64 `json:"cpu_cores"`
	AllocMBps    float64 `json:"alloc_mb_per_s"`
	ScavCores    float64 `json:"scav_cores"`
	GCPerS       float64 `json:"gcs_per_s"`
	LiveAvg      uint64  `json:"live_avg"`
	LiveMax      uint64  `json:"live_max"`
	RSSAvg       uint64  `json:"rss_avg"`
	RSSMax       uint64  `json:"rss_max"`
	MappedMax    uint64  `json:"mapped_max"`
	LimiterHit   bool    `json:"limiter_hit"`
	Phases       []phase `json:"phases,omitempty"`
	PeakRSSRatio float64 `json:"peak_rss_over_live"`
}

// phase aggregates samples of a ramp by live heap relative to the limit.
type phase struct {
	Label     string  `json:"label"`
	Seconds   float64 `json:"seconds"`
	CallsPerS float64 `json:"calls_per_s"`
	GCFrac    float64 `json:"gc_frac"`
	CPUCores  float64 `json:"cpu_cores"`
	GCPerS    float64 `json:"gcs_per_s"`
	RSSMax    uint64  `json:"rss_max"`
	LiveMax   uint64  `json:"live_max"`
}

func parseBytes(s string) int64 {
	if s == "" {
		return 0
	}
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1}} {
		if strings.HasSuffix(s, u.suf) {
			s = strings.TrimSuffix(s, u.suf)
			mult = u.m
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(fmt.Sprintf("bad size %q", s))
	}
	return int64(f * float64(mult))
}

func gib(b uint64) string { return fmt.Sprintf("%.2f", float64(b)/(1<<30)) }

// calibrate measures the live bytes one retained result costs.
func calibrate() int64 {
	rng := rand.New(rand.NewPCG(1, 2))
	s := newStore(64)
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	const n = 200000
	for range n {
		s.put(rng, call(rng, nil), math.MaxInt)
	}
	runtime.GC()
	runtime.ReadMemStats(&b)
	runtime.KeepAlive(s)
	return int64(b.HeapAlloc-a.HeapAlloc) / n
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	name := fs.String("name", "", "label for the summary")
	scenario := fs.String("scenario", "steady", "steady: hold -live; ramp: grow live from -live to -ramp-to over -dur")
	liveFlag := fs.String("live", "1GiB", "retained live heap target (steady) or ramp start")
	rampTo := fs.String("ramp-to", "", "ramp end live heap")
	dur := fs.Duration("dur", 20*time.Second, "measured duration")
	warm := fs.Duration("warm", 3*time.Second, "settle time after reaching the live target, not measured")
	workers := fs.Int("workers", runtime.GOMAXPROCS(0), "worker goroutines")
	retain := fs.Float64("retain", 0.02, "fraction of calls whose result replaces a retained one at steady state")
	interval := fs.Duration("interval", 250*time.Millisecond, "sample interval")
	csv := fs.Bool("csv", false, "print samples on stderr")
	ctl := fs.String("controller", "", "dynamic controller: limit:<frac>:<minHead>:<maxHead>:<ceiling> or gogc:<min%>:<max%>:<maxHead>")
	rate := fs.Float64("rate", 0, "cap calls per second across workers (0: as fast as possible)")
	freeEvery := fs.Duration("free-os-memory", 0, "call debug.FreeOSMemory this often")
	limitForPhases := fs.String("phase-limit", "", "limit to bucket ramp samples against (default: GOMEMLIMIT)")
	fs.Parse(args)

	unit := calibrate()
	live := parseBytes(*liveFlag)
	to := live
	if *rampTo != "" {
		to = parseBytes(*rampTo)
	}
	const nshards = 256
	targetFor := func(bytes int64) int { return int(bytes/unit) / nshards }

	stop := make(chan struct{})
	ctlName := ""
	if *ctl != "" {
		ctlName = *ctl
		p := strings.Split(*ctl, ":")
		switch p[0] {
		case "limit":
			frac, _ := strconv.ParseFloat(p[1], 64)
			go limitController(frac, parseBytes(p[2]), parseBytes(p[3]), parseBytes(p[4]), stop)
		case "gogc":
			lo, _ := strconv.Atoi(p[1])
			hi, _ := strconv.Atoi(p[2])
			go gogcController(lo, hi, parseBytes(p[3]), stop)
		default:
			panic("unknown controller " + p[0])
		}
	}
	if *freeEvery > 0 {
		go func() {
			t := time.NewTicker(*freeEvery)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					debug.FreeOSMemory()
				}
			}
		}()
	}

	s := newStore(nshards)
	var target atomic.Int64
	target.Store(int64(targetFor(live)))
	var calls atomic.Int64
	var done, paceOn atomic.Bool
	var wg sync.WaitGroup
	for w := range *workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(w), 99))
			var parent *callID
			local := int64(0)
			perWorker := *rate / float64(*workers)
			paceStart := time.Now()
			paced := 0.0
			for !done.Load() {
				r := call(rng, parent)
				if rng.IntN(8) == 0 {
					parent = r.call // short receiver chains, reset now and then
				}
				if rng.IntN(16) == 0 {
					parent = nil
				}
				tps := int(target.Load())
				if rng.Float64() < *retain {
					s.put(rng, r, tps)
				} else {
					// grow toward the target quickly when under it
					sh := &s.shards[rng.IntN(nshards)]
					sh.mu.Lock()
					under := len(sh.results) < tps
					sh.mu.Unlock()
					if under {
						s.put(rng, r, tps)
					}
				}
				local++
				if local == 64 {
					calls.Add(local)
					local = 0
					if perWorker > 0 && paceOn.Load() {
						if paced == 0 {
							paceStart = time.Now()
						}
						paced += 64
						due := paceStart.Add(time.Duration(paced / perWorker * float64(time.Second)))
						if d := time.Until(due); d > 0 {
							time.Sleep(d)
						}
					}
				}
			}
			calls.Add(local)
		}()
	}

	// fill to the starting target
	want := targetFor(live) * nshards
	for s.len() < want*98/100 {
		time.Sleep(50 * time.Millisecond)
	}
	samples := make([]metrics.Sample, len(metricNames))
	for i, n := range metricNames {
		samples[i].Name = n
	}
	// Settle, correcting the per-result estimate from the measured live heap
	// (evicted results' call frames stay shared, so steady state costs more
	// than the calibration's fresh results).
	paceOn.Store(true)
	settle := time.Now()
	lastCycles := readSnap(samples, 0).cycles
	for time.Since(settle) < *warm {
		time.Sleep(250 * time.Millisecond)
		x := readSnap(samples, 0)
		if x.cycles == lastCycles {
			continue
		}
		lastCycles = x.cycles
		if n := s.len(); n > 0 {
			unit = max(int64(x.live)/int64(n), 1)
			target.Store(int64(targetFor(live)))
			s.trim(int(target.Load()))
		}
	}
	var phaseLimit int64
	if *limitForPhases != "" {
		phaseLimit = parseBytes(*limitForPhases)
	}
	start := readSnap(samples, calls.Load())
	if phaseLimit == 0 && start.limit < math.MaxInt64 {
		phaseLimit = start.limit
	}
	prev := start
	var all []snap
	if *csv {
		fmt.Fprintln(os.Stderr, "t_s,calls_per_s,cpu_cores,gc_frac,scav_cores,gcs,live_gib,goal_gib,rss_gib,limit_gib,gogc,limiter_cycle,target_gib")
	}
	tick := time.NewTicker(*interval)
	for {
		<-tick.C
		el := time.Since(start.t)
		if *scenario == "ramp" {
			f := min(float64(el)/float64(*dur), 1)
			b := float64(live) + f*float64(to-live)
			target.Store(int64(targetFor(int64(b))))
			if to < live {
				s.trim(int(target.Load()))
			}
		}
		cur := readSnap(samples, calls.Load())
		all = append(all, cur)
		if *csv {
			dt := cur.t.Sub(prev.t).Seconds()
			used := (cur.totCPU - cur.idleCPU) - (prev.totCPU - prev.idleCPU)
			fmt.Fprintf(os.Stderr, "%.2f,%.0f,%.2f,%.3f,%.2f,%d,%s,%s,%s,%s,%d,%d,%s\n",
				cur.t.Sub(start.t).Seconds(), float64(cur.calls-prev.calls)/dt,
				(cur.cpu-prev.cpu)/dt,
				(cur.gcCPU-prev.gcCPU)/max(used, 1e-9), (cur.scavCPU-prev.scavCPU)/dt, cur.cycles-prev.cycles,
				gib(cur.live), gib(cur.goal), gib(cur.rss), gib(uint64(min(cur.limit, 1<<62))), cur.gogc, cur.limiterCycle,
				gib(uint64(target.Load()*nshards*unit)))
		}
		prev = cur
		if el >= *dur {
			break
		}
	}
	tick.Stop()
	done.Store(true)
	wg.Wait()
	close(stop)

	end := all[len(all)-1]
	sum := summary{
		Name: *name, Scenario: *scenario,
		GOGC: os.Getenv("GOGC"), GOMEMLIMIT: os.Getenv("GOMEMLIMIT"), Controller: ctlName,
		Procs: runtime.GOMAXPROCS(0), Workers: *workers,
	}
	wall := end.t.Sub(start.t).Seconds()
	sum.Seconds = wall
	sum.CallsPerS = float64(end.calls-start.calls) / wall
	used := (end.totCPU - end.idleCPU) - (start.totCPU - start.idleCPU)
	sum.GCFrac = (end.gcCPU - start.gcCPU) / used
	sum.GCCores = (end.gcCPU - start.gcCPU) / wall
	sum.CPUCores = (end.cpu - start.cpu) / wall
	sum.AllocMBps = float64(end.allocs-start.allocs) / wall / 1e6
	sum.ScavCores = (end.scavCPU - start.scavCPU) / wall
	sum.GCPerS = float64(end.cycles-start.cycles) / wall
	sum.LimiterHit = end.limiterCycle > start.limiterCycle
	var liveSum, rssSum uint64
	for _, x := range all {
		liveSum += x.live
		rssSum += x.rss
		sum.LiveMax = max(sum.LiveMax, x.live)
		sum.RSSMax = max(sum.RSSMax, x.rss)
		sum.MappedMax = max(sum.MappedMax, x.mapped-x.release)
	}
	sum.LiveAvg = liveSum / uint64(len(all))
	sum.RSSAvg = rssSum / uint64(len(all))
	sum.PeakRSSRatio = float64(sum.RSSMax) / float64(sum.LiveMax)

	if *scenario == "ramp" && phaseLimit > 0 {
		type acc struct {
			secs, calls, gc, used, gcs, cpu float64
			rss, live                  uint64
		}
		labels := []string{"<50%", "50-80%", "80-95%", "95-105%", "105-120%", ">120%"}
		bounds := []float64{0.5, 0.8, 0.95, 1.05, 1.2, math.Inf(1)}
		accs := make([]acc, len(labels))
		p := start
		for _, x := range all {
			ratio := float64(x.live) / float64(phaseLimit)
			i := sort.SearchFloat64s(bounds, ratio)
			if i >= len(bounds) {
				i = len(bounds) - 1
			}
			a := &accs[i]
			a.secs += x.t.Sub(p.t).Seconds()
			a.calls += float64(x.calls - p.calls)
			a.gc += x.gcCPU - p.gcCPU
			a.used += (x.totCPU - x.idleCPU) - (p.totCPU - p.idleCPU)
			a.gcs += float64(x.cycles - p.cycles)
			a.cpu += x.cpu - p.cpu
			a.rss = max(a.rss, x.rss)
			a.live = max(a.live, x.live)
			p = x
		}
		for i, a := range accs {
			if a.secs == 0 {
				continue
			}
			sum.Phases = append(sum.Phases, phase{
				Label: labels[i], Seconds: a.secs, CallsPerS: a.calls / a.secs,
				GCFrac: a.gc / max(a.used, 1e-9), CPUCores: a.cpu / a.secs, GCPerS: a.gcs / a.secs, RSSMax: a.rss, LiveMax: a.live,
			})
		}
	}
	out, _ := json.Marshal(sum)
	fmt.Println(string(out))
}

// ---------------------------------------------------------------------------
// sweep

type config struct {
	name string
	env  []string
	args []string
}

func sweepCmd(args []string) {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	scenario := fs.String("scenario", "steady", "steady or ramp")
	liveFlag := fs.String("live", "2GiB", "steady live heap / ramp start")
	limitFlag := fs.String("limit", "3GiB", "ramp: the memory limit the live heap ramps past")
	rampTo := fs.String("ramp-to", "4.5GiB", "ramp: end live heap")
	dur := fs.Duration("dur", 20*time.Second, "measured duration per run")
	procs := fs.Int("procs", 8, "GOMAXPROCS for each run")
	workers := fs.Int("workers", 0, "workers (default: procs)")
	reps := fs.Int("reps", 1, "repetitions of each config (interleaved)")
	only := fs.String("only", "", "semicolon-separated config names to run")
	rate := fs.Float64("rate", 0, "cap calls per second (0: saturate the CPUs)")
	warm := fs.Duration("warm", 4*time.Second, "settle time per run")
	csv := fs.Bool("csv", false, "print each run's samples")
	fs.Parse(args)
	if *workers == 0 {
		*workers = *procs
	}
	live := parseBytes(*liveFlag)
	gb := func(f float64) string { return fmt.Sprintf("%dMiB", int64(f*float64(live))>>20) }

	var cfgs []config
	switch *scenario {
	case "steady":
		cfgs = []config{
			{name: "default (GOGC=100)"},
			{name: "GOGC=75", env: []string{"GOGC=75"}},
			{name: "GOGC=50", env: []string{"GOGC=50"}},
			{name: "GOGC=25", env: []string{"GOGC=25"}},
			{name: "GOGC=100+limit 4x live (ceiling, idle)", env: []string{"GOMEMLIMIT=" + gb(4)}},
			{name: "GOGC=100+limit 1.5x live (ceiling, active)", env: []string{"GOMEMLIMIT=" + gb(1.5)}},
			{name: "GOGC=100+limit 1.2x live (ceiling, tight)", env: []string{"GOMEMLIMIT=" + gb(1.2)}},
			{name: "GOGC=off+limit 2x live", env: []string{"GOGC=off", "GOMEMLIMIT=" + gb(2)}},
			{name: "GOGC=off+limit 1.5x live", env: []string{"GOGC=off", "GOMEMLIMIT=" + gb(1.5)}},
			{name: "GOGC=off+limit 1.2x live", env: []string{"GOGC=off", "GOMEMLIMIT=" + gb(1.2)}},
			{name: "ctl limit=live*1.5 (min+256MiB)", args: []string{"-controller", "limit:0.5:256MiB:0:0"}},
			{name: "ctl limit=live+min(live,1GiB)", args: []string{"-controller", "limit:1.0:256MiB:1GiB:0"}},
			{name: "ctl GOGC=clamp(1GiB/live,25,100)", args: []string{"-controller", "gogc:25:100:1GiB"}},
			{name: "FreeOSMemory every 5s", args: []string{"-free-os-memory", "5s"}},
		}
	case "ramp":
		lim := *limitFlag
		phase := []string{"-phase-limit", lim}
		cfgs = []config{
			{name: "default (no limit)", args: phase},
			{name: "GOGC=100+GOMEMLIMIT", env: []string{"GOMEMLIMIT=" + lim}},
			{name: "GOGC=50+GOMEMLIMIT", env: []string{"GOGC=50", "GOMEMLIMIT=" + lim}},
			{name: "GOGC=off+GOMEMLIMIT", env: []string{"GOGC=off", "GOMEMLIMIT=" + lim}},
			{name: "ctl limit=live*1.5 ceil", args: append([]string{"-controller", "limit:0.5:256MiB:0:" + lim}, phase...)},
		}
	default:
		panic("unknown scenario")
	}
	if *only != "" {
		keep := map[string]bool{}
		for _, n := range strings.Split(*only, ";") {
			keep[strings.TrimSpace(n)] = true
		}
		var sel []config
		for _, c := range cfgs {
			if keep[c.name] {
				sel = append(sel, c)
			}
		}
		cfgs = sel
	}

	self, err := os.Executable()
	if err != nil {
		panic(err)
	}
	results := make([][]summary, len(cfgs))
	for rep := range *reps {
		for i, c := range cfgs {
			a := []string{"run", "-name", c.name, "-scenario", *scenario, "-live", *liveFlag,
				"-dur", dur.String(), "-workers", strconv.Itoa(*workers),
				"-rate", strconv.FormatFloat(*rate, 'f', -1, 64), "-warm", warm.String()}
			if *scenario == "ramp" {
				a = append(a, "-ramp-to", *rampTo)
			}
			if *csv {
				a = append(a, "-csv")
			}
			a = append(a, c.args...)
			cmd := exec.Command(self, a...)
			env := []string{}
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "GOGC=") && !strings.HasPrefix(e, "GOMEMLIMIT=") && !strings.HasPrefix(e, "GOMAXPROCS=") {
					env = append(env, e)
				}
			}
			cmd.Env = append(append(env, "GOMAXPROCS="+strconv.Itoa(*procs)), c.env...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s (rep %d): %v\n%s\n", c.name, rep, err, stderr.String())
				continue
			}
			sc := bufio.NewScanner(bytes.NewReader(out))
			var sum summary
			for sc.Scan() {
				if err := json.Unmarshal(sc.Bytes(), &sum); err == nil {
					break
				}
			}
			results[i] = append(results[i], sum)
			if *csv {
				fmt.Fprintf(os.Stderr, "# %s rep %d\n%s", c.name, rep, stderr.String())
			}
			fmt.Fprintf(os.Stderr, "done %s rep %d: %.0f calls/s cpu %.2f cores gc %.1f%% (%.2f cores) rss %s\n", c.name, rep, sum.CallsPerS, sum.CPUCores, sum.GCFrac*100, sum.GCCores, gib(sum.RSSMax))
		}
	}

	if *scenario == "steady" {
		var base float64
		if len(results[0]) > 0 {
			base = median(results[0], func(s summary) float64 { return s.CallsPerS })
		}
		baseCPU := median(results[0], func(s summary) float64 { return s.CPUCores })
		fmt.Printf("steady: live %s, GOMAXPROCS %d, %d workers, rate %.0f calls/s (0 = saturated), %s per run, %d reps (medians)\n\n", *liveFlag, *procs, *workers, *rate, dur, *reps)
		fmt.Println("| config | calls/s | vs default | alloc MB/s | CPU cores | CPU vs default | GC CPU % | GC cores | GCs/s | live GiB | RSS avg GiB | RSS max GiB | RSS max/live |")
		fmt.Println("|---|---|---|---|---|---|---|---|---|---|---|---|---|")
		for i, c := range cfgs {
			rs := results[i]
			if len(rs) == 0 {
				continue
			}
			cps := median(rs, func(s summary) float64 { return s.CallsPerS })
			cpu := median(rs, func(s summary) float64 { return s.CPUCores })
			fmt.Printf("| %s | %.0f | %+.1f%% | %.0f | %.2f | %+.1f%% | %.1f | %.2f | %.1f | %.2f | %.2f | %.2f | %.2f |\n", c.name,
				cps, (cps/base-1)*100, median(rs, func(s summary) float64 { return s.AllocMBps }), cpu, (cpu/baseCPU-1)*100,
				median(rs, func(s summary) float64 { return s.GCFrac * 100 }),
				median(rs, func(s summary) float64 { return s.GCCores }),
				median(rs, func(s summary) float64 { return s.GCPerS }),
				median(rs, func(s summary) float64 { return float64(s.LiveAvg) / (1 << 30) }),
				median(rs, func(s summary) float64 { return float64(s.RSSAvg) / (1 << 30) }),
				median(rs, func(s summary) float64 { return float64(s.RSSMax) / (1 << 30) }),
				median(rs, func(s summary) float64 { return s.PeakRSSRatio }))
		}
		return
	}
	fmt.Printf("ramp: live %s -> %s over %s against limit %s, GOMAXPROCS %d, %d workers\n\n", *liveFlag, *rampTo, dur, *limitFlag, *procs, *workers)
	fmt.Println("| config | live/limit | seconds | calls/s | CPU cores | GC CPU % | GCs/s | live max GiB | RSS max GiB |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|")
	for i, c := range cfgs {
		for _, s := range results[i] {
			for _, p := range s.Phases {
				fmt.Printf("| %s | %s | %.1f | %.0f | %.2f | %.1f | %.1f | %.2f | %.2f |\n", c.name, p.Label, p.Seconds, p.CallsPerS, p.CPUCores, p.GCFrac*100, p.GCPerS, float64(p.LiveMax)/(1<<30), float64(p.RSSMax)/(1<<30))
			}
		}
	}
}

func median(rs []summary, f func(summary) float64) float64 {
	v := make([]float64, len(rs))
	for i, r := range rs {
		v[i] = f(r)
	}
	sort.Float64s(v)
	if len(v) == 0 {
		return math.NaN()
	}
	if len(v)%2 == 1 {
		return v[len(v)/2]
	}
	return (v[len(v)/2-1] + v[len(v)/2]) / 2
}
