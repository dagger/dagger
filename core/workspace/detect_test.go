package workspace

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectInitializedWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{
		"/repo/app/.dagger":             {},
		"/repo/app/.dagger/config.toml": {},
		"/repo/.git":                    {},
	}

	ws, err := Detect(ctx, fakePathExists(existing), "/repo/app")
	require.NoError(t, err)
	require.Equal(t, "/repo", ws.Root)
	require.Equal(t, "app", ws.Cwd)
	require.Equal(t, "app/.dagger/config.toml", ws.ConfigFile)
	require.Equal(t, "app/.dagger/lock", ws.LockFile)
}

func TestDetectInitializedWorkspaceFromNestedCwd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{
		"/repo/.dagger":             {},
		"/repo/.dagger/config.toml": {},
		"/repo/.git":                {},
	}

	ws, err := Detect(ctx, fakePathExists(existing), "/repo/app/sub")
	require.NoError(t, err)
	require.Equal(t, "/repo", ws.Root)
	require.Equal(t, "app/sub", ws.Cwd)
	require.Equal(t, ".dagger/config.toml", ws.ConfigFile)
	require.Equal(t, ".dagger/lock", ws.LockFile)
}

func TestDetectMissingConfigDoesNotChangeBoundary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{
		"/repo/app/.dagger": {},
		"/repo/.git":        {},
	}

	ws, err := Detect(ctx, fakePathExists(existing), "/repo/app/sub")
	require.NoError(t, err)
	require.Equal(t, "/repo", ws.Root)
	require.Equal(t, "app/sub", ws.Cwd)
	require.Empty(t, ws.ConfigFile)
	require.Equal(t, "app/.dagger/lock", ws.LockFile)
}

func TestDetectUsesExistingLockFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{
		"/repo/.dagger":      {},
		"/repo/.dagger/lock": {},
		"/repo/.git":         {},
	}

	ws, err := Detect(ctx, fakePathExists(existing), "/repo/app/sub")
	require.NoError(t, err)
	require.Equal(t, "/repo", ws.Root)
	require.Equal(t, "app/sub", ws.Cwd)
	require.Empty(t, ws.ConfigFile)
	require.Equal(t, ".dagger/lock", ws.LockFile)
}

func TestDetectReturnsNilWithoutGit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existing := map[string]struct{}{}

	ws, err := Detect(ctx, fakePathExists(existing), "/repo/app")
	require.NoError(t, err)
	require.Nil(t, ws)
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
