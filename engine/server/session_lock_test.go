package server

import (
	"context"
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

func TestEnsureWorkspaceLoadedInheritsParentWorkspace(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	bound := &core.Workspace{
		Path:     ".",
		ClientID: "parent-client",
	}

	parent := &daggerClient{
		workspace: bound,
	}
	child := &daggerClient{
		parents: []*daggerClient{parent},
	}

	require.NoError(t, srv.ensureWorkspaceLoaded(context.Background(), child))
	require.Same(t, bound, child.workspace)
}

func TestEnsureWorkspaceLoadedKeepsExistingWorkspaceBinding(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	existing := &core.Workspace{
		Path:     ".",
		ClientID: "child-client",
	}
	parentBound := &core.Workspace{
		Path:     ".",
		ClientID: "parent-client",
	}

	parent := &daggerClient{
		workspace: parentBound,
	}
	child := &daggerClient{
		workspace: existing,
		parents:   []*daggerClient{parent},
	}

	require.NoError(t, srv.ensureWorkspaceLoaded(context.Background(), child))
	require.Same(t, existing, child.workspace)
}

func TestWorkspaceBindingMode(t *testing.T) {
	t.Parallel()

	t.Run("declared workspace takes precedence", func(t *testing.T) {
		t.Parallel()

		client := &daggerClient{
			pendingWorkspaceLoad: false,
			clientMetadata: &engine.ClientMetadata{
				Workspace: stringPtr("github.com/dagger/dagger@main"),
			},
		}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingDeclared, mode)
		require.Equal(t, "github.com/dagger/dagger@main", workspaceRef)
	})

	t.Run("non-module defaults to host detection", func(t *testing.T) {
		t.Parallel()

		client := &daggerClient{
			pendingWorkspaceLoad: true,
			clientMetadata:       &engine.ClientMetadata{},
		}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingDetectHost, mode)
		require.Equal(t, "", workspaceRef)
	})

	t.Run("module defaults to inheritance", func(t *testing.T) {
		t.Parallel()

		client := &daggerClient{
			pendingWorkspaceLoad: false,
			clientMetadata:       &engine.ClientMetadata{},
		}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingInherit, mode)
		require.Equal(t, "", workspaceRef)
	})
}

func TestParseWorkspaceRemoteRef(t *testing.T) {
	t.Parallel()

	t.Run("supports address fragment ref", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.cloneRef)
		require.Equal(t, "main", ref.version)
		require.Equal(t, ".", ref.workspaceSubdir)
	})

	t.Run("supports address fragment ref and subdir", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main:toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.cloneRef)
		require.Equal(t, "main", ref.version)
		require.Equal(t, "toolchains/changelog", ref.workspaceSubdir)
	})

	t.Run("supports legacy at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "github.com/dagger/dagger/toolchains/changelog@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.version)
		require.Equal(t, "toolchains/changelog", ref.workspaceSubdir)
	})

	t.Run("preserves legacy https at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.version)
		require.Equal(t, ".", ref.workspaceSubdir)
	})
}

func TestNormalizeWorkspaceRemoteSubdir(t *testing.T) {
	t.Parallel()

	t.Run("empty becomes dot", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("")
		require.NoError(t, err)
		require.Equal(t, ".", got)
	})

	t.Run("absolute gets normalized to relative", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("/toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "toolchains/changelog", got)
	})

	t.Run("rejects escaping paths", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeWorkspaceRemoteSubdir("../outside")
		require.ErrorContains(t, err, "outside repository")
	})
}

func stringPtr(v string) *string {
	return &v
}
