package parallel

import (
	"context"
	"slices"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

func New() parallelJobs {
	return parallelJobs{
		Reveal:   true,
		Internal: false,
	}
}

func Run(ctx context.Context, name string, fn JobFunc) error {
	return New().WithJob(name, fn).Run(ctx)
}

type parallelJobs struct {
	Jobs     []Job
	Limit    *int
	Internal bool
	Reveal   bool
}

type Job struct {
	Name       string
	Func       JobFunc
	attributes []attribute.KeyValue
	Internal   bool
	Reveal     bool
}

type JobFunc func(context.Context) error

func (p parallelJobs) WithInternal(internal bool) parallelJobs {
	p = p.Clone()
	p.Internal = internal
	return p
}

func (p parallelJobs) WithReveal(reveal bool) parallelJobs {
	p = p.Clone()
	p.Reveal = reveal
	return p
}

func (p parallelJobs) WithJob(name string, fn JobFunc, attributes ...attribute.KeyValue) parallelJobs {
	p = p.Clone()
	p.Jobs = append(p.Jobs, Job{name, fn, attributes, p.Internal, p.Reveal})
	return p
}

func (p parallelJobs) Clone() parallelJobs {
	cp := p
	cp.Jobs = slices.Clone(cp.Jobs)
	return cp
}

func (job Job) startSpan(ctx context.Context) (context.Context, trace.Span) {
	var opts []trace.SpanStartOption
	attr := job.attributes
	if job.Reveal {
		attr = append(attr, attribute.Bool("dagger.io/ui.reveal", true))
	}
	opts = append(opts, trace.WithAttributes(attr...))
	if job.Internal {
		opts = append(opts, telemetry.Internal())
	}
	return trace.SpanFromContext(ctx).TracerProvider().
		Tracer("dagger.io/util/parallel").
		Start(ctx, job.Name, opts...)
}

func (job Job) Runner(ctx context.Context) func() error {
	// FIXME: this starts the span once the job actually runs.
	//  - Pro: span duration is accurate
	//  - Con: parallel jobs are run in random order
	// If we start the span before the job runs, the pros and cons are switched.
	// The clean solution is to reimplement errgroup.Group to get our cake and eat it too.
	return func() error {
		ctx, span := job.startSpan(ctx)
		defer span.End()
		if job.Func == nil {
			return nil
		}
		err := job.Func(ctx)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		return err
	}
}

func (job Job) Run(ctx context.Context) error {
	return job.Runner(ctx)()
}

func (p parallelJobs) WithLimit(limit int) parallelJobs {
	p = p.Clone()
	p.Limit = &limit
	return p
}

func (p parallelJobs) Run(ctx context.Context) error {
	var eg errgroup.Group
	if p.Limit != nil {
		eg.SetLimit(*p.Limit)
	}
	for _, job := range p.Jobs {
		eg.Go(job.Runner(ctx))
	}
	return eg.Wait()
}

func (p parallelJobs) RunSerial(ctx context.Context) error {
	for _, job := range p.Jobs {
		if err := job.Run(ctx); err != nil {
			return err
		}
	}
	return nil
}
