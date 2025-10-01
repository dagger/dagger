package parallel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

func New() parallelJobs {
	return parallelJobs{
		Jobs: map[string]JobFunc{},
	}
}

func Run(ctx context.Context, name string, fn JobFunc) error {
	return New().WithJob(name, fn).Run(ctx)
}

type parallelJobs struct {
	Jobs map[string]JobFunc
}

type JobFunc func(context.Context) error

func (p parallelJobs) WithJob(name string, fn JobFunc) parallelJobs {
	clone := New()
	for k, v := range p.Jobs {
		clone.Jobs[k] = v
	}
	clone.Jobs[name] = fn
	return clone
}

func (p parallelJobs) Run(ctx context.Context) error {
	var eg errgroup.Group
	for name, job := range p.Jobs {
		name, job := name, job
		eg.Go(func() error {
			// FIXME: does the lib name affect correct span hierarchy?
			ctx, span := otel.Tracer("dagger.io/sdk.go").Start(ctx, name)
			err := job(ctx)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
			return err
		})
	}
	return eg.Wait()
}

func (p parallelJobs) RunSerial(ctx context.Context) error {
	for name, job := range p.Jobs {
		if err := Run(ctx, name, job); err != nil {
			return err
		}
	}
	return nil
}
