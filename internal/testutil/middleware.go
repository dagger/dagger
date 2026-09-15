package testutil

import (
	"os"

	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const benchPrewarmAttr = "dagger.io/bench.prewarm"

func isPrewarm() bool {
	_, ok := os.LookupEnv("DAGGER_BENCH_PREWARM")
	return ok
}

func SpanOpts[T testctx.Runner[T]](w *testctx.W[T]) []trace.SpanStartOption {
	attrs := []attribute.KeyValue{
		// Prevent revealed/rolled-up stuff bubbling up through test spans.
		attribute.Bool(telemetry.UIBoundaryAttr, true),
	}
	if isPrewarm() {
		attrs = append(attrs, attribute.Bool(benchPrewarmAttr, true))
	}
	return []trace.SpanStartOption{
		trace.WithAttributes(attrs...),
	}
}
