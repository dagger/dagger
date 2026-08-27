package core

import (
	"errors"
	"fmt"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestDescribeLoadFailure(t *testing.T) {
	t.Parallel()

	origin := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3},
		SpanID:  trace.SpanID{4, 5, 6},
	})

	t.Run("inlines the failing exec's stderr", func(t *testing.T) {
		t.Parallel()

		execErr := &ExecError{
			Err:      telemetry.TrackOrigin(errors.New("exit code: 1"), origin),
			ExitCode: 1,
			Stderr:   "# dagger/broken\n./main.go:8:9: undefined: nope\n",
		}
		err := fmt.Errorf("loading module %q: %w", "modules/broken",
			fmt.Errorf("failed to call module %q to get functions: %w", "broken",
				fmt.Errorf("call constructor: %w", execErr)))

		got := DescribeLoadFailure(err)
		require.Equal(t,
			"loading module \"modules/broken\": failed to call module \"broken\" to get functions: call constructor: exit code: 1\n"+
				"# dagger/broken\n./main.go:8:9: undefined: nope",
			got)
		require.NotContains(t, got, "traceparent")

		// The span cause keeps the message and re-stamps the origin so the
		// skipped-module row still links to the failing exec.
		cause := LoadFailureCause("", err)
		require.Equal(t, got, StripErrorOrigins(cause.Error()))
		origins := telemetry.ParseErrorOrigins(cause.Error())
		require.Len(t, origins, 1)
		require.Equal(t, origin.SpanID(), origins[0].SpanID())
	})

	t.Run("falls back to stdout when stderr is empty", func(t *testing.T) {
		t.Parallel()

		execErr := &ExecError{Err: errors.New("exit code: 2"), ExitCode: 2, Stdout: "only on stdout"}
		require.Equal(t, "boom: exit code: 2\nonly on stdout",
			DescribeLoadFailure(fmt.Errorf("boom: %w", execErr)))
	})

	t.Run("replaces the run-dagger-generate hint for missing generated files", func(t *testing.T) {
		t.Parallel()

		missing := &MissingGeneratedFileError{Module: "ungenerated", Path: "dagger.gen.go"}
		err := fmt.Errorf("loading module %q: failed to get module runtime: %w", "modules/ungenerated",
			telemetry.TrackOrigin(missing, origin))

		got := DescribeLoadFailure(err)
		require.Equal(t,
			"loading module \"modules/ungenerated\": failed to get module runtime: "+
				"module \"ungenerated\": generated file \"dagger.gen.go\" is missing (skipped until it is generated)",
			got)
		require.NotContains(t, got, "run `dagger generate`")
		// The strict-load error the module records is untouched: `dagger call`
		// should still tell the user to generate.
		require.Contains(t, err.Error(), "run `dagger generate`")
	})

	t.Run("leaves other errors alone", func(t *testing.T) {
		t.Parallel()

		err := telemetry.TrackOrigin(errors.New("no match found"), origin)
		require.Equal(t, "no match found", DescribeLoadFailure(err))
	})
}
