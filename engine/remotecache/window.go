package remotecache

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"runtime/metrics"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
)

// CacheWork is the cache's counts a restore's window reads at its start and
// at its end (RestoreWindow): the lookups' waits for the graph lock, the
// first uses by path, and the snapshot-sharing passes.
type CacheWork struct {
	LookupLockWaits dagql.LookupLockWaits
	FirstUse        dagql.FirstUseStats
	Sharing         dagql.SharingStats
}

// RestoreWindow is what the engine did between a file's arrival and its end,
// its last commit or what stopped it: what else used the engine while the
// file landed. Every count is the engine's, whatever it was for, and
// windows of files that land together overlap.
//
// CPU is the engine process's CPU time, user and system, and GCCPU the part
// the Go runtime estimates its garbage collector took, GCAssist the part of
// that the goroutines that allocated were made to do themselves, and
// GCCycles the collections that completed. SchedWaits and SchedWait are the goroutine
// waits the Go runtime sampled in its scheduler latency histogram, the time
// a runnable goroutine waited for a processor: how many, and their time
// estimated from the histogram's buckets. RunqWait is the time the engine
// process's threads were runnable and waiting for a CPU, as the kernel
// counts it (/proc/self/task/*/schedstat), when it could be read: a thread
// that exited in the window takes its share with it. LookupLockWaits are the
// lookups' waits for the graph lock begun in the window, by the kind of
// writer that let each in, counted and summed; FirstUses the first uses
// made in it, by path; Sharing the sharing passes, by kind of class.
type RestoreWindow struct {
	Wall            time.Duration
	CPU, GCCPU      time.Duration
	GCAssist        time.Duration
	GCCycles        int64
	SchedWaits      int64
	SchedWait       time.Duration
	RunqWait        time.Duration
	RunqWaitKnown   bool
	LookupLockWaits map[string]dagql.LockWaitStats
	FirstUses       map[string]dagql.FirstUsePath
	Sharing         map[string]dagql.SharePassStats
}

// restoreReading is the engine's counters at one moment, which two of
// RestoreWindow takes the difference of.
type restoreReading struct {
	at       time.Time
	cpu      time.Duration
	gcCPU    float64
	gcAssist float64
	gcCycles uint64
	sched    *metrics.Float64Histogram
	runq     time.Duration
	runqOK   bool
	work     CacheWork
}

// windowMetrics are the Go runtime's readings a restoreReading takes, in
// order.
var windowMetrics = []string{
	"/cpu/classes/gc/total:cpu-seconds",
	"/gc/cycles/total:gc-cycles",
	"/sched/latencies:seconds",
	"/cpu/classes/gc/mark/assist:cpu-seconds",
}

// readRestoreWindow reads the engine's counters now, with the cache's work.
func readRestoreWindow(work func() CacheWork) restoreReading {
	r := restoreReading{at: time.Now()}
	var ru unix.Rusage
	if unix.Getrusage(unix.RUSAGE_SELF, &ru) == nil {
		r.cpu = time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	}
	samples := make([]metrics.Sample, len(windowMetrics))
	for i, name := range windowMetrics {
		samples[i].Name = name
	}
	metrics.Read(samples)
	if v := samples[0].Value; v.Kind() == metrics.KindFloat64 {
		r.gcCPU = v.Float64()
	}
	if v := samples[1].Value; v.Kind() == metrics.KindUint64 {
		r.gcCycles = v.Uint64()
	}
	if v := samples[2].Value; v.Kind() == metrics.KindFloat64Histogram {
		r.sched = v.Float64Histogram()
	}
	if v := samples[3].Value; v.Kind() == metrics.KindFloat64 {
		r.gcAssist = v.Float64()
	}
	r.runq, r.runqOK = threadsRunqWait("/proc/self/task")
	if work != nil {
		r.work = work()
	}
	return r
}

