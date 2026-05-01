package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectIgnoresWorkspaceConfigAndDoesNotReadFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{
		"/repo/app/.dagger":             {},
		"/repo/app/.dagger/config.toml": {},
		"/repo/.git":                    {},
	}

	readCalls := 0
	ws, err := Detect(ctx, fakePathExists(existing), func(context.Context, string) ([]byte, error) {
		readCalls++
		return nil, os.ErrNotExist
	}, "/repo/app")
	require.NoError(t, err)
	require.Zero(t, readCalls, "readFile should not be called in the no-config split")
	require.Equal(t, "/repo", ws.Root)
	require.Equal(t, "app", ws.Path)
	require.True(t, ws.HasGit)
}

func TestDetectFallsBackToCwdWithoutGit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{}

	readCalls := 0
	ws, err := Detect(ctx, fakePathExists(existing), func(context.Context, string) ([]byte, error) {
		readCalls++
		return nil, os.ErrNotExist
	}, "/repo/app")
	require.NoError(t, err)
	require.Zero(t, readCalls, "readFile should not be called in the no-config split")
	require.Equal(t, "/repo/app", ws.Root)
	require.Equal(t, ".", ws.Path)
	require.False(t, ws.HasGit)
}

func fakePathExists(existing map[string]struct{}) PathExistsFunc {
	return func(_ context.Context, path string) (string, bool, error) {
		cleanPath := filepath.Clean(path)
		if _, ok := existing[cleanPath]; ok {
			return filepath.Dir(cleanPath), true, nil
		}
		return "", false, nil
	}
}
