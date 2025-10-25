package main

import (
	"os"
	"testing"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		oteltest.WithTracing[*testing.T](
			oteltest.TraceConfig[*testing.T]{
				StartOptions: testutil.SpanOpts[*testing.T],
			},
		),
		oteltest.WithLogging[*testing.T](),
	}
}
