package server

import (
	"bytes"
	"context"
	"errors"
	"runtime/pprof"

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

// Usage scopes of a phase: the CPU the engine used while it ran. They are
// activity during the phase, not CPU the phase owned: phases that overlap
// (two sessions) see each other's work.
const (
	// UsageScopeEngineProcess is the engine process itself.
	UsageScopeEngineProcess = "engine_process"
	// UsageScopeEngineChildren is the engine's child processes (runc, and
	// the processes it runs), counted only once the engine reaped them.
	UsageScopeEngineChildren = "engine_children"
	// UsageScopeEngineCgroup is the engine process's cgroup (as the engine's
	// cgroup metrics find it) and everything in it.
	UsageScopeEngineCgroup = "engine_cgroup"
)

type engineCPU struct {
	process, children wcprof.Usage
	cgroup            wcprof.Usage
	cgroupOK          bool
}

func readEngineCPU() engineCPU {
	var c engineCPU
	us := func(tv unix.Timeval) int64 { return tv.Sec*1_000_000 + tv.Usec }
	var ru unix.Rusage
	if unix.Getrusage(unix.RUSAGE_SELF, &ru) == nil {
		c.process = wcprof.Usage{CPUUserUS: us(ru.Utime), CPUSystemUS: us(ru.Stime)}
	}
	if unix.Getrusage(unix.RUSAGE_CHILDREN, &ru) == nil {
		c.children = wcprof.Usage{CPUUserUS: us(ru.Utime), CPUSystemUS: us(ru.Stime)}
	}
	if dir, err := cgroupmetrics.EngineCgroupDir(); err == nil {
		if user, sys, _, err := resources.ReadUsage(dir); err == nil {
			c.cgroup, c.cgroupOK = wcprof.Usage{CPUUserUS: user, CPUSystemUS: sys}, true
		}
	}
	return c
}

func usageSince(start, end wcprof.Usage) wcprof.Usage {
	return wcprof.Usage{CPUUserUS: end.CPUUserUS - start.CPUUserUS, CPUSystemUS: end.CPUSystemUS - start.CPUSystemUS}
}

// phaseRun is one run of a phase.
type phaseRun struct {
	ctx   context.Context
	op    *wcprof.Op
	start engineCPU
	prof  *armedProfile
}

// beginSessionPhase starts a session's run of PhaseSession: when ctx is
// profiled, an op over it with the engine's CPU counters read at its start;
// and the CPU profile armed for the phase, if any. Nil when there is neither.
func (srv *Server) beginSessionPhase(ctx context.Context, sessionID string) *phaseRun {
	var p *phaseRun
	if wcprof.Enabled(ctx) {
		p = &phaseRun{ctx: ctx, start: readEngineCPU()}
		p.ctx, p.op = wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, PhaseSession, wcprof.OpOpts{Ident: sessionID})
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
	end := readEngineCPU()
	id := p.op.ID()
	wcprof.RecordUsage(p.ctx, id, UsageScopeEngineProcess, usageSince(p.start.process, end.process))
	wcprof.RecordUsage(p.ctx, id, UsageScopeEngineChildren, usageSince(p.start.children, end.children))
	if p.start.cgroupOK && end.cgroupOK {
		wcprof.RecordUsage(p.ctx, id, UsageScopeEngineCgroup, usageSince(p.start.cgroup, end.cgroup))
	}
	p.op.End(wcprof.OutcomeOK)
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
