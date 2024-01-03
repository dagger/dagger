package dag

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	m.Run()
	Close() // close needs to be explicitly called
}

func TestDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dir := Directory()

	contents, err := dir.
		WithNewFile("/hello.txt", "world").
		File("/hello.txt").
		Contents(ctx)

	require.NoError(t, err)
	require.Equal(t, "world", contents)
}
