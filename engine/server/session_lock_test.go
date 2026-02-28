package server

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceLockMode(t *testing.T) {
	t.Parallel()

	mode, err := parseWorkspaceLockMode(nil)
	require.NoError(t, err)
	require.Equal(t, workspace.LockModeAuto, mode)

	mode, err = parseWorkspaceLockMode(&engine.ClientMetadata{LockMode: string(workspace.LockModeStrict)})
	require.NoError(t, err)
	require.Equal(t, workspace.LockModeStrict, mode)

	_, err = parseWorkspaceLockMode(&engine.ClientMetadata{LockMode: "weird"})
	require.ErrorContains(t, err, "invalid lock mode")
}

func TestResolveWorkspaceModuleLookup(t *testing.T) {
	t.Parallel()

	const source = "github.com/acme/mod@main"

	makeLock := func(t *testing.T, pin string, policy workspace.LockPolicy) *workspace.Lock {
		t.Helper()
		lock := workspace.NewLock()
		require.NoError(t, lock.SetModuleResolve(source, pin, policy))
		return lock
	}

	t.Run("entry exists with pin policy", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "abc123", workspace.PolicyPin)

		pin, policy, err := resolveWorkspaceModuleLookup(workspace.LockModeStrict, lock, source, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Equal(t, "abc123", pin)
		require.Equal(t, workspace.PolicyPin, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeAuto, lock, source, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Equal(t, "abc123", pin)
		require.Equal(t, workspace.PolicyPin, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeUpdate, lock, source, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyPin, policy)
	})

	t.Run("entry exists with float policy", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "abc123", workspace.PolicyFloat)

		pin, policy, err := resolveWorkspaceModuleLookup(workspace.LockModeStrict, lock, source, workspace.PolicyPin)
		require.NoError(t, err)
		require.Equal(t, "abc123", pin)
		require.Equal(t, workspace.PolicyFloat, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeAuto, lock, source, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyFloat, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeUpdate, lock, source, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyFloat, policy)
	})

	t.Run("entry missing with requested pin policy", func(t *testing.T) {
		t.Parallel()

		pin, policy, err := resolveWorkspaceModuleLookup(workspace.LockModeStrict, nil, source, workspace.PolicyPin)
		require.ErrorContains(t, err, "missing lock entry")
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyPin, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeAuto, nil, source, workspace.PolicyPin)
		require.ErrorContains(t, err, "missing lock entry for pinned")
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyPin, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeUpdate, nil, source, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyPin, policy)
	})

	t.Run("entry missing with requested float policy", func(t *testing.T) {
		t.Parallel()

		pin, policy, err := resolveWorkspaceModuleLookup(workspace.LockModeStrict, nil, source, workspace.PolicyFloat)
		require.ErrorContains(t, err, "missing lock entry")
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyFloat, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeAuto, nil, source, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyFloat, policy)

		pin, policy, err = resolveWorkspaceModuleLookup(workspace.LockModeUpdate, nil, source, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, pin)
		require.Equal(t, workspace.PolicyFloat, policy)
	})
}

func TestIsSameModuleReference(t *testing.T) {
	t.Parallel()

	local := func(contextPath, rootSubpath, sourceSubpath string) *core.ModuleSource {
		return &core.ModuleSource{
			Kind:              core.ModuleSourceKindLocal,
			Local:             &core.LocalModuleSource{ContextDirectoryPath: contextPath},
			SourceRootSubpath: rootSubpath,
			SourceSubpath:     sourceSubpath,
		}
	}

	t.Run("same local source root and pin", func(t *testing.T) {
		t.Parallel()
		a := local("/work/mod", ".", ".")
		b := local("/work/mod", ".", ".")
		require.True(t, isSameModuleReference(a, b))
	})

	t.Run("different local source", func(t *testing.T) {
		t.Parallel()
		a := local("/work/mod-a", ".", ".")
		b := local("/work/mod-b", ".", ".")
		require.False(t, isSameModuleReference(a, b))
	})

	t.Run("same module through different local refs", func(t *testing.T) {
		t.Parallel()
		// a points at the workspace root where dagger.json has sourceSubpath
		// ".dagger/modules/dagger-dev". b points directly at that module dir.
		a := local("/root/src/dagger", ".", ".dagger/modules/dagger-dev")
		b := local("/root/src/dagger/.dagger/modules/dagger-dev", ".", ".")
		require.True(t, isSameModuleReference(a, b))
	})
}
