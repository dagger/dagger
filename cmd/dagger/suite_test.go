package main

import (
	"os"
	"testing"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/otel-go/oteltestctx"
	"github.com/dagger/testctx"
)

func TestMain(m *testing.M) {
	os.Exit(oteltestctx.Main(m))
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		oteltestctx.WithTracing[*testing.T](
			oteltestctx.TraceConfig[*testing.T]{
				StartOptions: testutil.SpanOpts[*testing.T],
			},
		),
	}
}
