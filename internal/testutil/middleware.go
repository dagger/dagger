package testutil

import (
	"fmt"
	"os"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/testctx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const testctxTypeAttr = "dagger.io/testctx.type"
const testctxNameAttr = "dagger.io/testctx.name"
const testctxPrewarmAttr = "dagger.io/testctx.prewarm"

func isPrewarm() bool {
	_, ok := os.LookupEnv("TESTCTX_PREWARM")
	return ok
}

func SpanOpts[T testctx.Runner[T]](w *testctx.W[T]) []trace.SpanStartOption {
	var t T
	attrs := []attribute.KeyValue{
		attribute.String(testctxNameAttr, w.Name()),
		attribute.String(testctxTypeAttr, fmt.Sprintf("%T", t)),
		// Prevent revealed/rolled-up stuff bubbling up through test spans.
		attribute.Bool(telemetry.UIBoundaryAttr, true),
	}
	if isPrewarm() {
		attrs = append(attrs, attribute.Bool(testctxPrewarmAttr, true))
	}
	return []trace.SpanStartOption{
		trace.WithAttributes(attrs...),
	}
}
