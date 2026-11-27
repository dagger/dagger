package schema

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestOwnerSSHAuthSocketPath(t *testing.T) {
	t.Parallel()
	owner := &engine.ClientMetadata{ClientID: "owner"}
	var prepared []string
	prepare := func(ctx context.Context, remote string) (string, error) {
		md, err := engine.ClientMetadataFromContext(ctx)
		require.NoError(t, err)
		require.Equal(t, "owner", md.ClientID, "the owner prepares its own agent")
		prepared = append(prepared, remote)
		return "/tmp/agent.sock", nil
	}

	// An approved restored load of a recipe without a remote, for an owner
	// without SSH_AUTH_SOCK whose checkout has an SSH origin (named by the
	// authorization): an agent is prepared for that origin.
	path, err := ownerSSHAuthSocketPath(t.Context(), &core.SSHAgentAuthorization{Owner: owner, Remote: "git@github.com:me/repo.git"}, prepare)
	require.NoError(t, err)
	require.Equal(t, "/tmp/agent.sock", path)
	require.Equal(t, []string{"git@github.com:me/repo.git"}, prepared)

	// The owner's SSH_AUTH_SOCK needs nothing prepared.
	path, err = ownerSSHAuthSocketPath(t.Context(), &core.SSHAgentAuthorization{
		Owner: &engine.ClientMetadata{ClientID: "owner", SSHAuthSocketPath: "/run/ssh.sock"},
	}, prepare)
	require.NoError(t, err)
	require.Equal(t, "/run/ssh.sock", path)
	require.Len(t, prepared, 1)

	// Without a remote to prepare for, it says so.
	_, err = ownerSSHAuthSocketPath(t.Context(), &core.SSHAgentAuthorization{Owner: owner}, prepare)
	require.ErrorContains(t, err, "no SSH_AUTH_SOCK, and neither the call nor the owner's checkout names an SSH remote")
	require.Len(t, prepared, 1)

	// A failed preparation names the remote.
	_, err = ownerSSHAuthSocketPath(t.Context(), &core.SSHAgentAuthorization{Owner: owner, Remote: "git@github.com:me/repo.git"},
		func(context.Context, string) (string, error) { return "", errors.New("no SSH identity files found") })
	require.ErrorContains(t, err, `using your SSH keys for "git@github.com:me/repo.git"`)
	require.ErrorContains(t, err, "no SSH identity files found")
}

func TestWorkspaceLockExcludePattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "apps", "api")
	lockRel := workspace.LockFileName

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
		require.Equal(t, filepath.Join("apps", "api", workspace.LockFileName), pattern)
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
		require.Equal(t, filepath.Join("apps", "api", workspace.LockFileName), pattern)
	})

	t.Run("descendant path", func(t *testing.T) {
		t.Parallel()

		lockDir := filepath.Join(workspaceRoot, "subdir")
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
