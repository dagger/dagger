package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeSessionCaller struct {
	id   string
	conn *grpc.ClientConn
}

func (caller *fakeSessionCaller) Supports(string) bool {
	return false
}

func (caller *fakeSessionCaller) Conn() *grpc.ClientConn {
	return caller.conn
}

func TestPendingLegacyModule(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{Root: "/repo", Cwd: "."}
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
		require.Equal(t, "/resolved/.", mod.DefaultPathContextSourceRef)
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
		require.Equal(t, "/resolved/.", mod.DefaultPathContextSourceRef)
		require.Nil(t, mod.ConfigDefaults)
	})
}

func TestFilterPendingWorkspaceModulesForRootFields(t *testing.T) {
	t.Parallel()

	mods := []pendingModule{
		{Kind: moduleLoadKindAmbient, Name: "foo", Entrypoint: false},
		{Kind: moduleLoadKindAmbient, Name: "bar-baz", Entrypoint: true},
		{Kind: moduleLoadKindCWD, Name: "local", Entrypoint: true},
	}

	t.Run("constructor match loads only matching module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, []string{"foo"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("unknown root field with multiple entrypoints loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, []string{"doThing"})
		require.Equal(t, mods, filtered)
	})

	t.Run("unknown root field with one entrypoint loads entrypoint", func(t *testing.T) {
		t.Parallel()

		oneEntrypoint := []pendingModule{mods[0], mods[1]}
		filtered := filterPendingWorkspaceModulesForRootFields(oneEntrypoint, []string{"doThing"})
		require.Equal(t, []pendingModule{mods[1]}, filtered)
	})

	t.Run("introspection loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, []string{"__schema"})
		require.Equal(t, mods, filtered)
	})

	t.Run("current typedefs loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, []string{"currentTypeDefs"})
		require.Equal(t, mods, filtered)
	})

	t.Run("current module loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, []string{"currentModule"})
		require.Equal(t, mods, filtered)
	})

	t.Run("core-only query loads none", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, []string{"container", "version"})
		require.Empty(t, filtered)
	})
}

func TestWorkspaceConfigPendingModules(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{
		Root:       "/repo",
		Cwd:        ".",
		ConfigFile: filepath.Join(workspace.LockDirName, workspace.ConfigFileName),
		LockFile:   filepath.Join(workspace.LockDirName, workspace.LockFileName),
	}
	resolveLocalRef := func(_ *workspace.Workspace, relPath string) string {
		return filepath.Join("/resolved", relPath)
	}

	pending := workspaceConfigPendingModules(ws, &workspace.Config{
		DefaultsFromDotEnv: true,
		Modules: map[string]workspace.ModuleEntry{
			"zeta": {
				Source:     "github.com/acme/zeta@main",
				Entrypoint: true,
				Settings:   map[string]any{"message": "hello"},
			},
			"alpha": {
				Source:            "modules/alpha",
				LegacyDefaultPath: true,
			},
		},
	}, resolveLocalRef)
	require.Len(t, pending, 2)

	require.Equal(t, "alpha", pending[0].Name)
	require.Equal(t, "/resolved/.dagger/modules/alpha", pending[0].Ref)
	require.Empty(t, pending[0].RefPin)
	require.False(t, pending[0].Entrypoint)
	require.True(t, pending[0].DisableFindUp)
	require.True(t, pending[0].LegacyDefaultPath)
	require.Equal(t, "/resolved", pending[0].DefaultPathContextSourceRef)
	require.True(t, pending[0].DefaultsFromDotEnv)

	require.Equal(t, "zeta", pending[1].Name)
	require.Equal(t, "github.com/acme/zeta@main", pending[1].Ref)
	require.Empty(t, pending[1].RefPin)
	require.True(t, pending[1].Entrypoint)
	require.True(t, pending[1].DisableFindUp)
	require.False(t, pending[1].LegacyDefaultPath)
	require.Empty(t, pending[1].DefaultPathContextSourceRef)
	require.True(t, pending[1].DefaultsFromDotEnv)
	require.Equal(t, map[string]any{"message": "hello"}, pending[1].ConfigDefaults)
}

