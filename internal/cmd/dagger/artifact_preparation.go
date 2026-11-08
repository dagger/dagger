package daggercmd

import (
	"context"
	"sync"

	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

type artifactPreparationKey struct{}

// Preparation belongs to the command, not to any one artifact. Keep its parent
// so execution can leave the encapsulated subtree without losing context values.
type artifactPreparation struct {
	parent trace.Span
	span   trace.Span
	once   sync.Once
}

func withArtifactEngine(ctx context.Context, params client.Params, fn runClientCallback) error {
	// Start only after withEngine has connected and initialized the client.
	return withEngine(ctx, params, func(ctx context.Context, engineClient *client.Client) (rerr error) {
		ctx, preparation := startArtifactPreparation(ctx)
		defer func() { preparation.end(rerr) }()
		return fn(ctx, engineClient)
	})
}

func startArtifactPreparation(ctx context.Context) (context.Context, *artifactPreparation) {
	preparation := &artifactPreparation{parent: trace.SpanFromContext(ctx)}
	ctx, preparation.span = Tracer().Start(ctx, "prepare artifacts", telemetry.Encapsulate())
	return context.WithValue(ctx, artifactPreparationKey{}, preparation), preparation
}

func (p *artifactPreparation) end(err error) {
	p.once.Do(func() { telemetry.EndWithCause(p.span, &err) })
}

func artifactPreparationContext(ctx context.Context) context.Context {
	if p, ok := ctx.Value(artifactPreparationKey{}).(*artifactPreparation); ok {
		return trace.ContextWithSpan(ctx, p.span)
	}
	return ctx
}

func artifactExecutionContext(ctx context.Context) context.Context {
	if p, ok := ctx.Value(artifactPreparationKey{}).(*artifactPreparation); ok {
		return trace.ContextWithSpan(ctx, p.parent)
	}
	return ctx
}

func finishArtifactPreparation(ctx context.Context, err error) {
	if p, ok := ctx.Value(artifactPreparationKey{}).(*artifactPreparation); ok {
		p.end(err)
	}
}
