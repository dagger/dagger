package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime/metrics"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/engineutil/resources"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetry/cgroupmetrics"
	"github.com/dagger/dagger/engine/wcprof"
)

// Engine phases for wcprof: a named stretch of the engine's life, such as a
// session's, that wcprof records as an op carrying the engine's CPU activity
// during it, and that an armed CPU profile starts and stops on.

// PhaseSession is a session's life, from its initialization to the end of its
// removal.
const PhaseSession = "session"

// Usage scopes of a phase: the resources the engine used while it ran. They
// are activity during the phase, not resources the phase owned: phases that
// overlap (two sessions) see each other's work.
const (
	// UsageScopeEngineProcess is the engine process itself: its CPU, page
	// faults, and Go runtime counters.
	UsageScopeEngineProcess = "engine_process"
	// UsageScopeEngineChildren is the engine's child processes (runc, and
	// the processes it runs), counted only once the engine reaped them: their
	// CPU and page faults.
	UsageScopeEngineChildren = "engine_children"
	// UsageScopeEngineCgroup is the engine process's cgroup (as the engine's
	// cgroup metrics find it) and everything in it: its CPU, and the bytes it
	// read from and wrote to block devices (left out when io.stat can't be
	// read).
	UsageScopeEngineCgroup = "engine_cgroup"
	// UsageScopeMachine is the whole machine the engine runs on, from
	// /proc/stat, which a container shares with its host: user (with nice),
	// system (with interrupts), iowait, steal and idle, summed over its cores.
	UsageScopeMachine = "machine"
	// UsageScopeMachineSample is the machine, as UsageScopeMachine, over one
	// stretch of the phase: read every machineSampleInterval while it runs,
	// each reading covers the time since the phase's previous one (or its
	// start), and its time is the stretch's end. The last stretch ends with
	// the phase.
	UsageScopeMachineSample = "machine_sample"
	// UsageScopeEngineCgroupSample is the engine's cgroup's memory at one
	// moment of the phase, read every machineSampleInterval while it runs:
	// anonymous memory, file pages, and the part of both in transparent huge
	// pages.
	UsageScopeEngineCgroupSample = "engine_cgroup_sample"
	// UsageScopeGoRuntimeSample is the engine process's Go garbage collector
	// over one stretch of the phase, read every machineSampleInterval while
	// it runs: the CPU its GC used, by class, since the previous reading (the
	// runtime's estimates, which it updates at GC events, so a stretch can
	// carry an earlier one's CPU); and at the reading's time, the live heap,
	// the heap goal, and the scannable heap, stacks and globals.
	UsageScopeGoRuntimeSample = "go_runtime_sample"
)

// machineSampleInterval is how often a profiled phase reads the machine's
// CPU, the engine's memory and its Go GC: fine enough to place a slow
// stretch of about a second, for two small file reads and a runtime read.
const machineSampleInterval = 100 * time.Millisecond

// engineCgroupDir finds the engine's cgroup; tests replace it.
var engineCgroupDir = cgroupmetrics.EngineCgroupDir

// engineReading is the engine's counters at one moment.
type engineReading struct {
	process, children wcprof.Usage
	// cgroup is the engine's cgroup's CPU, and its I/O when ioOK.
	cgroup    wcprof.Usage
	cgroupOK  bool
	ioOK      bool
	cgroupDir string
	machine   wcprof.Usage
	machineOK bool
	// goGC is the Go runtime's GC readings, as readGoGC reads them.
	goGC wcprof.Usage
}

func readEngine() engineReading {
	var c engineReading
	us := func(tv unix.Timeval) int64 { return tv.Sec*1_000_000 + tv.Usec }
	var ru unix.Rusage
	if unix.Getrusage(unix.RUSAGE_SELF, &ru) == nil {
		c.process = wcprof.Usage{CPUUserUS: us(ru.Utime), CPUSystemUS: us(ru.Stime), MinorFaults: ru.Minflt, MajorFaults: ru.Majflt}
	}
	readGoRuntime(&c.process)
	c.goGC = readGoGC()
	if unix.Getrusage(unix.RUSAGE_CHILDREN, &ru) == nil {
		c.children = wcprof.Usage{CPUUserUS: us(ru.Utime), CPUSystemUS: us(ru.Stime), MinorFaults: ru.Minflt, MajorFaults: ru.Majflt}
	}
	if dir, err := engineCgroupDir(); err == nil {
		c.cgroupDir = dir
		if user, sys, _, err := resources.ReadUsage(dir); err == nil {
			c.cgroup, c.cgroupOK = wcprof.Usage{CPUUserUS: user, CPUSystemUS: sys}, true
		}
		if read, written, err := resources.ReadIO(dir); err == nil {
			c.cgroup.IOReadBytes, c.cgroup.IOWriteBytes, c.ioOK = read, written, true
		}
	}
	if stat, err := os.ReadFile("/proc/stat"); err == nil {
		c.machine, c.machineOK = parseMachineCPU(stat)
	}
	return c
}