// since is the window from start to r.
func (r restoreReading) since(start restoreReading) *RestoreWindow {
	w := &RestoreWindow{
		Wall:     r.at.Sub(start.at),
		CPU:      r.cpu - start.cpu,
		GCCPU:    time.Duration((r.gcCPU - start.gcCPU) * float64(time.Second)),
		GCAssist: time.Duration((r.gcAssist - start.gcAssist) * float64(time.Second)),
		GCCycles: int64(r.gcCycles - start.gcCycles),
	}
	w.SchedWaits, w.SchedWait = histogramSince(r.sched, start.sched)
	if r.runqOK && start.runqOK {
		w.RunqWait, w.RunqWaitKnown = r.runq-start.runq, true
	}
	w.LookupLockWaits = lockWaitsSince(r.work.LookupLockWaits, start.work.LookupLockWaits)
	w.FirstUses = firstUsesSince(r.work.FirstUse.Paths, start.work.FirstUse.Paths)
	w.Sharing = sharingSince(r.work.Sharing.Kinds, start.work.Sharing.Kinds)
	return w
}

// histogramSince is how many samples h gained since start, and their time
// estimated as each bucket's middle, or its finite edge for an unbounded
// one.
func histogramSince(h, start *metrics.Float64Histogram) (int64, time.Duration) {
	if h == nil || start == nil || len(h.Counts) != len(start.Counts) {
		return 0, 0
	}
	var n int64
	var seconds float64
	for i, count := range h.Counts {
		d := count - start.Counts[i]
		if d == 0 {
			continue
		}
		lo, hi := h.Buckets[i], h.Buckets[i+1]
		mid := (lo + hi) / 2
		switch {
		case math.IsInf(lo, -1):
			mid = hi
		case math.IsInf(hi, 1):
			mid = lo
		}
		n += int64(d)
		seconds += float64(d) * mid
	}
	return n, time.Duration(seconds * float64(time.Second))
}

// threadsRunqWait is the time a process's threads, those in the task
// directory dir, spent runnable and waiting for a CPU, summed over the
// threads alive now, from the second field of each one's schedstat; false
// when none could be read.
func threadsRunqWait(dir string) (time.Duration, bool) {
	tasks, err := os.ReadDir(dir)
	if err != nil {
		return 0, false
	}
	var total int64
	read := false
	for _, task := range tasks {
		stat, err := os.ReadFile(filepath.Join(dir, task.Name(), "schedstat"))
		if err != nil {
			continue
		}
		fields := bytes.Fields(stat)
		if len(fields) < 2 {
			continue
		}
		waited, err := strconv.ParseInt(string(fields[1]), 10, 64)
		if err != nil {
			continue
		}
		total += waited
		read = true
	}
	return time.Duration(total), read
}

func lockWaitsSince(end, start dagql.LookupLockWaits) map[string]dagql.LockWaitStats {
	var out map[string]dagql.LockWaitStats
	for _, window := range []struct {
		end, start map[string]dagql.LockWaitStats
	}{{end.Landing, start.Landing}, {end.Other, start.Other}} {
		for holder, stats := range window.end {
			waits := stats.Waits - window.start[holder].Waits
			if waits == 0 {
				continue
			}
			if out == nil {
				out = map[string]dagql.LockWaitStats{}
			}
			sum := out[holder]
			sum.Waits += waits
			sum.Total += stats.Total - window.start[holder].Total
			out[holder] = sum
		}
	}
	return out
}

func firstUsesSince(end, start map[string]dagql.FirstUsePath) map[string]dagql.FirstUsePath {
	var out map[string]dagql.FirstUsePath
	for path, uses := range end {
		prepared := uses.Prepared - start[path].Prepared
		if prepared == 0 {
			continue
		}
		if out == nil {
			out = map[string]dagql.FirstUsePath{}
		}
		out[path] = dagql.FirstUsePath{Prepared: prepared, Total: uses.Total - start[path].Total}
	}
	return out
}

func sharingSince(end, start map[string]dagql.SharePassStats) map[string]dagql.SharePassStats {
	var out map[string]dagql.SharePassStats
	for kind, stats := range end {
		was := start[kind]
		if stats.Passes == was.Passes {
			continue
		}
		if out == nil {
			out = map[string]dagql.SharePassStats{}
		}
		out[kind] = dagql.SharePassStats{
			Passes:       stats.Passes - was.Passes,
			Members:      stats.Members - was.Members,
			Slotted:      stats.Slotted - was.Slotted,
			Slots:        stats.Slots - was.Slots,
			Prepared:     stats.Prepared - was.Prepared,
			PrepareTotal: stats.PrepareTotal - was.PrepareTotal,
		}
	}
	return out
}
