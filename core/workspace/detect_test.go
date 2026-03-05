package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestDetectMissingConfigDoesNotReadFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	exists := map[string]core.FileType{
		"/repo/app/.dagger": core.FileTypeDirectory,
		"/repo/.git":        core.FileTypeDirectory,
	}

	readCalls := 0
	ws, err := Detect(ctx, fakeStatFS(exists), func(context.Context, string) ([]byte, error) {
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
	exists := map[string]core.FileType{
		"/repo/app/.dagger":             core.FileTypeDirectory,
		"/repo/.git":                    core.FileTypeDirectory,
		"/repo/app/.dagger/config.toml": core.FileTypeRegular,
	}

	readCalls := 0
	ws, err := Detect(ctx, fakeStatFS(exists), func(context.Context, string) ([]byte, error) {
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

func fakeStatFS(exists map[string]core.FileType) core.StatFSFunc {
	return func(_ context.Context, path string) (string, *core.Stat, error) {
		cleanPath := filepath.Clean(path)
		fileType, ok := exists[cleanPath]
		if !ok {
			return "", nil, os.ErrNotExist
		}

		return filepath.Dir(cleanPath), &core.Stat{
			Name:     filepath.Base(cleanPath),
			FileType: fileType,
		}, nil
	}
}