// goRuntimeMetrics are the Go runtime's counters readGoRuntime reads, in
// the order it reads them.
var goRuntimeMetrics = []string{
	"/gc/heap/allocs:bytes",
	"/gc/cycles/total:gc-cycles",
	"/cpu/classes/gc/total:cpu-seconds",
}

// readGoRuntime adds this process's Go runtime counters to u. A counter the
// runtime doesn't support stays 0.
func readGoRuntime(u *wcprof.Usage) {
	samples := make([]metrics.Sample, len(goRuntimeMetrics))
	for i, name := range goRuntimeMetrics {
		samples[i].Name = name
	}
	metrics.Read(samples)
	if v := samples[0].Value; v.Kind() == metrics.KindUint64 {
		u.GoAllocBytes = int64(v.Uint64())
	}
	if v := samples[1].Value; v.Kind() == metrics.KindUint64 {
		u.GoGCCycles = int64(v.Uint64())
	}
	if v := samples[2].Value; v.Kind() == metrics.KindFloat64 {
		u.GoGCCPUUS = int64(v.Float64() * 1e6)
	}
}

// goGCMetrics are the Go runtime's GC readings readGoGC reads, in the order
// it reads them: CPU by class, in seconds, then sizes in bytes.
var goGCMetrics = []string{
	"/cpu/classes/gc/mark/assist:cpu-seconds",
	"/cpu/classes/gc/mark/dedicated:cpu-seconds",
	"/cpu/classes/gc/mark/idle:cpu-seconds",
	"/cpu/classes/gc/pause:cpu-seconds",
	"/gc/heap/live:bytes",
	"/gc/heap/goal:bytes",
	"/gc/scan/heap:bytes",
	"/gc/scan/stack:bytes",
	"/gc/scan/globals:bytes",
}

// readGoGC reads this process's GC: its cumulative CPU by class, and its
// heap and scan sizes now. A reading the runtime doesn't support stays 0.
func readGoGC() wcprof.Usage {
	samples := make([]metrics.Sample, len(goGCMetrics))
	for i, name := range goGCMetrics {
		samples[i].Name = name
	}
	metrics.Read(samples)
	us := func(i int) int64 {
		if v := samples[i].Value; v.Kind() == metrics.KindFloat64 {
			return int64(v.Float64() * 1e6)
		}
		return 0
	}
	bytes := func(i int) int64 {
		if v := samples[i].Value; v.Kind() == metrics.KindUint64 {
			return int64(v.Uint64())
		}
		return 0
	}
	return wcprof.Usage{
		GCAssistUS:       us(0),
		GCDedicatedUS:    us(1),
		GCIdleUS:         us(2),
		GCPauseUS:        us(3),
		HeapLiveBytes:    bytes(4),
		HeapGoalBytes:    bytes(5),
		ScanHeapBytes:    bytes(6),
		ScanStackBytes:   bytes(7),
		ScanGlobalsBytes: bytes(8),
	}
}

// procStatTickUS is a /proc/stat tick in microseconds: the kernel reports
// these times in USER_HZ, which is 100 on Linux.
const procStatTickUS = 10_000

// parseMachineCPU reads the whole machine's CPU times from /proc/stat's
// first line ("cpu user nice system idle iowait irq softirq steal ...").
func parseMachineCPU(stat []byte) (wcprof.Usage, bool) {
	line, _, _ := bytes.Cut(stat, []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) < 9 || fields[0] != "cpu" {
		return wcprof.Usage{}, false
	}
	var ticks [8]int64
	for i := range ticks {
		n, err := strconv.ParseInt(fields[i+1], 10, 64)
		if err != nil {
			return wcprof.Usage{}, false
		}
		ticks[i] = n * procStatTickUS
	}
	user, nice, system, idle, iowait, irq, softirq, steal := ticks[0], ticks[1], ticks[2], ticks[3], ticks[4], ticks[5], ticks[6], ticks[7]
	return wcprof.Usage{
		CPUUserUS:   user + nice,
		CPUSystemUS: system + irq + softirq,
		CPUIowaitUS: iowait,
		CPUStealUS:  steal,
		CPUIdleUS:   idle,
	}, true
}

