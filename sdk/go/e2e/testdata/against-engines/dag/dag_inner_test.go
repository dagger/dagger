package dag_test

import (
	"context"
	"testing"

	"dagger.io/dagger/dag"
	"github.com/stretchr/testify/require"
)

func TestDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Cleanup(func() {
		require.NoError(t, dag.Close())
	})

	contents, err := dag.Directory().
		WithNewFile("/hello.txt", "world").
		File("/hello.txt").
		Contents(ctx)

	require.NoError(t, err)
	require.Equal(t, "world", contents)
}
