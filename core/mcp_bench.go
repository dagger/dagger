package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

// BENCHMARK INSTRUMENTATION — not for merging.
//
// benchStep wraps fn in a "bench.<name>" span so the changeset normalization
// benchmark can attribute wall time to each step. benchEval forces lazy
// results inside the current step, so their work is billed to it rather than
// to whichever later step happens to evaluate them first.
func benchStep(ctx context.Context, name string, fn func(context.Context) error) error {
	ctx, span := Tracer(ctx).Start(ctx, "bench."+name)
	defer span.End()
	return fn(ctx)
}

func benchEval(ctx context.Context, results ...dagql.AnyResult) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	return cache.Evaluate(ctx, results...)
}
