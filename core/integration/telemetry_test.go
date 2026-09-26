package core

// These tests cover progress telemetry written to a client's log output. They
// verify that internal BuildKit/DagQL vertices are filtered from user-visible
// logs.
//
// See also:
// - cloud_test.go: cloud trace and reporting integration.

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

type TelemetrySuite struct{}

func TestTelemetry(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(TelemetrySuite{})
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

// TestSetSessionTitle: an SDK client's `dagger session` names itself with one
// API call. The engine publishes the rename as a span-name record on the
// session CLI's command span (the primary span it declared at connect); the
// CLI forwards that record like any engine telemetry, and applies it to its
// own live span, so the span it exports carries the title too.
func (TelemetrySuite) TestSetSessionTitle(ctx context.Context, t *testctx.T) {
	const title = "Deploy the docs"
	c, sink := connectWithTrace(ctx, t)
	require.NoError(t, c.SetSessionTitle(ctx, title))
	require.NoError(t, c.Close()) // close + flush the session CLI's telemetry

	traces, logs := sink.capture()
	var primary []byte
	for _, req := range logs {
		for _, rl := range req.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				for _, rec := range sl.GetLogRecords() {
					if rec.GetBody().GetStringValue() != title {
						continue
					}
					for _, kv := range rec.GetAttributes() {
						if kv.GetKey() == telemetryattrs.LogRoleAttr && kv.GetValue().GetStringValue() == telemetryattrs.LogRoleSpanName {
							primary = rec.GetSpanId()
						}
					}
				}
			}
		}
	}
	require.NotEmpty(t, primary, "the engine publishes the title as a span-name record")

	var names []string
	for _, req := range traces {
		for _, rs := range req.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, span := range ss.GetSpans() {
					if bytes.Equal(span.GetSpanId(), primary) {
						names = append(names, span.GetName())
					}
				}
			}
		}
	}
	require.NotEmpty(t, names, "the record targets the session CLI's own command span")
	require.Equal(t, title, names[len(names)-1], "the CLI's exported command span carries the title")
}
