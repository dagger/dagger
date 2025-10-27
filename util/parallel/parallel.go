package parallel

import (
	"context"
	"slices"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

func New() parallelJobs {
	return parallelJobs{}
}

func Run(ctx context.Context, name string, fn JobFunc) error {
	return New().WithJob(name, fn).Run(ctx)
}

type parallelJobs struct {
	Jobs  []Job
	Limit *int
}

type Job struct {
	Name string
	Func JobFunc
}

type JobFunc func(context.Context) error

func (p parallelJobs) WithJob(name string, fn JobFunc) parallelJobs {
	p = p.Clone()
	p.Jobs = append(p.Jobs, Job{name, fn})
	return p
}

func (p parallelJobs) Clone() parallelJobs {
	cp := p
	cp.Jobs = slices.Clone(cp.Jobs)
	return cp
}

func startSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	attr := []attribute.KeyValue{
		attribute.Bool("dagger.io/ui.reveal", true),
	}
	return trace.SpanFromContext(ctx).TracerProvider().
		Tracer("dagger.io/util/parallel").
		Start(ctx, name, trace.WithAttributes(attr...))
}

func (job Job) Runner(ctx context.Context) func() error {
	// FIXME: this starts the span before the job actually runs.
	// In the case where jobs are long and parallelism is limited, the difference
	// can be significant (ie. misleading)
	// On the other hand, if we start the span inside the goroutine, display order will be random
	// The clean solution is to reimplement errgroup.Group to get our cake and eat it too.
	ctx, span := startSpan(ctx, job.Name)
	return func() error {
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
