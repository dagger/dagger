package schema

import (
	"path/filepath"
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

func TestPlanSDKModuleInitInstall(t *testing.T) {
	t.Run("default path", func(t *testing.T) {
		cfg := &workspace.Config{}
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", ".dagger/modules/demo", false))
		require.Equal(t, workspace.ModuleEntry{Source: ".dagger/modules/demo"}, cfg.Modules["demo"])
	})

	t.Run("custom path", func(t *testing.T) {
		cfg := &workspace.Config{}
		require.NoError(t, planSDKModuleInitInstall(cfg, "demo", "apps/demo", true))
		require.Empty(t, cfg.Modules)
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
	sdk, err := workspaceSDKFromEntry(nil, "apps/demo", "custom", sdkEntry, moduleEntry)
	require.NoError(t, err)
	require.Equal(t, "custom", sdk.Name)
	require.Equal(t, "apps/sdk@sha256:abc", sdk.Ref)
	require.Len(t, sdk.Modules, 1)
	require.Equal(t, "demo", sdk.Modules[0].Name)
	require.Equal(t, "apps/demo/.dagger/modules/demo", sdk.Modules[0].Source)
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
						Clients:  []string{"target", "github.com/acme/remote"},
					},
					"target": {IsModule: true, Name: "target"},
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
				".":      {IsModule: true, Name: "root", Clients: []string{"left", "right"}},
				"left":   {IsModule: true, Name: "left", Clients: []string{"shared"}},
				"right":  {IsModule: true, Name: "right", Clients: []string{"shared"}},
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

// A client module ref is stored relative to the dagger.toml holding it, and has
// to read back as a local path. Only a spelling the classifier would call a git
// ref — a dot in its first segment, "sdk.v1/api" — needs the explicit marker.
func TestResolveWorkspaceClientModuleRefStoresClassifiableLocalRef(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		ref           string
		configDir     string
		wantLoadRef   string
		wantConfigRef string
	}{
		{name: "dotted path stays local", ref: "./sdk.v1/api", configDir: ".", wantLoadRef: "sdk.v1/api", wantConfigRef: "./sdk.v1/api"},
		{name: "dotted path from nested config", ref: "./sdk.v1/api", configDir: "apps", wantLoadRef: "sdk.v1/api", wantConfigRef: "../sdk.v1/api"},
		{name: "plain path keeps the bare spelling", ref: "./modules/api", configDir: ".", wantLoadRef: "modules/api", wantConfigRef: "modules/api"},
		{name: "dot in a later segment needs no marker", ref: ".dagger/modules/api", configDir: ".", wantLoadRef: ".dagger/modules/api", wantConfigRef: ".dagger/modules/api"},
		{name: "nested config", ref: "modules/api", configDir: "apps/demo", wantLoadRef: "modules/api", wantConfigRef: "../../modules/api"},
		{name: "remote ref verbatim", ref: "github.com/acme/sdk", configDir: "apps/demo", wantLoadRef: "github.com/acme/sdk", wantConfigRef: "github.com/acme/sdk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			loadRef, configRef, err := resolveWorkspaceClientModuleRef(&core.Workspace{}, tc.ref, tc.configDir)
			require.NoError(t, err)
			require.Equal(t, tc.wantLoadRef, loadRef)
			require.Equal(t, tc.wantConfigRef, configRef)
			require.True(t, workspace.IsLocalRef(configRef, "") == workspace.IsLocalRef(tc.ref, ""),
				"stored ref must classify the same way as the ref it came from")
		})
	}
}

func TestResolveWorkspaceClientModuleInputPreservesInstalledName(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
		"api":        {Source: "modules/api"},
		"remote-api": {Source: "github.com/acme/api", Pin: "v1.2.3"},
	}}
	loadRef, configRef, err := resolveWorkspaceClientModuleInput(
		&core.Workspace{},
		cfg,
		"apps",
		"apps/client",
		"api",
	)
	require.NoError(t, err)
	require.Equal(t, "apps/modules/api", filepath.ToSlash(loadRef))
	require.Equal(t, "api", configRef)

	loadRef, err = resolveSDKManagedClientModule(&core.Workspace{}, cfg, "apps", configRef)
	require.NoError(t, err)
	require.Equal(t, "apps/modules/api", filepath.ToSlash(loadRef))

	loadRef, configRef, err = resolveWorkspaceClientModuleInput(
		&core.Workspace{},
		cfg,
		"apps",
		"apps/client",
		"remote-api",
	)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/api@v1.2.3", loadRef)
	require.Equal(t, "remote-api", configRef)

	cfg.Modules["target"] = workspace.ModuleEntry{Source: "installed/target"}
	loadRef, configRef, err = resolveWorkspaceClientModuleInput(
		&core.Workspace{},
		cfg,
		".",
		".",
		"./target",
	)
	require.NoError(t, err)
	require.Equal(t, "target", loadRef)
	require.Equal(t, "./target", configRef)
	loadRef, err = resolveSDKManagedClientModule(&core.Workspace{}, cfg, ".", configRef)
	require.NoError(t, err)
	require.Equal(t, "target", loadRef)
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
		sdk, err := workspaceSDKFromEntry(config, "common", "go", config.SDKs["go"], config.Modules["go-sdk"])
		require.NoError(t, err)
		require.Equal(t, "common/.dagger/modules/mymod", sdk.Modules[0].Source)
		require.ElementsMatch(t, []*core.WorkspaceModule{
			{Name: "common/.dagger/modules/mymod", Source: "common/sdk/shared"},
			{Name: "common/clients/one", Source: "common/sdk/api"},
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
