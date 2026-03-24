package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/stretchr/testify/require"
)

func TestPendingLegacyModule(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{Root: "/repo", Path: "."}
	resolveLocalRef := func(_ *workspace.Workspace, relPath string) string {
		return "/resolved/" + relPath
	}

	t.Run("preserves remote pin", func(t *testing.T) {
		t.Parallel()

		mod := pendingLegacyModule(
			ws,
			resolveLocalRef,
			"go",
			"github.com/acme/go-toolchain@main",
			"abc123",
			false,
			map[string]any{"foo": "bar"},
			[]*modules.ModuleConfigArgument{{
				Argument:    "config",
				DefaultPath: "./custom-config.txt",
			}},
		)

		require.Equal(t, "github.com/acme/go-toolchain@main", mod.Ref)
		require.Equal(t, "abc123", mod.RefPin)
		require.Equal(t, "go", mod.Name)
		require.False(t, mod.Entrypoint)
		require.True(t, mod.LegacyDefaultPath)
		require.Equal(t, map[string]any{"foo": "bar"}, mod.ConfigDefaults)
		require.Len(t, mod.ArgCustomizations, 1)
		require.Equal(t, "./custom-config.txt", mod.ArgCustomizations[0].DefaultPath)
	})

	t.Run("resolves local refs without ref pin", func(t *testing.T) {
		t.Parallel()

		mod := pendingLegacyModule(
			ws,
			resolveLocalRef,
			"blueprint",
			"../blueprint",
			"",
			true,
			nil,
			nil,
		)

		require.Equal(t, "/resolved/../blueprint", mod.Ref)
		require.Empty(t, mod.RefPin)
		require.Equal(t, "blueprint", mod.Name)
		require.True(t, mod.Entrypoint)
		require.True(t, mod.LegacyDefaultPath)
		require.Nil(t, mod.ConfigDefaults)
	})
}

func TestWorkspaceConfigPendingModules(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{Root: "/repo", Path: "."}
	resolveLocalRef := func(_ *workspace.Workspace, relPath string) string {
		return "/resolved/" + filepath.Clean(relPath)
	}

	pending, err := workspaceConfigPendingModules(ws, &workspace.Config{
		DefaultsFromDotEnv: true,
		Modules: map[string]workspace.ModuleEntry{
			"zeta": {
				Source:    "github.com/acme/zeta@main",
				Blueprint: true,
				Config:    map[string]any{"message": "hello"},
			},
			"alpha": {
				Source: "modules/alpha",
			},
		},
	}, resolveLocalRef)
	require.NoError(t, err)
	require.Len(t, pending, 2)

	require.Equal(t, "alpha", pending[0].Name)
	require.Equal(t, "/resolved/.dagger/modules/alpha", pending[0].Ref)
	require.Empty(t, pending[0].RefPin)
	require.False(t, pending[0].Entrypoint)
	require.True(t, pending[0].DisableFindUp)
	require.True(t, pending[0].DefaultsFromDotEnv)

	require.Equal(t, "zeta", pending[1].Name)
	require.Equal(t, "github.com/acme/zeta@main", pending[1].Ref)
	require.Empty(t, pending[1].RefPin)
	require.True(t, pending[1].Entrypoint)
	require.True(t, pending[1].DisableFindUp)
	require.True(t, pending[1].DefaultsFromDotEnv)
	require.Equal(t, map[string]any{"message": "hello"}, pending[1].ConfigDefaults)
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

func TestBuildCoreWorkspaceIncludesConfigState(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "main-client",
	})

	t.Run("initialized workspace", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:        "/repo",
			Path:        "services/payment",
			Initialized: true,
		}, true, dagql.ObjectResult[*core.Directory]{}, "")
		require.NoError(t, err)
		require.Equal(t, "file:///repo/services/payment", ws.Address)
		require.Equal(t, "services/payment", ws.Path)
		require.True(t, ws.Initialized)
		require.True(t, ws.HasConfig)
		require.Equal(t, filepath.Join("services/payment", workspace.LockDirName, workspace.ConfigFileName), ws.ConfigPath)
		require.Equal(t, "/repo", ws.HostPath())
	})

	t.Run("uninitialized workspace", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root: "/repo",
			Path: ".",
		}, true, dagql.ObjectResult[*core.Directory]{}, "")
		require.NoError(t, err)
		require.False(t, ws.Initialized)
		require.False(t, ws.HasConfig)
		require.Empty(t, ws.ConfigPath)
	})
}

