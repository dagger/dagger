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
	// without I/O, and those that needed it. Only process scopes have them.
	MinorFaults int64
	MajorFaults int64
	// GoAllocBytes, GoGCCycles and GoGCCPUUS are a Go process's cumulative
	// heap allocations, its completed GC cycles, and the CPU time its runtime
	// estimates the GC used. Only the engine process scope has them.
	GoAllocBytes int64
	GoGCCycles   int64
	GoGCCPUUS    int64
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
