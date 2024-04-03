package core

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

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