// samplePhase records, on the op opID every machineSampleInterval, the
// machine's CPU since the previous reading (from start's, when it has one),
// the memory of the engine's cgroup (when start found it), and the engine's
// Go GC, until the returned func is called; that records a last sample and
// returns once it's recorded.
func samplePhase(ctx context.Context, opID uint64, start engineReading) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		prev, prevGC := start.machine, start.goGC
		sample := func() {
			gc := readGoGC()
			stretch := gc
			stretch.GCAssistUS -= prevGC.GCAssistUS
			stretch.GCDedicatedUS -= prevGC.GCDedicatedUS
			stretch.GCIdleUS -= prevGC.GCIdleUS
			stretch.GCPauseUS -= prevGC.GCPauseUS
			wcprof.RecordUsage(ctx, opID, UsageScopeGoRuntimeSample, stretch)
			prevGC = gc
			if start.machineOK {
				if stat, err := os.ReadFile("/proc/stat"); err == nil {
					if cur, ok := parseMachineCPU(stat); ok {
						wcprof.RecordUsage(ctx, opID, UsageScopeMachineSample, usageSince(prev, cur))
						prev = cur
					}
				}
			}
			if start.cgroupDir != "" {
				if m, err := resources.ReadMemory(start.cgroupDir); err == nil {
					wcprof.RecordUsage(ctx, opID, UsageScopeEngineCgroupSample, wcprof.Usage{
						MemAnonBytes: m.AnonBytes,
						MemFileBytes: m.FileBytes,
						MemHugeBytes: m.HugeBytes,
					})
				}
			}
		}
		ticker := time.NewTicker(machineSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sample()
			case <-stop:
				sample()
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func usageSince(start, end wcprof.Usage) wcprof.Usage {
	return wcprof.Usage{
		CPUUserUS:    end.CPUUserUS - start.CPUUserUS,
		CPUSystemUS:  end.CPUSystemUS - start.CPUSystemUS,
		CPUIowaitUS:  end.CPUIowaitUS - start.CPUIowaitUS,
		CPUStealUS:   end.CPUStealUS - start.CPUStealUS,
		CPUIdleUS:    end.CPUIdleUS - start.CPUIdleUS,
		MinorFaults:  end.MinorFaults - start.MinorFaults,
		MajorFaults:  end.MajorFaults - start.MajorFaults,
		GoAllocBytes: end.GoAllocBytes - start.GoAllocBytes,
		GoGCCycles:   end.GoGCCycles - start.GoGCCycles,
		GoGCCPUUS:    end.GoGCCPUUS - start.GoGCCPUUS,
		IOReadBytes:  end.IOReadBytes - start.IOReadBytes,
		IOWriteBytes: end.IOWriteBytes - start.IOWriteBytes,
	}
}

// phaseRun is one run of a phase.
type phaseRun struct {
	ctx   context.Context
	op    *wcprof.Op
	start engineReading
	prof  *armedProfile
	// stopSampling stops sampling the machine, the engine's memory and its
	// Go GC, and waits for its last sample.
	stopSampling func()
}

// beginSessionPhase starts a session's run of PhaseSession: when ctx is
// profiled, an op over it with the engine's CPU counters read at its start;
// and the CPU profile armed for the phase, if any. Nil when there is neither.
func (srv *Server) beginSessionPhase(ctx context.Context, sessionID string) *phaseRun {
	var p *phaseRun
	if wcprof.Enabled(ctx) {
		p = &phaseRun{ctx: ctx, start: readEngine()}
		p.ctx, p.op = wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, PhaseSession, wcprof.OpOpts{Ident: sessionID})
		p.stopSampling = samplePhase(p.ctx, p.op.ID(), p.start)
	}
	if a := srv.startArmedProfile(PhaseSession); a != nil {
		if p == nil {
			p = &phaseRun{}
		}
		p.prof = a
	}
	return p
}

