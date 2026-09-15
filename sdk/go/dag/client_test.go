package dag

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	res := m.Run()

	// close needs to be explicitly called
	if err := Close(); err != nil {
		if res == 0 {
			res = 1
		}
	}

	os.Exit(res)
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
