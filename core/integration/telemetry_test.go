package core

import (
	"context"
	"fmt"
	"regexp"
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

func (TelemetrySuite) TestMetrics(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t,
		dagger.WithLogOutput(&logs),
		dagger.WithVerbosity(3), // bump to -vvv so metrics show up
	)

	cmd := "dd if=/f of=/f2 iflag=direct oflag=direct bs=1M count=1 && sync && sleep 10"
	_, err := c.Container().From(alpineImage).
		WithEnvVariable("BUST", fmt.Sprintf("%d", time.Now().UTC().UnixNano())).
		WithExec([]string{"sh", "-c",
			"dd if=/dev/zero of=/f bs=1M count=1",
		}).
		WithExec([]string{"sh", "-c", cmd}).
		Sync(ctx)
	require.NoError(t, err)

	require.NoError(t, c.Close()) // close + flush logs
	require.Regexp(t,
		regexp.MustCompile(`.*exec sh -c `+cmd+`.*\Disk Read Bytes:\s*\d{7}.*Disk Write Bytes:\s*\d{7}.*`),
		logs.String(),
	)
}
