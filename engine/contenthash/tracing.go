package contenthash

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

func Tracer(ctx context.Context) trace.Tracer {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer("dagger.io/contenthash")
}
