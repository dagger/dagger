package schema

import (
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceLockExcludePattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "apps", "api")
	lockRel := filepath.Join(".dagger", "dagger.lock")

	newWorkspace := func() *core.Workspace {
		ws := &core.Workspace{
			ClientID: "client-a",
			LockFile: lockRel,
		}
		ws.SetHostPath(workspaceRoot)
		return ws
	}

	t.Run("exact workspace path", func(t *testing.T) {
		t.Parallel()

		pattern, ok := workspaceLockExcludePattern(
			newWorkspace(),
			"client-a",
			workspaceRoot,
			workspaceRoot,
		)
		require.True(t, ok)
		require.Equal(t, lockRel, pattern)
	})

	t.Run("rebased snapshot root", func(t *testing.T) {
		t.Parallel()

		pattern, ok := workspaceLockExcludePattern(
			newWorkspace(),
			"client-a",
			root,
			workspaceRoot,
		)
		require.True(t, ok)
		require.Equal(t, filepath.Join("apps", "api", ".dagger", "dagger.lock"), pattern)
	})

	t.Run("ancestor snapshot", func(t *testing.T) {
		t.Parallel()

		pattern, ok := workspaceLockExcludePattern(
			newWorkspace(),
			"client-a",
			root,
			root,
		)
		require.True(t, ok)
		require.Equal(t, filepath.Join("apps", "api", ".dagger", "dagger.lock"), pattern)
	})

	t.Run("descendant path", func(t *testing.T) {
		t.Parallel()

		lockDir := filepath.Join(workspaceRoot, ".dagger")
		_, ok := workspaceLockExcludePattern(
			newWorkspace(),
			"client-a",
			lockDir,
			lockDir,
		)
		require.False(t, ok)
	})

	t.Run("different path", func(t *testing.T) {
		t.Parallel()

		siblingPath := filepath.Join(root, "apps", "web")
		_, ok := workspaceLockExcludePattern(
			newWorkspace(),
			"client-a",
			siblingPath,
			siblingPath,
		)
		require.False(t, ok)
	})

	t.Run("different client", func(t *testing.T) {
		t.Parallel()

		_, ok := workspaceLockExcludePattern(
			newWorkspace(),
			"client-b",
			workspaceRoot,
			workspaceRoot,
		)
		require.False(t, ok)
	})

	t.Run("remote workspace", func(t *testing.T) {
		t.Parallel()

		// Remote workspaces have no host path.
		remote := &core.Workspace{
			ClientID: "client-a",
			LockFile: lockRel,
		}
		_, ok := workspaceLockExcludePattern(remote, "client-a", root, root)
		require.False(t, ok)
	})

	t.Run("no lockfile selected", func(t *testing.T) {
		t.Parallel()

		ws := &core.Workspace{ClientID: "client-a"}
		ws.SetHostPath(workspaceRoot)
		_, ok := workspaceLockExcludePattern(ws, "client-a", workspaceRoot, workspaceRoot)
		require.False(t, ok)
	})
}