func TestNestedClientMetadata(t *testing.T) {
	t.Parallel()

	execMD := &buildkit.ExecutionMetadata{
		ClientID:          "nested-client",
		SessionID:         "session",
		SecretToken:       "secret",
		Hostname:          "nested-host",
		ClientStableID:    "stable",
		SSHAuthSocketPath: "/tmp/ssh.sock",
		AllowedLLMModules: []string{"parent"},
	}

	t.Run("inherits forwarded lock mode", func(t *testing.T) {
		t.Parallel()

		md := nestedClientMetadata(execMD, &engine.ClientMetadata{
			ClientVersion:        "v-test",
			AllowedLLMModules:    []string{"child"},
			ExtraModules:         []engine.ExtraModule{{Ref: "github.com/dagger/mod", Entrypoint: true}},
			SkipWorkspaceModules: true,
			LockMode:             string(workspace.LockModeLive),
			EagerRuntime:         true,
			Workspace:            stringPtr("github.com/dagger/dagger@main"),
		}, string(workspace.LockModeFrozen))

		require.Equal(t, "nested-client", md.ClientID)
		require.Equal(t, "v-test", md.ClientVersion)
		require.Equal(t, []string{"child"}, md.AllowedLLMModules)
		require.Equal(t, string(workspace.LockModeLive), md.LockMode)
		require.True(t, md.SkipWorkspaceModules)
		require.True(t, md.EagerRuntime)
		require.Equal(t, "github.com/dagger/dagger@main", *md.Workspace)
		require.Len(t, md.ExtraModules, 1)
	})

	t.Run("falls back to caller lock mode when child omits it", func(t *testing.T) {
		t.Parallel()

		md := nestedClientMetadata(execMD, &engine.ClientMetadata{
			ClientVersion:     "v-test",
			AllowedLLMModules: []string{"child"},
		}, string(workspace.LockModeLive))

		require.Equal(t, string(workspace.LockModeLive), md.LockMode)
	})

	t.Run("falls back to execution metadata defaults", func(t *testing.T) {
		t.Parallel()

		md := nestedClientMetadata(execMD, nil, "")

		require.Equal(t, engine.Version, md.ClientVersion)
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
		require.Empty(t, md.LockMode)
		require.Nil(t, md.Workspace)
	})
}

func TestInheritedNestedClientLockMode(t *testing.T) {
	t.Parallel()

	t.Run("prefers execution metadata lock mode", func(t *testing.T) {
		t.Parallel()

		srv := &Server{}
		mode := srv.inheritedNestedClientLockMode(&buildkit.ExecutionMetadata{
			LockMode:       string(workspace.LockModeLive),
			SessionID:      "session",
			CallerClientID: "caller",
		})

		require.Equal(t, string(workspace.LockModeLive), mode)
	})
}

func TestLocalWorkspaceAddress(t *testing.T) {
	t.Parallel()

	require.Equal(t, "file:///repo", localWorkspaceAddress("/repo", "."))
	require.Equal(t, "file:///repo/services/payment", localWorkspaceAddress("/repo", "services/payment"))
}

func TestRemoteWorkspaceAddress(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://github.com/dagger/dagger@main", remoteWorkspaceAddress("https://github.com/dagger/dagger", ".", "main"))
	require.Equal(t, "https://github.com/dagger/dagger/services/payment@main", remoteWorkspaceAddress("https://github.com/dagger/dagger", "services/payment", "main"))
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

func TestGatherModuleLoadRequests(t *testing.T) {
	t.Parallel()

	loads := gatherModuleLoadRequests(
		[]pendingModule{
			{Ref: "github.com/acme/a", Name: "a"},
			{Ref: "github.com/acme/b", Name: "b"},
		},
		[]engine.ExtraModule{
			{Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true},
			{Ref: "github.com/acme/extra2", Name: "extra2"},
		},
	)

	require.Len(t, loads, 4)
	require.False(t, loads[0].extra)
	require.False(t, loads[1].extra)
	require.True(t, loads[2].extra)
	require.True(t, loads[3].extra)

	require.Equal(t, "github.com/acme/a", loads[0].mod.Ref)
	require.Equal(t, "github.com/acme/b", loads[1].mod.Ref)
	require.Equal(t, "github.com/acme/extra1", loads[2].mod.Ref)
	require.Equal(t, "github.com/acme/extra2", loads[3].mod.Ref)
	require.True(t, loads[2].mod.Entrypoint)
}

func TestModuleResolveParallelism(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, moduleResolveParallelism(0))
	require.Equal(t, 1, moduleResolveParallelism(1))
	require.Equal(t, 3, moduleResolveParallelism(3))
	require.Equal(t, maxParallelModuleResolves, moduleResolveParallelism(maxParallelModuleResolves+4))
}

func TestModuleLoadErr(t *testing.T) {
	t.Parallel()

	err := errors.New("boom")

	normal := moduleLoadErr(moduleLoadRequest{mod: pendingModule{Ref: "github.com/acme/mod"}}, err)
	require.ErrorContains(t, normal, `loading module "github.com/acme/mod": boom`)

	extra := moduleLoadErr(moduleLoadRequest{
		mod:   pendingModule{Ref: "github.com/acme/extra"},
		extra: true,
	}, err)
	require.ErrorContains(t, extra, `loading extra module "github.com/acme/extra": boom`)
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
