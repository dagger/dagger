package core

// These tests cover progress telemetry written to a client's log output. They
// verify that internal BuildKit/DagQL vertices are filtered from user-visible
// logs.
//
// See also:
// - cloud_test.go: cloud trace and reporting integration.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type TelemetrySuite struct{}

func TestTelemetry(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(TelemetrySuite{})
}

func (TelemetrySuite) TestNestedLogsDeliveredOnce(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to capture telemetry")
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)
	marker := fmt.Sprintf("nested-telemetry-%d", time.Now().UnixNano())

	// The inner CLI both subscribes to engine logs and forwards them through
	// its inherited OTLP endpoint. The outer client must receive the original
	// exec log, not a second copy reflected back by that exporter. Query sync
	// rather than stdout so the CLI's result is not another copy of the text.
	_, err := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithNewFile("/query.graphql", fmt.Sprintf(`{
			container { from(address: %q) {
				withExec(args: ["echo", %q]) { sync }
			} }
		}`, alpineImage, marker)).
		WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).Sync(ctx)
	require.NoError(t, err)
	require.NoError(t, c.Close()) // Drain both the nested CLI and outer subscription.

	_, batches := sink.capture()
	counts := map[string]int{}
	for _, batch := range batches {
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					if record.Body.GetStringValue() == marker+"\n" {
						key := fmt.Sprintf("%x/%x", record.TraceId, record.SpanId)
						counts[key]++
					}
				}
			}
		}
	}
	require.NotEmpty(t, counts, "missing inner exec output")
	for span, count := range counts {
		require.Equal(t, 1, count, "duplicate log delivery for span %s", span)
	}
}

func (TelemetrySuite) TestInternalVertexes(ctx context.Context, t *testctx.T) {
	cacheBuster := fmt.Sprintf("%d", time.Now().UTC().UnixNano())

	t.Run("merge pipeline", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))

		dirA := c.Directory().WithNewFile("/foo", "foo")
		dirB := c.Directory().WithNewFile("/bar", "bar")

		_, err := c.
			Container().
			From(alpineImage).
			WithDirectory("/foo", dirA).
			WithDirectory("/bar", dirB).
			WithExec([]string{"echo", cacheBuster}).
			Sync(ctx)

		require.NoError(t, err)

		require.NoError(t, c.Close()) // close + flush logs
		require.NotContains(t, logs.String(), "merge (")
	})
}
