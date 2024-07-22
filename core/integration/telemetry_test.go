package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type TelemetrySuite struct{}

func TestTelemetry(t *testing.T) {
	testctx.Run(testCtx, t, TelemetrySuite{}, Middleware()...)
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
