package wcprof

import "context"

// Usage is a resource reading attached to an op: the CPU time and peak memory
// of one scope (see the UsageScope constants), read once the scope's work is
// done.
type Usage struct {
	CPUUserUS   int64
	CPUSystemUS int64
	// MemPeakBytes is the scope's peak memory, 0 when it couldn't be read.
	MemPeakBytes int64
	// CPUIowaitUS, CPUStealUS and CPUIdleUS are a whole machine's CPU time
	// waiting for I/O, taken by its hypervisor for other guests, and idle,
	// summed over its cores. Only machine scopes have them.
	CPUIowaitUS int64
	CPUStealUS  int64
	CPUIdleUS   int64
	// MinorFaults and MajorFaults are the scope's page faults: those served
	// without I/O, and those that needed it. HugeFaults are those that
	// allocated a transparent huge page. Process and exec scopes have them.
	MinorFaults int64
	MajorFaults int64
	HugeFaults  int64
	// CPUStallUS and MemStallUS are the time any of the scope's tasks
	// stalled waiting for a CPU and for memory (its cgroup's cpu.pressure and
	// memory.pressure "some" totals). Only exec scopes have them.
	CPUStallUS int64
	MemStallUS int64
	// GoAllocBytes, GoGCCycles and GoGCCPUUS are a Go process's cumulative
	// heap allocations, its completed GC cycles, and the CPU time its runtime
	// estimates the GC used. Only the engine process scope has them.
	GoAllocBytes int64
	GoGCCycles   int64
	GoGCCPUUS    int64
	// IOReadBytes and IOWriteBytes are the bytes a cgroup scope read from
	// and wrote to block devices.
	IOReadBytes  int64
	IOWriteBytes int64
	// MemAnonBytes, MemFileBytes and MemHugeBytes are a cgroup's memory at
	// the reading's time: anonymous memory, file pages, and the part of both
	// in transparent huge pages. Only sample scopes have them.
	MemAnonBytes int64
	MemFileBytes int64
	MemHugeBytes int64
	// GCAssistUS, GCDedicatedUS, GCIdleUS and GCPauseUS are the CPU time a
	// Go process's GC took, by class, as the runtime estimates it: mark
	// assists by allocating goroutines, dedicated and idle mark workers, and
	// stop-the-world pauses, counted as the pause's duration times GOMAXPROCS
	// rather than processor time used. HeapLiveBytes is the heap the last GC
	// marked live, and HeapGoalBytes the heap size the next GC aims for.
	// ScanHeapBytes and ScanGlobalsBytes are the scannable heap and globals
	// now, and ScanStackBytes the stack the last GC scanned. Only Go runtime
	// sample scopes have them; see runtime/metrics for the definitions.
	GCAssistUS       int64
	GCDedicatedUS    int64
	GCIdleUS         int64
	GCPauseUS        int64
	HeapLiveBytes    int64
	HeapGoalBytes    int64
	ScanHeapBytes    int64
	ScanStackBytes   int64
	ScanGlobalsBytes int64
}

// Usage scopes of an exec.
const (
	// UsageScopeExecCgroup is the exec's resource cgroup: the workload and the
	// mount helpers that run beside it, read after mount teardown.
	UsageScopeExecCgroup = "exec_cgroup"
	// UsageScopeMountHelpers is the mount helpers' own cgroup inside the
	// exec's resource cgroup, read with it, and only when a helper ran: the
	// workload's usage is the exec cgroup's less this.
	UsageScopeMountHelpers = "mount_helpers"
)

type usageRecord struct {
	opID    uint64
	scopeID uint32
	atNS    int64
	usage   Usage
}

// RecordUsage attaches a resource reading for scope to the op opID. It is a
// no-op when profiling is disabled for ctx or opID is 0. Readings count
// against the recorder's event cap like other events.
func RecordUsage(ctx context.Context, opID uint64, scope string, u Usage) {
	if opID == 0 {
		return
	}
	r := recorderFor(ctx)
	if r == nil {
		return
	}
	if r.total.Add(1) > r.maxTotal {
		r.total.Add(-1)
		r.dropped.Add(1)
		return
	}
	sh := r.shardFor(opID)
	rec := usageRecord{opID: opID, scopeID: r.Intern(scope), atNS: r.Now(), usage: u}
	sh.mu.Lock()
	sh.usage = append(sh.usage, rec)
	sh.mu.Unlock()
}