// end ends the run: the op gets the CPU the engine used meanwhile, and an
// armed profile started on this run stops and is delivered.
func (p *phaseRun) end(srv *Server) {
	if p == nil {
		return
	}
	if p.prof != nil {
		pprof.StopCPUProfile()
		srv.finishArmedProfile(p.prof, nil)
	}
	if p.op == nil {
		return
	}
	if p.stopSampling != nil {
		p.stopSampling()
	}
	end := readEngine()
	id := p.op.ID()
	wcprof.RecordUsage(p.ctx, id, UsageScopeEngineProcess, usageSince(p.start.process, end.process))
	wcprof.RecordUsage(p.ctx, id, UsageScopeEngineChildren, usageSince(p.start.children, end.children))
	if p.start.cgroupOK && end.cgroupOK {
		cgroup := usageSince(p.start.cgroup, end.cgroup)
		if !p.start.ioOK || !end.ioOK {
			cgroup.IOReadBytes, cgroup.IOWriteBytes = 0, 0
		}
		wcprof.RecordUsage(p.ctx, id, UsageScopeEngineCgroup, cgroup)
	}
	if p.start.machineOK && end.machineOK {
		wcprof.RecordUsage(p.ctx, id, UsageScopeMachine, usageSince(p.start.machine, end.machine))
	}
	p.op.End(wcprof.OutcomeOK)
}

// phaseStep is one step of a phase run, recorded as its own op under the
// run's op (or under an enclosing step's) when the run is profiled. Its methods
// work on a nil step, so callers need no profiling checks.
type phaseStep struct {
	ctx context.Context
	op  *wcprof.Op
}

// beginTeardown begins the run's removal step, "session.teardown", directly
// under its op. Nil when the run isn't profiled.
func (p *phaseRun) beginTeardown() *phaseStep {
	if p == nil || p.op == nil {
		return nil
	}
	return beginPhaseStep(p.ctx, "session.teardown")
}

// beginStep begins a step within this one.
func (s *phaseStep) beginStep(name string) *phaseStep {
	if s == nil {
		return nil
	}
	return beginPhaseStep(s.ctx, name)
}

func beginPhaseStep(ctx context.Context, name string) *phaseStep {
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, name, wcprof.OpOpts{})
	return &phaseStep{ctx: ctx, op: op}
}

// end ends the step.
func (s *phaseStep) end() {
	if s == nil {
		return
	}
	s.op.End(wcprof.OutcomeOK)
}

// armedProfile is a CPU profile armed for the next run of a phase. The
// server's armedMu guards arming, starting and disarming, so a started
// profile stays armed, and keeps others from arming, until its run ends.
type armedProfile struct {
	phase   string
	started bool
	buf     bytes.Buffer
	done    chan struct{}
	err     error
}

// startArmedProfile starts the profile armed for phase, if any, and returns
// it; nil when none is armed or it couldn't start.
func (srv *Server) startArmedProfile(phase string) *armedProfile {
	srv.armedMu.Lock()
	defer srv.armedMu.Unlock()
	a := srv.armed
	if a == nil || a.phase != phase || a.started {
		return nil
	}
	if err := pprof.StartCPUProfile(&a.buf); err != nil {
		srv.armed = nil
		a.err = err
		close(a.done)
		return nil
	}
	a.started = true
	return a
}

func (srv *Server) finishArmedProfile(a *armedProfile, err error) {
	srv.armedMu.Lock()
	defer srv.armedMu.Unlock()
	if srv.armed == a {
		srv.armed = nil
	}
	a.err = err
	close(a.done)
}

// disarm removes a unless a run already started it, and reports whether one
// did. It leaves alone a profile armed in a's place after a failed start.
func (srv *Server) disarm(a *armedProfile) (started bool) {
	srv.armedMu.Lock()
	defer srv.armedMu.Unlock()
	if a.started {
		return true
	}
	if srv.armed == a {
		srv.armed = nil
	}
	return false
}

var errProfileArmed = errors.New("a CPU profile is already armed")

// ProfilePhase arms a CPU profile of the next run of the named phase and
// returns it in pprof's format once the run ends. It disarms if ctx ends
// before the run starts; once started, the profile runs to the run's end.
// One profile can be armed at a time.
func (srv *Server) ProfilePhase(ctx context.Context, phase string) ([]byte, error) {
	a := &armedProfile{phase: phase, done: make(chan struct{})}
	srv.armedMu.Lock()
	if srv.armed != nil {
		srv.armedMu.Unlock()
		return nil, errProfileArmed
	}
	srv.armed = a
	srv.armedMu.Unlock()
	slog.Info("wcprof: CPU profile armed", "phase", phase)
	select {
	case <-a.done:
	case <-ctx.Done():
		if !srv.disarm(a) {
			return nil, ctx.Err()
		}
		// It started before ctx ended: it is this request's, delivered
		// at the end of its run.
		<-a.done
	}
	if a.err != nil {
		return nil, a.err
	}
	return a.buf.Bytes(), nil
}
