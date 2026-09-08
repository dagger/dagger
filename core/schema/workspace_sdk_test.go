package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestResolveSDKModuleInit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ws       *core.Workspace
		pathArg  string
		nameArg  string
		wantPath string
		wantName string
		wantErr  string
	}{
		{
			name:     "explicit name wins",
			ws:       &core.Workspace{Cwd: "apps/web"},
			pathArg:  "modules/payments",
			nameArg:  "billing",
			wantPath: "apps/web/modules/payments",
			wantName: "billing",
		},
		{
			name:     "path supplies name",
			ws:       &core.Workspace{Cwd: "."},
			pathArg:  "foo/bar/baz",
			wantPath: "foo/bar/baz",
			wantName: "baz",
		},
		{
			name:     "absolute workspace path supplies name",
			ws:       &core.Workspace{Cwd: "apps/web"},
			pathArg:  "/modules/payments",
			wantPath: "modules/payments",
			wantName: "payments",
		},
		{
			name:     "active config directory supplies name",
			ws:       &core.Workspace{Cwd: "apps/payments/internal", ConfigFile: "apps/payments/dagger.toml"},
			wantPath: "apps/payments/.dagger/modules/payments-dev",
			wantName: "payments-dev",
		},
		{
			name:     "explicit name uses managed path beside active config",
			ws:       &core.Workspace{Cwd: "apps/payments/internal", ConfigFile: "apps/payments/dagger.toml"},
			nameArg:  "billing",
			wantPath: "apps/payments/.dagger/modules/billing",
			wantName: "billing",
		},
		{
			name:     "workspace root supplies local name",
			ws:       localWorkspaceForSDKInit("/dev/projects/foo", ".", "dagger.toml"),
			wantPath: ".dagger/modules/foo-dev",
			wantName: "foo-dev",
		},
		{
			name:     "managed path ignores cwd below workspace root",
			ws:       localWorkspaceForSDKInit("/dev/projects/foo", "apps/web", "dagger.toml"),
			wantPath: ".dagger/modules/foo-dev",
			wantName: "foo-dev",
		},
		{
			name:     "workspace root supplies path without active config",
			ws:       localWorkspaceForSDKInit("/dev/projects/foo", "apps/web", ""),
			wantPath: ".dagger/modules/foo-dev",
			wantName: "foo-dev",
		},
		{
			name:     "root path uses local workspace name",
			ws:       localWorkspaceForSDKInit("/dev/projects/foo", "apps/web", "dagger.toml"),
			pathArg:  "/",
			wantPath: ".",
			wantName: "foo-dev",
		},
		{
			name: "workspace root supplies remote name",
			ws: &core.Workspace{
				Address: "github.com/acme/foo/apps/web@main",
				Cwd:     "apps/web",
			},
			wantPath: ".dagger/modules/foo-dev",
			wantName: "foo-dev",
		},
		{
			name: "remote version can contain slashes",
			ws: &core.Workspace{
				Address: "https://github.com/acme/foo/apps/web@feature/client-v2",
				Cwd:     "apps/web",
			},
			wantPath: ".dagger/modules/foo-dev",
			wantName: "foo-dev",
		},
		{
			name:    "unidentifiable root requires name",
			ws:      &core.Workspace{Address: "directory://sha256:abc", Cwd: "."},
			wantErr: "pass --name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotPath, gotName, gotExplicit, err := resolveSDKModuleInit(test.ws, test.pathArg, test.nameArg)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantPath, gotPath)
			require.Equal(t, test.wantName, gotName)
			// Only an explicit --path locks the result against defaultModulePath.
			require.Equal(t, test.pathArg != "", gotExplicit)
		})
	}
}

