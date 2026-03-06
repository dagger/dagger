package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectMissingConfigDoesNotReadFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{
		"/repo/app/.dagger": {},
		"/repo/.git":        {},
	}

	readCalls := 0
	ws, err := Detect(ctx, fakePathExists(existing), func(context.Context, string) ([]byte, error) {
		readCalls++
		return nil, os.ErrNotExist
	}, "/repo/app")
	require.NoError(t, err)
	require.Zero(t, readCalls, "readFile should not be called when config.toml does not exist")
	require.False(t, ws.Initialized)
	require.Equal(t, "/repo", ws.Root)
	require.Equal(t, "app", ws.Path)
	require.Nil(t, ws.Config)
}

func TestDetectReadsConfigWhenPresent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{
		"/repo/app/.dagger":             {},
		"/repo/.git":                    {},
		"/repo/app/.dagger/config.toml": {},
	}

	readCalls := 0
	ws, err := Detect(ctx, fakePathExists(existing), func(context.Context, string) ([]byte, error) {
		readCalls++
		return []byte(`
[modules]
`), nil
	}, "/repo/app")
	require.NoError(t, err)
	require.Equal(t, 1, readCalls)
	require.True(t, ws.Initialized)
	require.Equal(t, "/repo", ws.Root)
	require.Equal(t, "app", ws.Path)
	require.NotNil(t, ws.Config)
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
