package core

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestPipeline(t *testing.T) {
	cacheBuster := fmt.Sprintf("%d", time.Now().UTC().UnixNano())

	t.Run("container pipeline", func(t *testing.T) {
		t.Parallel()

		c, ctx, logs := connectWithLogs(t)

		_, err := c.
			Container().
			Pipeline("container pipeline").
			From(alpineImage).
			WithExec([]string{"echo", cacheBuster}).
			Sync(ctx)

		require.NoError(t, err)

		require.NoError(t, c.Close()) // close + flush logs

		require.Contains(t, logs.String(), "container pipeline")
	})

	t.Run("directory pipeline", func(t *testing.T) {
		t.Parallel()

		c, ctx, logs := connectWithLogs(t)

		contents, err := c.
			Directory().
			Pipeline("directory pipeline").
			WithNewFile("/foo", cacheBuster).
			File("/foo").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, contents, cacheBuster)

		require.NoError(t, c.Close()) // close + flush logs

		require.Contains(t, logs.String(), "directory pipeline")
	})

	t.Run("service pipeline", func(t *testing.T) {
		t.Parallel()

		c, ctx, logs := connectWithLogs(t)

		srv, url := httpService(ctx, t, c, "Hello, world!")

		hostname, err := srv.Hostname(ctx)
		require.NoError(t, err)

		client := c.Container().
			From(alpineImage).
			WithServiceBinding("www", srv).
			WithExec([]string{"apk", "add", "curl"}).
			WithExec([]string{"curl", "-v", url})

		_, err = client.Sync(ctx)
		require.NoError(t, err)

		require.NoError(t, c.Close()) // close + flush logs

		require.Contains(t, logs.String(), "service "+hostname)
		require.Regexp(t, `start python -m http.server.*DONE`, logs.String())
	})
}

func TestInternalVertexes(t *testing.T) {
	t.Parallel()

	cacheBuster := fmt.Sprintf("%d", time.Now().UTC().UnixNano())

	t.Run("merge pipeline", func(t *testing.T) {
		t.Parallel()

		c, ctx, logs := connectWithLogs(t)

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
		require.NotContains(t, logs.String(), "merge")
	})
}

func connectWithLogs(t *testing.T, opts ...dagger.ClientOpt) (*dagger.Client, context.Context, *safeBuffer) {
	var logs safeBuffer
	out := io.MultiWriter(&logs, newTWriter(t))
	c, ctx := connect(t, append(opts, dagger.WithLogOutput(out))...)
	return c, ctx, &logs
}