func TestResolveSDKModuleName(t *testing.T) {
	for _, test := range []struct {
		name     string
		modules  map[string]workspace.ModuleEntry
		override string
		want     string
		wantErr  string
	}{
		{
			name: "SDK-selected path retains the entrypoint name",
			modules: map[string]workspace.ModuleEntry{
				"shop-dev": {Source: "../services/api", Entrypoint: true},
			},
			want: "shop-dev",
		},
		{
			name: "explicit name wins over entrypoint and path",
			modules: map[string]workspace.ModuleEntry{
				"shop-dev": {Source: "../services/api", Entrypoint: true},
			},
			override: "billing", want: "billing",
		},
		{
			name: "explicit path without installation supplies the name",
			want: "api",
		},
		{
			name: "aliases and unrelated entrypoints do not supply the name",
			modules: map[string]workspace.ModuleEntry{
				"alias":  {Source: "../services/api"},
				"other":  {Source: "other", Entrypoint: true},
				"remote": {Source: "github.com/acme/api", Entrypoint: true},
			},
			want: "api",
		},
		{
			name: "root-relative installation matches",
			modules: map[string]workspace.ModuleEntry{
				"shop-dev": {Source: "/apps/services/./api", Entrypoint: true},
			},
			want: "shop-dev",
		},
		{
			name: "ambiguous entrypoints require an override",
			modules: map[string]workspace.ModuleEntry{
				"second": {Source: "../services/api", Entrypoint: true},
				"first":  {Source: "../services/api", Entrypoint: true},
			},
			wantErr: `multiple entrypoint names ["first" "second"]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &workspace.Config{Modules: test.modules}
			for _, cwd := range []string{".", "apps/shop/internal", "apps/services/api/nested"} {
				ws := localWorkspaceForSDKInit("/work", cwd, "apps/shop/dagger.toml")
				name, err := resolveSDKModuleName(ws, cfg, "apps/shop", "apps/services/api", test.override)
				if test.wantErr != "" {
					require.ErrorContains(t, err, test.wantErr)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.want, name)
				}
			}
		})
	}
}

func TestPlanSDKModuleInitInstall(t *testing.T) {
	t.Run("default name and path install an entrypoint", func(t *testing.T) {
		cfg := &workspace.Config{}
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", ".dagger/modules/demo", false, false))
		require.Equal(t, workspace.ModuleEntry{Source: ".dagger/modules/demo", Entrypoint: true}, cfg.Modules["demo"])
	})

	t.Run("explicit name installs a namespaced module", func(t *testing.T) {
		cfg := &workspace.Config{}
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", ".dagger/modules/demo", false, true))
		require.Equal(t, workspace.ModuleEntry{Source: ".dagger/modules/demo"}, cfg.Modules["demo"])
	})

	t.Run("custom path", func(t *testing.T) {
		cfg := &workspace.Config{}
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", "apps/demo", true, false))
		require.Empty(t, cfg.Modules)
	})

	t.Run("repeated default init promotes and preserves its entrypoint", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"demo": {Source: ".dagger/modules/demo"},
		}}
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", ".dagger/modules/demo", false, false))
		require.True(t, cfg.Modules["demo"].Entrypoint)
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", ".dagger/modules/demo", false, false))
		require.True(t, cfg.Modules["demo"].Entrypoint)
	})

	t.Run("explicit init does not demote an entrypoint", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"demo": {Source: ".dagger/modules/demo", Entrypoint: true},
		}}
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", ".dagger/modules/demo", false, true))
		require.True(t, cfg.Modules["demo"].Entrypoint)
	})

	t.Run("different entrypoint requires an explicit name", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"existing": {Source: "existing", Entrypoint: true},
		}}
		err := planSDKModuleInitInstall(cfg, "demo", ".dagger/modules/demo", false, false)
		require.EqualError(t, err, `workspace already has entrypoint module "existing"; pass --name to initialize an additional namespaced module`)
		require.NotContains(t, cfg.Modules, "demo")
	})
}

func localWorkspaceForSDKInit(root, cwd, configFile string) *core.Workspace {
	ws := &core.Workspace{Cwd: cwd, ConfigFile: configFile}
	ws.SetHostPath(root)
	return ws
}

func TestInstalledSDKSource(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go-sdk": {
				Source: "github.com/dagger/go-sdk",
				Pin:    "sha256:abc",
			},
			"typescript-sdk": {
				Source: "github.com/dagger/typescript-sdk@v1.2.3",
				Pin:    "sha256:ignored",
			},
			"plain": {
				Source: "github.com/dagger/plain",
			},
		},
		SDKs: map[string]workspace.SDKEntry{
			"go":         {Module: "go-sdk"},
			"typescript": {Module: "typescript-sdk"},
		},
	}

	name, entry, source, err := installedSDKSource(cfg, "go")
	require.NoError(t, err)
	require.Equal(t, "go", name)
	require.Equal(t, "github.com/dagger/go-sdk", entry.Source)
	require.Equal(t, "github.com/dagger/go-sdk@sha256:abc", source)

	name, _, source, err = installedSDKSource(cfg, "typescript")
	require.NoError(t, err)
	require.Equal(t, "typescript", name)
	require.Equal(t, "github.com/dagger/typescript-sdk@v1.2.3", source)

	name, _, source, err = installedSDKSource(cfg, "go-sdk")
	require.NoError(t, err)
	require.Equal(t, "go", name)
	require.Equal(t, "github.com/dagger/go-sdk@sha256:abc", source)

	_, _, source, err = installedSDKSource(cfg, "plain")
	require.EqualError(t, err, "\"plain\" is not installed as an SDK in this workspace; install its module with `dagger module install <module-ref>`")
	require.Empty(t, source)

	_, _, source, err = installedSDKSource(cfg, "missing")
	require.EqualError(t, err, "\"missing\" is not installed as an SDK in this workspace; install its module with `dagger module install <module-ref>`")
	require.Empty(t, source)
}

func TestInstalledSDKSourceRejectsMultipleNamesForProvider(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"dagger-go-sdk": {
				Source: "github.com/dagger/go-sdk",
			},
		},
		SDKs: map[string]workspace.SDKEntry{
			"go":     {Module: "dagger-go-sdk"},
			"golang": {Module: "dagger-go-sdk"},
		},
	}

	_, _, source, err := installedSDKSource(cfg, "go")
	require.ErrorContains(t, err, `module "dagger-go-sdk" provides multiple SDKs`)
	require.Empty(t, source)
}

func TestSelectSDKModuleRequiresName(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go-sdk": {Source: "github.com/dagger/go-sdk"},
		},
		SDKs: map[string]workspace.SDKEntry{
			"go": {Module: "go-sdk"},
		},
	}
	_, err := selectSDKModule(cfg, "")
	require.EqualError(t, err, "SDK name is required")
}

func TestDeepestRecordedSDKModuleScope(t *testing.T) {
	entry := workspace.SDKEntry{Scopes: map[string]workspace.SDKScope{
		".":                    {},
		"services":             {},
		"services/api":         {},
		"services/api/sibling": {},
	}}

	scope, err := deepestRecordedSDKModuleScope(entry, "apps/demo", "apps/demo/services/api/internal")
	require.NoError(t, err)
	require.Equal(t, "apps/demo/services/api", scope)
}

func TestDeeperSDKModuleScope(t *testing.T) {
	for _, test := range []struct {
		name     string
		first    string
		second   string
		expected string
	}{
		{name: "none", expected: ""},
		{name: "first only", first: "apps/demo", expected: "apps/demo"},
		{name: "second only", second: "apps/demo", expected: "apps/demo"},
		{name: "second is deeper", first: "apps/demo", second: "apps/demo/internal", expected: "apps/demo/internal"},
		{name: "first is deeper", first: "apps/demo/internal", second: "apps/demo", expected: "apps/demo/internal"},
		{name: "equal", first: "apps/demo", second: "apps/demo", expected: "apps/demo"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, deeperSDKModuleScope(test.first, test.second))
		})
	}
}

func TestSelectDeepestSDKModuleScope(t *testing.T) {
	goRoot := resolvedSDKModuleScope{sdk: selectedSDKModule{name: "go"}, scope: ".", configScopePath: "."}
	pythonRoot := resolvedSDKModuleScope{sdk: selectedSDKModule{name: "python"}, scope: ".", configScopePath: "."}
	pythonApp := resolvedSDKModuleScope{sdk: selectedSDKModule{name: "python"}, scope: "app", configScopePath: "app"}
	pythonA := resolvedSDKModuleScope{sdk: selectedSDKModule{name: "python"}, scope: "a", configScopePath: "a"}
	goApp := resolvedSDKModuleScope{sdk: selectedSDKModule{name: "go"}, scope: "app", configScopePath: "app"}
	for _, test := range []struct {
		name   string
		scopes []resolvedSDKModuleScope
		want   resolvedSDKModuleScope
		err    string
	}{
		{name: "none"},
		{name: "one", scopes: []resolvedSDKModuleScope{goRoot}, want: goRoot},
		{name: "deeper SDK", scopes: []resolvedSDKModuleScope{goRoot, pythonApp}, want: pythonApp},
		{name: "deeper SDK first", scopes: []resolvedSDKModuleScope{pythonApp, goRoot}, want: pythonApp},
		{name: "one character directory", scopes: []resolvedSDKModuleScope{goRoot, pythonA}, want: pythonA},
		{name: "shallower tie", scopes: []resolvedSDKModuleScope{pythonRoot, goApp, goRoot}, want: goApp},
		{name: "same SDK nested records", scopes: []resolvedSDKModuleScope{goRoot, goApp}, want: goApp},
		{name: "deepest tie", scopes: []resolvedSDKModuleScope{pythonApp, goRoot, goApp},
			err: `multiple SDKs match the deepest client scope: SDK "go" at "app", SDK "python" at "app"; select one with --sdk`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectDeepestSDKModuleScope(test.scopes)
			if test.err != "" {
				require.EqualError(t, err, test.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestWorkspaceSDKEntryPaths(t *testing.T) {
	t.Parallel()

	moduleEntry := workspace.ModuleEntry{
		Source: "../sdk",
		Pin:    "sha256:abc",
	}
	sdkEntry := workspace.SDKEntry{Module: "custom-sdk", Scopes: map[string]workspace.SDKScope{
		".dagger/modules/demo": {IsModule: true, Name: "demo"},
	}}

	require.Equal(t, "apps/sdk@sha256:abc", resolvedModuleEntrySourceWithPin("apps/demo", moduleEntry))
	require.Equal(t, "../../../apps/sdk@sha256:abc", mustModuleEntrySourceWithPinRelativeTo(t, "apps/demo", ".dagger/modules/new", moduleEntry))

	// The SDK scope path is recorded against the config directory, like the
	// entry's own source, and surfaces workspace-root-relative.
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{"custom-sdk": moduleEntry},
		SDKs:    map[string]workspace.SDKEntry{"custom": sdkEntry},
	}
	sdk, err := workspaceSDKFromEntry(&core.Workspace{}, cfg, "apps/demo", "custom", moduleEntry)
	require.NoError(t, err)
	require.Equal(t, "custom", sdk.Name)
	require.Equal(t, "apps/sdk@sha256:abc", sdk.Ref)
	require.Len(t, sdk.Modules, 1)
	require.Equal(t, "demo", sdk.Modules[0].Name)
	require.Equal(t, "apps/demo/.dagger/modules/demo", sdk.Modules[0].Source)

	// SDK readers expose the inferred name without storing it in the scope.
	scope := sdkEntry.Scopes[".dagger/modules/demo"]
	scope.Name = ""
	sdkEntry.Scopes[".dagger/modules/demo"] = scope
	cfg.Modules["demo-dev"] = workspace.ModuleEntry{Source: ".dagger/modules/demo", Entrypoint: true}
	sdk, err = workspaceSDKFromEntry(&core.Workspace{}, cfg, "apps/demo", "custom", moduleEntry)
	require.NoError(t, err)
	require.Equal(t, "demo-dev", sdk.Modules[0].Name)
	require.Empty(t, cfg.SDKs["custom"].Scopes[".dagger/modules/demo"].Name)
}

func TestModuleEntrySourceWithPinRelativeToLeavesGitRefsCanonical(t *testing.T) {
	t.Parallel()

	entry := workspace.ModuleEntry{
		Source: "github.com/acme/sdk",
		Pin:    "v1.2.3",
	}
	require.Equal(t, "github.com/acme/sdk@v1.2.3", mustModuleEntrySourceWithPinRelativeTo(t, "apps/demo", ".dagger/modules/new", entry))
}

func TestValidateSDKModuleGenerationGraph(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{
		SDKs: map[string]workspace.SDKEntry{
			"go": {
				Module: "go-sdk",
				Scopes: map[string]workspace.SDKScope{
					".": {
						IsModule: true,
						Name:     "root",
						Clients:  []string{"./target", "github.com/acme/remote"},
					},
					"target": {IsModule: true},
				},
			},
		},
	}
	require.NoError(t, validateSDKModuleGenerationGraph(cfg, "apps/demo"))

	target := cfg.SDKs["go"].Scopes["target"]
	target.Clients = []string{"."}
	cfg.SDKs["go"].Scopes["target"] = target
	require.EqualError(
		t,
		validateSDKModuleGenerationGraph(cfg, "apps/demo"),
		"local SDK generation cycle: apps/demo -> apps/demo/target -> apps/demo",
	)
}

func TestPlanSDKModuleScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		scopes map[string]workspace.SDKScope
		want   []string
	}{
		{
			name: "root dependency runs once and first",
			scopes: map[string]workspace.SDKScope{
				".":      {IsModule: true, Name: "root"},
				"nested": {IsModule: true, Name: "nested", Clients: []string{"."}},
			},
			want: []string{".", "nested"},
		},
		{
			name: "diamond visits the shared dependency once",
			scopes: map[string]workspace.SDKScope{
				".":      {IsModule: true, Name: "root", Clients: []string{"./left", "./right"}},
				"left":   {IsModule: true, Name: "left", Clients: []string{"./shared"}},
				"right":  {IsModule: true, Name: "right", Clients: []string{"./shared"}},
				"shared": {IsModule: true, Name: "shared"},
			},
			want: []string{"shared", "left", "right", "."},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := &workspace.Config{SDKs: map[string]workspace.SDKEntry{
				"go": {Module: "go-sdk", Scopes: test.scopes},
			}}
			plan, err := planSDKModuleScopes(".", cfg, ".", map[string]bool{"go": true})
			require.NoError(t, err)

			paths := make([]string, len(plan.ordered))
			for i, scope := range plan.ordered {
				paths[i] = scope.path
			}
			require.Equal(t, test.want, paths)
		})
	}
}

func mustModuleEntrySourceWithPinRelativeTo(t *testing.T, configDir, targetDir string, entry workspace.ModuleEntry) string {
	t.Helper()
	ref, err := moduleEntrySourceWithPinRelativeTo(configDir, targetDir, entry)
	require.NoError(t, err)
	return ref
}

// Local client references must keep an explicit path marker after both the
// command directory and configuration directory have been resolved.
func TestResolveWorkspaceClientModuleInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ref, configDir, cwd, wantLoad, wantSaved string
	}{
		{"./sdk.v1/api", ".", ".", "./sdk.v1/api", "./sdk.v1/api"},
		{"./modules/api", ".", ".", "./modules/api", "./modules/api"},
		{"./.dagger/modules/api", ".", ".", "./.dagger/modules/api", "./.dagger/modules/api"},
		{"../../modules/api", "apps", "apps/client", "./modules/api", "../modules/api"},
		{"../api", "apps", "apps/client", "./apps/api", "./api"},
		{"/modules/api", "apps", "apps/client", "./modules/api", "../modules/api"},
		{".", "apps", "apps", "./apps", "."},
		{"..", "apps", "apps", ".", ".."},
		{"github.com/acme/sdk@v1.2.3", "apps", "apps/client", "github.com/acme/sdk@v1.2.3", "github.com/acme/sdk@v1.2.3"},
	} {
		t.Run(tc.ref+" from "+tc.cwd, func(t *testing.T) {
			loadRef, saved, err := resolveWorkspaceClientModuleInput(tc.configDir, tc.cwd, tc.ref)
			require.NoError(t, err)
			require.Equal(t, tc.wantLoad, loadRef)
			require.Equal(t, tc.wantSaved, saved)
			reloaded, err := resolveSDKManagedClientModule(tc.configDir, saved)
			require.NoError(t, err)
			require.Equal(t, loadRef, reloaded)
		})
	}
}

// A hand-written root-anchored SDK scope, matched by consumers that see the
// module through its config-relative install source.
func TestRootAnchoredSDKScopeIsMatched(t *testing.T) {
	cfg := func() *workspace.Config {
		return &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"mymod":  {Source: ".dagger/modules/mymod"},
				"go-sdk": {Source: "github.com/dagger/go-sdk"},
			},
			SDKs: map[string]workspace.SDKEntry{
				"go": {Module: "go-sdk", Scopes: map[string]workspace.SDKScope{
					"/common/.dagger/modules/mymod": {
						IsModule: true,
						Name:     "mymod",
						Clients:  []string{"/common/sdk/shared"},
					},
					"/common/clients/one": {Clients: []string{"/common/sdk/api"}},
				}},
			},
		}
	}

	t.Run("uninstall removes the module scope", func(t *testing.T) {
		c := cfg()
		path, del, err := removeSDKManagedModuleReference(c, "common", "mymod", c.Modules["mymod"])
		require.NoError(t, err)
		require.True(t, del)
		require.Equal(t, "common/.dagger/modules/mymod", path)
		moduleScope, ok := c.SDKs["go"].Scopes["/common/.dagger/modules/mymod"]
		require.True(t, ok)
		require.False(t, moduleScope.IsModule)
		require.Empty(t, moduleScope.Name)
		require.Equal(t, []string{"/common/sdk/shared"}, moduleScope.Clients)
		require.Contains(t, c.SDKs["go"].Scopes, "/common/clients/one")
	})

	t.Run("sdk listing resolves it", func(t *testing.T) {
		config := cfg()
		sdk, err := workspaceSDKFromEntry(&core.Workspace{}, config, "common", "go", config.Modules["go-sdk"])
		require.NoError(t, err)
		require.Equal(t, "common/.dagger/modules/mymod", sdk.Modules[0].Source)
		require.ElementsMatch(t, []*core.WorkspaceModule{
			{Name: "common/.dagger/modules/mymod", Source: "./common/sdk/shared"},
			{Name: "common/clients/one", Source: "./common/sdk/api"},
		}, sdk.Clients)
	})
}

func TestValidateSDKModuleDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawPath  string
		wantPath string
		wantErr  string
	}{
		{name: "empty declines and keeps the engine default", rawPath: "", wantPath: ""},
		{name: "relative path is kept", rawPath: "modules/api", wantPath: "modules/api"},
		{name: "leading dot slash is cleaned", rawPath: "./modules/api", wantPath: "modules/api"},
		{name: "workspace root is allowed", rawPath: ".", wantPath: "."},
		{name: "windows separators are read as paths", rawPath: `modules\api`, wantPath: "modules/api"},
		{
			name:     "path need not contain the invocation cwd",
			rawPath:  "elsewhere/api",
			wantPath: "elsewhere/api",
		},
		{
			name:    "absolute path is rejected",
			rawPath: "/modules/api",
			wantErr: "must be workspace-root-relative",
		},
		{
			name:    "escaping the workspace is rejected",
			rawPath: "../outside",
			wantErr: "escape",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotPath, err := validateSDKModuleDestination(test.rawPath)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantPath, gotPath)
		})
	}
}
