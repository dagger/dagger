package core

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestGroup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cacheBuster := fmt.Sprintf("%d", time.Now().UTC().UnixNano())

	t.Run("container group", func(t *testing.T) {
		var logs bytes.Buffer
		c, err := dagger.Connect(ctx, dagger.WithLogOutput(&logs))
		require.NoError(t, err)
		defer c.Close()

		_, err = c.
			Container().
			Group("container group").
			From("alpine:3.16.2").
			WithExec([]string{"echo", cacheBuster}).
			ExitCode(ctx)

		require.NoError(t, err)
		require.Contains(t, logs.String(), "container group")
	})

	t.Run("directory group", func(t *testing.T) {
		var logs bytes.Buffer
		c, err := dagger.Connect(ctx, dagger.WithLogOutput(&logs))
		require.NoError(t, err)
		defer c.Close()

		contents, err := c.
			Directory().
			Group("directory group").
			WithNewFile("/foo", cacheBuster).
			File("/foo").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, contents, cacheBuster)
		// FIXME: Wait for logs to be flushed out
		time.Sleep(100 * time.Millisecond)
		require.Contains(t, logs.String(), "directory group")
	})
}
