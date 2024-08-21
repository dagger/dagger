package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type BenchmarkSuite struct{}

func TestBenchmark(t *testing.T) {
	testctx.Run(testCtx, t, BenchmarkSuite{},
		testctx.WithOTelLogging(Logger()),
		testctx.WithOTelTracing(Tracer()),
	)
}

func (BenchmarkSuite) TestBenchmark(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	timers := map[string]time.Duration{}

	t.Run("dagger init", func(ctx context.Context, t *testctx.T) {
		now := time.Now()
		defer func() {
			timers[t.Name()] = time.Since(now)
		}()

		_, err := hostDaggerExec(ctx, t, modDir, "--debug", "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)
	})

	t.Run("dagger functions", func(ctx context.Context, t *testctx.T) {
		now := time.Now()
		defer func() {
			timers[t.Name()] = time.Since(now)
		}()

		_, err := hostDaggerExec(ctx, t, modDir, "--debug", "functions")
		require.NoError(t, err)
	})

	t.Run("dagger functions (cached)", func(ctx context.Context, t *testctx.T) {
		now := time.Now()
		defer func() {
			timers[t.Name()] = time.Since(now)
		}()

		_, err := hostDaggerExec(ctx, t, modDir, "--debug", "functions")
		require.NoError(t, err)
	})

	t.Run("dagger functions (modified)", func(ctx context.Context, t *testctx.T) {
		now := time.Now()
		defer func() {
			timers[t.Name()] = time.Since(now)
		}()

		err := os.WriteFile(filepath.Join(modDir, "mod.ts"), []byte(fmt.Sprintf(``)), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "--debug", "functions")
		require.NoError(t, err)
	})

	tw := tabwriter.NewWriter(os.Stderr, 0, 0, 1, ' ', 0)
	defer tw.Flush()

	fmt.Fprintln(tw, "NAME\tTIME")
	for name, timer := range timers {
		fmt.Fprintf(tw, "%s\t%s\n", name, timer)
	}
}
