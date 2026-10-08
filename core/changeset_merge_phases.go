package core

import (
	"context"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/wcprof"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// mergePhases times the phases of a changeset merge, so a slow merge says
// where its own time went: each phase is a wcprof io op "<prefix>.<phase>"
// and adds its milliseconds to the span attribute
// "dagger.<prefix>.<phase>_ms". A nil *mergePhases records nothing.
type mergePhases struct {
	span   trace.Span
	prefix string

	mu sync.Mutex
	ms map[string]int64
}

// phaseMark is a phase boundary: wall time for the span attribute, and the
// wcprof clock (zero when profiling is off).
type phaseMark struct {
	wall time.Time
	ns   int64
}

func newMergePhases(span trace.Span, prefix string) *mergePhases {
	return &mergePhases{span: span, prefix: prefix, ms: map[string]int64{}}
}

func phaseNow() phaseMark {
	return phaseMark{wall: time.Now(), ns: wcprof.NowNS()}
}

// run times fn as phase. Work fn starts with its context (dagql calls,
// lazy evaluations, waits) is profiled beneath the phase's op.
func (p *mergePhases) run(ctx context.Context, phase string, fn func(context.Context) error) error {
	if p == nil {
		return fn(ctx)
	}
	start := time.Now()
	opCtx, op := wcprof.BeginOp(ctx, wcprof.OpKindIO, p.prefix+"."+phase, wcprof.OpOpts{})
	err := fn(opCtx)
	op.EndErr(err)
	p.add(phase, time.Since(start))
	return err
}

// record records phase as having run from start until now, and returns now:
// the next phase's start.
func (p *mergePhases) record(ctx context.Context, phase string, start phaseMark) phaseMark {
	end := phaseNow()
	if p == nil {
		return end
	}
	if start.ns != 0 && end.ns != 0 {
		wcprof.RecordOp(ctx, wcprof.OpKindIO, p.prefix+"."+phase, wcprof.OpOpts{}, start.ns, end.ns, wcprof.OutcomeOK)
	}
	p.add(phase, end.wall.Sub(start.wall))
	return end
}

func (p *mergePhases) add(phase string, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ms[phase] += d.Milliseconds()
	p.span.SetAttributes(attribute.Int64("dagger."+p.prefix+"."+phase+"_ms", p.ms[phase]))
}
