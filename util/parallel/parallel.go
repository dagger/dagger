package parallel

import (
	"context"
	"slices"

	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func New() Jobs {
	return Jobs{
		Tracing:  true, // Enable tracing by default
		Internal: false,
		Reveal:   false, // this used to be 'true', but it caused too many "hiding noisy spans" in web trace view

		// Don't use the contextual tracer by default: this breaks in Dagger *clients* (including modules),
		// because a freshly connected client does not have
		ContextualTracer: false,
	}
}

func Run(ctx context.Context, name string, fn JobFunc) error {
	return New().WithJob(name, fn).Run(ctx)
}

// Jobs is a set of jobs to run together, built up with the With methods and
// executed by Run.
type Jobs struct {
	Jobs     []Job
	Limit    *int
	Internal bool
	Reveal   bool
	// Use the "contextual tracer".
	// If enabled, spans are created using the same tracer that created the current span.
	// If disabled, spans are created using the global tracer for this process
	// Typically, you need to enable this in multi-tenant systems with many client contexts,
	// each with their own tracer (eg. inside the dagger engine)
	//
	ContextualTracer bool

	// Roll up child logs into this span.
	RollupLogs bool
	// Roll up child spans into this span for aggregated progress display.
	RollupSpans bool
	Tracing     bool // If set to false, tracing is completely disabled (no otel spans are created)

	// If true, cancel remaining jobs when the first job returns an error.
	FailFast bool
}

type Job struct {
	Name             string
	Func             JobFunc
	attributes       []attribute.KeyValue
	Internal         bool
	Reveal           bool
	ContextualTracer bool
	RollupLogs       bool
	RollupSpans      bool
	// If set to false, tracing is completely disabled (no otel spans are created)
	Tracing bool
}

type JobFunc func(context.Context) error

func (p Jobs) WithTracing(tracing bool) Jobs {
	p = p.Clone()
	p.Tracing = tracing
	return p
}

func (p Jobs) WithInternal(internal bool) Jobs {
	p = p.Clone()
	p.Internal = internal
	return p
}

func (p Jobs) WithReveal(reveal bool) Jobs {
	p = p.Clone()
	p.Reveal = reveal
	return p
}

func (p Jobs) WithContextualTracer(contextualTracer bool) Jobs {
	p = p.Clone()
	p.ContextualTracer = contextualTracer
	return p
}

func (p Jobs) WithRollupSpans(rollupSpans bool) Jobs {
	p = p.Clone()
	p.RollupSpans = rollupSpans
	return p
}

func (p Jobs) WithRollupLogs(rollupLogs bool) Jobs {
	p = p.Clone()
	p.RollupLogs = rollupLogs
	return p
}

func (p Jobs) WithFailFast(failFast bool) Jobs {
	p = p.Clone()
	p.FailFast = failFast
	return p
}

func (p Jobs) WithJob(name string, fn JobFunc, attributes ...attribute.KeyValue) Jobs {
	p = p.Clone()
	p.Jobs = append(p.Jobs, Job{name, fn, attributes, p.Internal, p.Reveal, p.ContextualTracer, p.RollupLogs, p.RollupSpans, p.Tracing})
	return p
}

func (p Jobs) Clone() Jobs {
	cp := p
	cp.Jobs = slices.Clone(cp.Jobs)
	return cp
}

var tracerName = "dagger.io/util/parallel"

func (job Job) tracer(ctx context.Context) trace.Tracer {
	if job.ContextualTracer {
		return trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName)
	}
	return otel.Tracer(tracerName)
}

func (job Job) startSpan(ctx context.Context) (context.Context, trace.Span) {
	attr := job.attributes
	if job.Reveal {
		attr = append(attr, attribute.Bool("dagger.io/ui.reveal", true))
	}
	if job.Internal {
		attr = append(attr, attribute.Bool("dagger.io/ui.internal", true))
	}
	if job.RollupLogs {
		attr = append(attr, attribute.Bool("dagger.io/ui.rollup.logs", true))
	}
	if job.RollupSpans {
		attr = append(attr, attribute.Bool("dagger.io/ui.rollup.spans", true))
	}
	return job.tracer(ctx).Start(ctx, job.Name, trace.WithAttributes(attr...))
}

func (job Job) Runner(ctx context.Context) func() error {
	// FIXME: this starts the span once the job actually runs.
	//  - Pro: span duration is accurate
	//  - Con: parallel jobs are run in random order
	// If we start the span before the job runs, the pros and cons are switched.
	// The clean solution is to reimplement errgroup.Group to get our cake and eat it too.
	return func() (rerr error) {
		var span trace.Span
		if job.Tracing {
			ctx, span = job.startSpan(ctx)
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()
		}
		if job.Func == nil {
			return nil
		}
		return job.Func(ctx)
	}
}

func (job Job) Run(ctx context.Context) error {
	return job.Runner(ctx)()
}

func (p Jobs) WithLimit(limit int) Jobs {
	p = p.Clone()
	p.Limit = &limit
	return p
}

func (p Jobs) Run(ctx context.Context) error {
	if p.FailFast {
		return p.runFailFast(ctx)
	}
	eg := pool.New().WithErrors()
	if p.Limit != nil {
		eg = eg.WithMaxGoroutines(*p.Limit)
	}
	for _, job := range p.Jobs {
		eg.Go(job.Runner(ctx))
	}
	return eg.Wait()
}

func (p Jobs) runFailFast(ctx context.Context) error {
	eg := pool.New().WithContext(ctx).WithCancelOnError().WithFirstError()
	if p.Limit != nil {
		eg = eg.WithMaxGoroutines(*p.Limit)
	}
	for _, job := range p.Jobs {
		eg.Go(func(ctx context.Context) error {
			return job.Runner(ctx)()
		})
	}
	return eg.Wait()
}

func (p Jobs) RunSerial(ctx context.Context) error {
	for _, job := range p.Jobs {
		if err := job.Run(ctx); err != nil {
			return err
		}
	}
	return nil
}
