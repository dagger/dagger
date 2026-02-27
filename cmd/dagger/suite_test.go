package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		oteltest.WithTracing[*testing.T](
			oteltest.TraceConfig[*testing.T]{
				StartOptions: spanOpts[*testing.T],
			},
		),
		oteltest.WithLogging[*testing.T](),
	}
}

func spanOpts[T testctx.Runner[T]](w *testctx.W[T]) []trace.SpanStartOption {
	var t T
	attrs := []attribute.KeyValue{
		attribute.String("dagger.io/testctx.name", w.Name()),
		attribute.String("dagger.io/testctx.type", fmt.Sprintf("%T", t)),
		attribute.Bool(telemetry.UIBoundaryAttr, true),
	}
	if strings.Count(w.Name(), "/") == 0 {
		attrs = append(attrs, attribute.Bool(telemetry.UIRevealAttr, true))
	}
	if _, ok := os.LookupEnv("TESTCTX_PREWARM"); ok {
		attrs = append(attrs, attribute.Bool("dagger.io/testctx.prewarm", true))
	}
	return []trace.SpanStartOption{
		trace.WithAttributes(attrs...),
	}
}