// TestModuleResolutionFromSubdirectory verifies that module source paths from
// dagger.json are resolved relative to the config file location, not the
// client's working directory. When a client connects from sdk/go/, a module
// with source "modules/changelog" should resolve to /repo/modules/changelog,
// not /repo/sdk/go/modules/changelog.
func TestModuleResolutionFromSubdirectory(t *testing.T) {
	t.Parallel()

	// Filesystem layout:
	//   /repo/.git                  (git root)
	//   /repo/dagger.json           (config declaring a module)
	//   /repo/sdk/go/               (client CWD)

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	// The "toolchains" field is the current config mechanism for declaring
	// workspace modules in dagger.json.
	daggerJSON := `{
		"name": "myproject",
		"toolchains": [
			{"name": "changelog", "source": "modules/changelog"}
		]
	}`

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(daggerJSON), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		return filepath.Join(ws.Root, relPath)
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/sdk/go", // CWD is a subdirectory
		resolveLocalRef,
		nil,
		true, // isLocal
	)
	require.NoError(t, err)
	require.Equal(t, "sdk/go", client.workspace.Cwd)

	// Module source must resolve relative to dagger.json (/repo),
	// not relative to CWD (/repo/sdk/go).
	require.Len(t, client.pendingModules, 1)
	require.Equal(t, "/repo/modules/changelog", client.pendingModules[0].Ref)
	require.Equal(t, "changelog", client.pendingModules[0].Name)
}

func TestDetectAndLoadWorkspaceIgnoresCWDModuleWhenConfigExists(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":                              true,
		"/repo/.dagger/config.toml":               true,
		"/repo/mymod/dagger.json":                 true,
		"/repo/.dagger/modules/local":             true,
		"/repo/.dagger/modules/local/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		switch filepath.Clean(path) {
		case "/repo/.dagger/config.toml":
			return []byte(`[modules.dev]
source = "github.com/acme/dev@main"
entrypoint = true

[modules.local]
source = "modules/local"
`), nil
		case "/repo/mymod/dagger.json":
			return []byte(`{"name":"mymod"}`), nil
		default:
			return nil, os.ErrNotExist
		}
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.Equal(t, "mymod", client.workspace.Cwd)
	require.Equal(t, filepath.Join(workspace.LockDirName, workspace.ConfigFileName), client.workspace.ConfigFile)

	require.Len(t, client.pendingModules, 2)
	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[0].Kind)
	require.Equal(t, "dev", client.pendingModules[0].Name)
	require.Equal(t, "github.com/acme/dev@main", client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)

	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[1].Kind)
	require.Equal(t, "local", client.pendingModules[1].Name)
	require.Equal(t, "/repo/.dagger/modules/local", client.pendingModules[1].Ref)
	require.False(t, client.pendingModules[1].Entrypoint)
}

