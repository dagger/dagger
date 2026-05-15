package core

// These benchmarks measure module performance for large APIs, dependency
// graphs, and generated code. They are benchmarks, not behavior ownership.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func BenchmarkModule(b *testing.B) {
	testctx.New(b, BenchMiddleware()...).RunBenchmarks(ModuleSuite{})
}

func (ModuleSuite) BenchmarkLotsOfFunctions(ctx context.Context, b *testctx.B) {
	const funcCount = 100

	for _, tc := range []struct {
		name    string
		fixture string
	}{
		{name: "go sdk", fixture: "go/benchmark-lots-functions"},
		{name: "python sdk", fixture: "python/benchmark-lots-functions"},
		{name: "typescript sdk", fixture: "typescript/benchmark-lots-functions"},
	} {
		b.Run(tc.name, func(ctx context.Context, b *testctx.B) {
			for b.Unwrap().Loop() {
				c := connect(ctx, b)
				modGen := moduleFixture(b, c, tc.fixture)

				var eg errgroup.Group
				for i := range funcCount {
					// just verify a subset work
					if i%10 != 0 {
						continue
					}
					eg.Go(func() error {
						_, err := modGen.
							With(daggerCall(fmt.Sprintf("potato-%d", i))).
							Sync(ctx)
						return err
					})
				}
				require.NoError(b, eg.Wait())
			}
		})
	}
}

func (ModuleSuite) BenchmarkLotsOfDeps(ctx context.Context, b *testctx.B) {
	for b.Unwrap().Loop() {
		c := connect(ctx, b)

		// The fixture has a base module, 6 widening dependency layers, and a
		// top module depending on the final layer.
		_, err := moduleFixture(b, c, "go/benchmark-lots-of-deps").
			WithWorkdir("/work/mod28").
			With(daggerCall("fn")).
			Sync(ctx)
		require.NoError(b, err)
	}
}

// make sure we don't hit any limits when an object field value is large
func (ModuleSuite) BenchmarkLargeObjectFieldVal(ctx context.Context, b *testctx.B) {
	for b.Unwrap().Loop() {
		c := connect(ctx, b)

		// put a timeout on this since failures modes could result in hangs
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		b.Cleanup(cancel)

		_, err := moduleFixture(b, c, "go/benchmark-large-object-field-val").
			With(daggerCall("fn")).
			Sync(ctx)
		require.NoError(b, err)
	}
}

// regression test for https://github.com/dagger/dagger/issues/7334
// and https://github.com/dagger/dagger/pull/7336
func (ModuleSuite) BenchmarkCallSameModuleInParallel(ctx context.Context, b *testctx.B) {
	for b.Unwrap().Loop() {
		c := connect(ctx, b)

		ctr := moduleFixture(b, c, "go/benchmark-parallel-dep").
			With(daggerCall("fn"))

		out, err := ctr.Stdout(ctx)
		require.NoError(b, err)
		results := strings.Split(strings.TrimSpace(out), "\n")
		require.Len(b, results, 10)
		expectedRes := results[0]
		for _, res := range results {
			require.Equal(b, expectedRes, res)
		}
	}
}
