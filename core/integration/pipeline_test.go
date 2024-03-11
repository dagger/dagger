package core

import (
	"fmt"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestPipeline(t *testing.T) {
	t.Parallel()

	cacheBuster := fmt.Sprintf("%d", time.Now().UTC().UnixNano())

	t.Run("client pipeline", func(t *testing.T) {
		t.Parallel()

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(&logs))

		_, err := c.
			Pipeline("client pipeline").
			Container().
			From(alpineImage).
			WithExec([]string{"echo", cacheBuster}).
			Sync(ctx)

		require.NoError(t, err)

		require.NoError(t, c.Close()) // close + flush logs

		t.Log(logs.String())
		require.Contains(t, logs.String(), "client pipeline")
	})

	t.Run("container pipeline", func(t *testing.T) {
		t.Parallel()

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(&logs))

		_, err := c.
			Container().
			Pipeline("container pipeline").
			From(alpineImage).
			WithExec([]string{"echo", cacheBuster}).
			Sync(ctx)

		require.NoError(t, err)

		require.NoError(t, c.Close()) // close + flush logs

		t.Log(logs.String())
		require.Contains(t, logs.String(), "container pipeline")
	})

	t.Run("directory pipeline", func(t *testing.T) {
		t.Parallel()

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(&logs))

		contents, err := c.
			Directory().
			Pipeline("directory pipeline").
			WithNewFile("/foo", cacheBuster).
			File("/foo").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, contents, cacheBuster)

		require.NoError(t, c.Close()) // close + flush logs

		t.Log(logs.String())
		require.Contains(t, logs.String(), "directory pipeline")
	})

	t.Run("service pipeline", func(t *testing.T) {
		t.Parallel()

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(&logs))

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

		_, err = srv.Stop(ctx) // FIXME: shouldn't need this, but test is flaking
		require.NoError(t, err)

		require.NoError(t, c.Close()) // close + flush logs

		t.Log(logs.String())
		require.Contains(t, logs.String(), "service "+hostname)
		require.Regexp(t, `start python -m http.server.*DONE`, logs.String())
	})
}

func TestPipelineGraphQLClient(t *testing.T) {
	t.Parallel()

	c, _ := connect(t)
	require.NotNil(t, c.GraphQLClient())
	require.NotNil(t, c.Pipeline("client pipeline").GraphQLClient())
}

func TestInternalVertexes(t *testing.T) {
	t.Parallel()

	cacheBuster := fmt.Sprintf("%d", time.Now().UTC().UnixNano())

	t.Run("merge pipeline", func(t *testing.T) {
		t.Parallel()

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(&logs))

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