func TestDetectAndLoadWorkspaceLoadsCWDModuleWithoutConfig(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":              true,
		"/repo/mymod/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/mymod/dagger.json" {
			return []byte(`{"name":"mymod"}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.Empty(t, client.workspace.ConfigFile)

	require.Len(t, client.pendingModules, 1)
	require.Equal(t, moduleLoadKindCWD, client.pendingModules[0].Kind)
	require.Equal(t, "mymod", client.pendingModules[0].Name)
	require.Equal(t, "/repo/mymod", client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)
}

func TestDetectAndLoadWorkspaceLoadsCWDModuleWithoutWorkspace(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/tmp/mymod/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/tmp/mymod/dagger.json" {
			return []byte(`{"name":"mymod"}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/tmp/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.Nil(t, client.workspace)

	require.Len(t, client.pendingModules, 1)
	require.Equal(t, moduleLoadKindCWD, client.pendingModules[0].Kind)
	require.Equal(t, "mymod", client.pendingModules[0].Name)
	require.Equal(t, "/tmp/mymod", client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)
}

func TestRemoteWorkspaceCwdUsesDetectionStart(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		".dagger/config.toml": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == ".dagger/config.toml" {
			return []byte("# workspace\n"), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, relPath)
		return core.GitRefString("github.com/acme/repo", subPath, "main")
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata:       &engine.ClientMetadata{},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspaceWithRootfs(ctx, client,
		statFS,
		readFile,
		"subdir",
		resolveLocalRef,
		func(ws *workspace.Workspace) string {
			return remoteWorkspaceAddress("github.com/acme/repo", ws.Cwd, "main")
		},
		false,
		dagql.ObjectResult[*core.Directory]{},
	)
	require.NoError(t, err)
	require.Equal(t, "subdir", client.workspace.Cwd)
	require.Equal(t, "github.com/acme/repo/subdir@main", client.workspace.Address)
	require.Equal(t, filepath.Join(".dagger", workspace.ConfigFileName), client.workspace.ConfigFile)
}

func TestRemoteWorkspaceLoadsCWDModuleFromDetectionStart(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"subdir/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "subdir/dagger.json" {
			return []byte(`{"name":"remote-mod"}`), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, relPath)
		return core.GitRefString("github.com/acme/repo", subPath, "main")
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspaceWithRootfs(ctx, client,
		statFS,
		readFile,
		"subdir/child",
		resolveLocalRef,
		func(ws *workspace.Workspace) string {
			return remoteWorkspaceAddress("github.com/acme/repo", ws.Cwd, "main")
		},
		false,
		dagql.ObjectResult[*core.Directory]{},
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("subdir", "child"), client.workspace.Cwd)

	require.Len(t, client.pendingModules, 1)
	require.Equal(t, moduleLoadKindCWD, client.pendingModules[0].Kind)
	require.Equal(t, "remote-mod", client.pendingModules[0].Name)
	require.Equal(t, core.GitRefString("github.com/acme/repo", "subdir", "main"), client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)
}

func TestDetectAndLoadWorkspaceDoesNotLoadModulesByDefault(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(`{"name":"myproject","toolchains":[{"name":"changelog","source":"modules/changelog"}]}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata:       &engine.ClientMetadata{},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/sdk/go",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
	require.Empty(t, client.pendingModules)
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
		ClientID: "child-client",
	}
	parentBound := &core.Workspace{
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

func TestResolveHostServiceCallerFallsBackToParentForSyntheticNestedClient(t *testing.T) {
	t.Parallel()

	parentCaller := &fakeSessionCaller{id: "parent"}
	parent := &daggerClient{clientID: "parent"}
	parent.getHostServiceCaller = func(ctx context.Context, id string) (engineutil.SessionCaller, error) {
		require.Equal(t, "parent", id)
		return parentCaller, nil
	}

	child := &daggerClient{
		clientID:                 "child",
		hostServiceProxyClientID: "parent",
		parents:                  []*daggerClient{parent},
	}

	child.daggerSession = &daggerSession{attachables: newSessionAttachableManager()}

	caller, err := child.resolveHostServiceCaller(context.Background(), "child")
	require.NoError(t, err)
	require.Same(t, parentCaller, caller)
}

func TestResolveHostServiceCallerPrefersCurrentClientAttachable(t *testing.T) {
	t.Parallel()

	currentCaller := &sessionAttachableCaller{
		ctx:       context.Background(),
		supported: map[string]struct{}{},
	}
	parent := &daggerClient{clientID: "parent"}
	parent.getHostServiceCaller = func(context.Context, string) (engineutil.SessionCaller, error) {
		t.Fatal("unexpected parent fallback")
		return nil, nil
	}
	attachables := newSessionAttachableManager()
	attachables.callers["child"] = currentCaller

	child := &daggerClient{
		clientID:                 "child",
		hostServiceProxyClientID: "parent",
		parents:                  []*daggerClient{parent},
		daggerSession:            &daggerSession{attachables: attachables},
	}

	caller, err := child.resolveHostServiceCaller(context.Background(), "child")
	require.NoError(t, err)
	require.Same(t, currentCaller, caller)
}

func TestResolveHostServiceCallerUsesBlockingLookupForOtherClients(t *testing.T) {
	t.Parallel()

	otherCaller := &fakeSessionCaller{id: "other"}
	child := &daggerClient{clientID: "child"}
	child.getClientCaller = func(ctx context.Context, id string) (engineutil.SessionCaller, error) {
		require.Equal(t, "other", id)
		return otherCaller, nil
	}

	caller, err := child.resolveHostServiceCaller(context.Background(), "other")
	require.NoError(t, err)
	require.Same(t, otherCaller, caller)
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

	t.Run("workspace with config", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:       "/repo",
			Cwd:        filepath.Join("services", "payment", "src"),
			ConfigFile: filepath.Join("services", "payment", workspace.LockDirName, workspace.ConfigFileName),
			LockFile:   filepath.Join("services", "payment", workspace.LockDirName, workspace.LockFileName),
		}, true, dagql.ObjectResult[*core.Directory]{}, "")
		require.NoError(t, err)
		require.Equal(t, "file:///repo/services/payment/src", ws.Address)
		require.Equal(t, filepath.Join("services", "payment", "src"), ws.Cwd)
		require.Equal(t, filepath.Join("services", "payment", workspace.LockDirName, workspace.ConfigFileName), ws.ConfigFile)
		require.Equal(t, filepath.Join("services", "payment", workspace.LockDirName, workspace.LockFileName), ws.LockFile)
		require.Equal(t, "/repo", ws.HostPath())
	})

	t.Run("workspace without config", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:     "/repo",
			Cwd:      ".",
			LockFile: filepath.Join(workspace.LockDirName, workspace.LockFileName),
		}, true, dagql.ObjectResult[*core.Directory]{}, "")
		require.NoError(t, err)
		require.Empty(t, ws.ConfigFile)
		require.Equal(t, filepath.Join(workspace.LockDirName, workspace.LockFileName), ws.LockFile)
	})
}

func TestNestedClientMetadataForRequest(t *testing.T) {
	t.Parallel()

	baseMetadata := func() *engine.ClientMetadata {
		return &engine.ClientMetadata{
			ClientID:          "nested-client",
			ClientSecretToken: "secret",
			SessionID:         "session",
			ClientHostname:    "nested-host",
			ClientStableID:    "stable",
			ClientVersion:     "",
			Labels: map[string]string{
				"ignored": "true",
			},
			SSHAuthSocketPath: "/tmp/ssh.sock",
			AllowedLLMModules: []string{"parent"},
			ExtraModules: []engine.ExtraModule{{
				Ref: "github.com/dagger/base-extra",
			}},
			LoadWorkspaceModules:  true,
			EagerRuntime:          true,
			LockMode:              string(workspace.LockModeFrozen),
			Workspace:             stringPtr("github.com/dagger/base@main"),
			UseRecipeIDsByDefault: true,
		}
	}

	t.Run("inherits live nested client identity and policy without forwarded metadata", func(t *testing.T) {
		t.Parallel()

		base := baseMetadata()
		md := nestedClientMetadataForRequest(http.Header{}, base)

		require.Equal(t, "nested-client", md.ClientID)
		require.Equal(t, "secret", md.ClientSecretToken)
		require.Equal(t, "session", md.SessionID)
		require.Equal(t, "nested-host", md.ClientHostname)
		require.Equal(t, "stable", md.ClientStableID)
		require.Equal(t, engine.Version, md.ClientVersion)
		require.Empty(t, md.Labels)
		require.Equal(t, "/tmp/ssh.sock", md.SSHAuthSocketPath)
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
		require.Equal(t, string(workspace.LockModeFrozen), md.LockMode)
		require.Empty(t, md.ExtraModules)
		require.False(t, md.LoadWorkspaceModules)
		require.False(t, md.EagerRuntime)
		require.Nil(t, md.Workspace)
		require.Nil(t, md.WorkspaceEnv)
		require.True(t, md.UseRecipeIDsByDefault)

		base.AllowedLLMModules[0] = "mutated"
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
	})

	t.Run("overlays request-scoped forwarded metadata", func(t *testing.T) {
		t.Parallel()

		workspaceRef := "github.com/dagger/dagger@main"
		workspaceEnv := "ci"
		forwarded := engine.ClientMetadata{
			ClientID:          "forwarded-client",
			ClientSecretToken: "forwarded-secret",
			SessionID:         "forwarded-session",
			ClientHostname:    "forwarded-host",
			ClientStableID:    "forwarded-stable",
			ClientVersion:     "v-test",
			Labels: map[string]string{
				"forwarded": "ignored",
			},
			SSHAuthSocketPath: "/tmp/forwarded-ssh.sock",
			AllowedLLMModules: []string{"child"},
			ExtraModules: []engine.ExtraModule{{
				Ref:        "github.com/dagger/mod",
				Entrypoint: true,
			}},
			LoadWorkspaceModules:           true,
			EagerRuntime:                   true,
			SuppressCompatWorkspaceWarning: true,
			LockMode:                       string(workspace.LockModeLive),
			Workspace:                      &workspaceRef,
			WorkspaceEnv:                   &workspaceEnv,
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), baseMetadata())

		require.Equal(t, "nested-client", md.ClientID)
		require.Equal(t, "secret", md.ClientSecretToken)
		require.Equal(t, "session", md.SessionID)
		require.Equal(t, "nested-host", md.ClientHostname)
		require.Equal(t, "stable", md.ClientStableID)
		require.Equal(t, "/tmp/ssh.sock", md.SSHAuthSocketPath)
		require.Empty(t, md.Labels)

		require.Equal(t, "v-test", md.ClientVersion)
		require.Equal(t, []string{"child"}, md.AllowedLLMModules)
		require.Equal(t, string(workspace.LockModeLive), md.LockMode)
		require.True(t, md.LoadWorkspaceModules)
		require.True(t, md.EagerRuntime)
		require.True(t, md.SuppressCompatWorkspaceWarning)
		require.Equal(t, "github.com/dagger/dagger@main", *md.Workspace)
		require.Equal(t, "ci", *md.WorkspaceEnv)
		require.Equal(t, []engine.ExtraModule{{
			Ref:        "github.com/dagger/mod",
			Entrypoint: true,
		}}, md.ExtraModules)
		require.True(t, md.UseRecipeIDsByDefault)
	})

	t.Run("keeps parent lock mode when forwarded metadata omits it", func(t *testing.T) {
		t.Parallel()

		forwarded := engine.ClientMetadata{
			ClientVersion:     "v-test",
			AllowedLLMModules: []string{"child"},
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), baseMetadata())

		require.Equal(t, "v-test", md.ClientVersion)
		require.Equal(t, []string{"child"}, md.AllowedLLMModules)
		require.Equal(t, string(workspace.LockModeFrozen), md.LockMode)
		require.Nil(t, md.WorkspaceEnv)
		require.True(t, md.UseRecipeIDsByDefault)
	})

	t.Run("does not accept internal recipe ID default from forwarded metadata", func(t *testing.T) {
		t.Parallel()

		base := baseMetadata()
		base.UseRecipeIDsByDefault = false
		forwarded := engine.ClientMetadata{
			ClientVersion:         "v-test",
			UseRecipeIDsByDefault: true,
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), base)

		require.False(t, md.UseRecipeIDsByDefault)
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
			{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/a", Name: "a"},
			{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/b", Name: "b"},
		},
		[]engine.ExtraModule{
			{Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true},
			{Ref: "github.com/acme/extra2", Name: "extra2"},
		},
	)

	require.Len(t, loads, 4)
	require.Equal(t, moduleLoadKindAmbient, loads[0].mod.Kind)
	require.Equal(t, moduleLoadKindAmbient, loads[1].mod.Kind)
	require.Equal(t, moduleLoadKindExtra, loads[2].mod.Kind)
	require.Equal(t, moduleLoadKindExtra, loads[3].mod.Kind)

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
		mod: pendingModule{
			Kind: moduleLoadKindExtra,
			Ref:  "github.com/acme/extra",
		},
	}, err)
	require.ErrorContains(t, extra, `loading extra module "github.com/acme/extra": boom`)
}

func TestDedupeResolvedModuleLoads(t *testing.T) {
	t.Parallel()

	loads := []moduleLoadRequest{
		{
			mod: pendingModule{
				Kind:       moduleLoadKindAmbient,
				Ref:        "github.com/acme/app",
				Name:       "app",
				Entrypoint: false,
			},
		},
		{
			mod: pendingModule{
				Kind:       moduleLoadKindExtra,
				Ref:        "github.com/acme/app",
				Name:       "app",
				Entrypoint: true,
			},
		},
		{
			mod: pendingModule{
				Kind:       moduleLoadKindAmbient,
				Ref:        "github.com/acme/other",
				Name:       "other",
				Entrypoint: false,
			},
		},
	}
	resolved := []resolvedModuleLoad{
		{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: false},
		{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
		{primary: sessionTestModuleResult(t, "other"), primaryEntrypoint: false},
	}

	dedupLoads, dedupResolved := dedupeResolvedModuleLoads(loads, resolved)
	require.Len(t, dedupLoads, 2)

	require.Equal(t, moduleLoadKindExtra, dedupLoads[0].mod.Kind)
	require.True(t, dedupResolved[0].primaryEntrypoint)

	require.Equal(t, moduleLoadKindAmbient, dedupLoads[1].mod.Kind)
	require.False(t, dedupResolved[1].primaryEntrypoint)
}

func TestArbitrateResolvedModuleLoads(t *testing.T) {
	t.Parallel()

	t.Run("cwd beats ambient", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/app", Name: "app", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindCWD, Ref: "github.com/acme/local", Name: "local", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "local"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.NoError(t, err)
		require.False(t, resolved[0].primaryEntrypoint)
		require.True(t, resolved[1].primaryEntrypoint)
	})

	t.Run("extra beats ambient", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/app", Name: "app", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra", Name: "extra", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "extra"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.NoError(t, err)
		require.False(t, resolved[0].primaryEntrypoint)
		require.True(t, resolved[1].primaryEntrypoint)
	})

	t.Run("multiple ambient entrypoints are invalid", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/app", Name: "app", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/other", Name: "other", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "other"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.EqualError(t, err, "invalid workspace configuration: multiple distinct ambient entrypoint modules: app, other")
	})

	t.Run("multiple extra entrypoints are invalid", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra2", Name: "extra2", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "extra1"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "extra2"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.EqualError(t, err, "invalid extra-module request: multiple distinct extra-module entrypoints: extra1, extra2")
	})
}

func TestSuppressPendingCWDModules(t *testing.T) {
	t.Parallel()

	mods := []pendingModule{
		{
			Kind: moduleLoadKindAmbient,
			Ref:  "github.com/acme/app",
		},
		{
			Kind: moduleLoadKindCWD,
			Ref:  "github.com/acme/local",
		},
		{
			Kind: moduleLoadKindExtra,
			Ref:  "github.com/acme/extra",
		},
	}

	filtered := suppressPendingCWDModules(mods)
	require.Len(t, filtered, 2)
	require.Equal(t, moduleLoadKindAmbient, filtered[0].Kind)
	require.Equal(t, moduleLoadKindExtra, filtered[1].Kind)
}

func TestSuppressCWDModuleForCompatWorkspace(t *testing.T) {
	t.Parallel()

	t.Run("suppresses cwd module at compat root", func(t *testing.T) {
		t.Parallel()

		require.True(t, suppressCWDModuleForCompatWorkspace(&workspace.CompatWorkspace{
			ProjectRoot: "/repo",
		}, "/repo"))
	})

	t.Run("does not suppress nested cwd module", func(t *testing.T) {
		t.Parallel()

		require.False(t, suppressCWDModuleForCompatWorkspace(&workspace.CompatWorkspace{
			ProjectRoot: "/repo",
		}, "/repo/modules/foo"))
	})

	t.Run("does not suppress without compat workspace", func(t *testing.T) {
		t.Parallel()

		require.False(t, suppressCWDModuleForCompatWorkspace(nil, "/repo"))
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

func sessionTestModuleResult(t *testing.T, name string) dagql.ObjectResult[*core.Module] {
	t.Helper()

	dag, err := dagql.NewServer(t.Context(), &core.Module{})
	require.NoError(t, err)
	res, err := dagql.NewObjectResultForCall(
		&core.Module{NameField: name},
		dag,
		&dagql.ResultCall{SyntheticOp: "session-test-module-" + name},
	)
	require.NoError(t, err)
	return res
}
