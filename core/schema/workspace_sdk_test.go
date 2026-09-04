package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestInstalledSDKSource(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go-sdk": {
				Source: "github.com/dagger/go-sdk",
				Pin:    "sha256:abc",
				AsSDK:  &workspace.ModuleAsSDK{Name: "go"},
			},
			"typescript-sdk": {
				Source: "github.com/dagger/typescript-sdk@v1.2.3",
				Pin:    "sha256:ignored",
				AsSDK:  &workspace.ModuleAsSDK{Name: "typescript"},
			},
			"plain": {
				Source: "github.com/dagger/plain",
			},
		},
	}

	name, entry, source, err := installedSDKSource(cfg, "go")
	require.NoError(t, err)
	require.Equal(t, "go-sdk", name)
	require.Equal(t, "github.com/dagger/go-sdk", entry.Source)
	require.Equal(t, "github.com/dagger/go-sdk@sha256:abc", source)

	name, _, source, err = installedSDKSource(cfg, "typescript")
	require.NoError(t, err)
	require.Equal(t, "typescript-sdk", name)
	require.Equal(t, "github.com/dagger/typescript-sdk@v1.2.3", source)

	name, _, source, err = installedSDKSource(cfg, "go-sdk")
	require.NoError(t, err)
	require.Equal(t, "go-sdk", name)
	require.Equal(t, "github.com/dagger/go-sdk@sha256:abc", source)

	_, _, source, err = installedSDKSource(cfg, "plain")
	require.EqualError(t, err, "\"plain\" is not installed as an SDK in this workspace; run `dagger sdk install plain` first")
	require.Empty(t, source)

	_, _, source, err = installedSDKSource(cfg, "missing")
	require.EqualError(t, err, "\"missing\" is not installed as an SDK in this workspace; run `dagger sdk install missing` first")
	require.Empty(t, source)
}

func TestInstalledSDKSourceAmbiguousAlias(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go-sdk": {
				Source: "github.com/dagger/go-sdk",
				AsSDK:  &workspace.ModuleAsSDK{Name: "go"},
			},
			"custom-go-sdk": {
				Source: "github.com/acme/go-sdk",
				AsSDK:  &workspace.ModuleAsSDK{Name: "go"},
			},
		},
	}

	_, _, source, err := installedSDKSource(cfg, "go")
	require.ErrorContains(t, err, `SDK name "go" is ambiguous`)
	require.ErrorContains(t, err, "modules.custom-go-sdk.as-sdk")
	require.ErrorContains(t, err, "modules.go-sdk.as-sdk")
	require.Empty(t, source)
}

func TestWorkspaceSDKEntryPaths(t *testing.T) {
	t.Parallel()

	entry := workspace.ModuleEntry{
		Source: "../sdk",
		Pin:    "sha256:abc",
		AsSDK: &workspace.ModuleAsSDK{
			Name: "custom",
			Modules: []workspace.SDKManagedModule{
				{Path: ".dagger/modules/demo"},
			},
		},
	}

	require.Equal(t, "apps/sdk@sha256:abc", resolvedModuleEntrySourceWithPin("apps/demo", entry))
	require.Equal(t, "../../../apps/sdk@sha256:abc", mustModuleEntrySourceWithPinRelativeTo(t, "apps/demo", ".dagger/modules/new", entry))

	// The as-sdk path is recorded against the config directory, like the
	// entry's own source, and surfaces workspace-root-relative.
	sdk, err := workspaceSDKFromEntry("apps/demo", "custom-sdk", entry)
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

func mustModuleEntrySourceWithPinRelativeTo(t *testing.T, configDir, targetDir string, entry workspace.ModuleEntry) string {
	t.Helper()
	ref, err := moduleEntrySourceWithPinRelativeTo(configDir, targetDir, entry)
	require.NoError(t, err)
	return ref
}

func TestRemoveClientEntryAtPathPreservesSDKMarker(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go": {
				Source: "github.com/dagger/go-sdk",
				AsSDK: &workspace.ModuleAsSDK{
					Clients: []workspace.SDKManagedClient{
						{Path: "lib/client", Module: ".dagger/modules/api"},
					},
				},
			},
		},
	}

	owners, err := removeClientEntryAtPath(cfg, ".", "./lib/client")
	require.NoError(t, err)
	require.Equal(t, []string{"go"}, owners)

	entry := cfg.Modules["go"]
	require.NotNil(t, entry.AsSDK)
	require.Empty(t, entry.AsSDK.Clients)
}

func TestRemoveClientEntryAtPathRejectsEscapingEntry(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go": {
				Source: "github.com/dagger/go-sdk",
				AsSDK: &workspace.ModuleAsSDK{
					Clients: []workspace.SDKManagedClient{
						{Path: "../outside", Module: ".dagger/modules/api"},
					},
				},
			},
		},
	}

	_, err := removeClientEntryAtPath(cfg, ".", "./lib/client")
	require.ErrorContains(t, err, "escapes the workspace root")
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

// A hand-written root-anchored as-sdk entry, matched by consumers that see the
// module through its config-relative install source.
func TestRootAnchoredAsSDKEntryIsMatched(t *testing.T) {
	cfg := func() *workspace.Config {
		return &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"mymod": {Source: ".dagger/modules/mymod"},
				"go-sdk": {
					Source: "github.com/dagger/go-sdk",
					AsSDK: &workspace.ModuleAsSDK{
						Modules: []workspace.SDKManagedModule{{Path: "/common/.dagger/modules/mymod"}},
						Clients: []workspace.SDKManagedClient{{Path: "/common/clients/one", Module: "/common/sdk/api"}},
					},
				},
			},
		}
	}

	t.Run("uninstall removes the as-sdk entry", func(t *testing.T) {
		c := cfg()
		path, del, err := removeSDKManagedModuleReference(c, "common", c.Modules["mymod"])
		require.NoError(t, err)
		require.True(t, del)
		require.Equal(t, "common/.dagger/modules/mymod", path)
		require.Empty(t, c.Modules["go-sdk"].AsSDK.Modules)
	})

	t.Run("client init replaces the entry at the same path", func(t *testing.T) {
		c := cfg()
		owners, err := removeClientEntryAtPath(c, "common", "common/clients/one")
		require.NoError(t, err)
		require.Equal(t, []string{"go-sdk"}, owners)
		require.Empty(t, c.Modules["go-sdk"].AsSDK.Clients)
	})

	t.Run("ownership map and sdk listing resolve it", func(t *testing.T) {
		owners, err := sdkOwnersByModulePathFromConfig("common", cfg())
		require.NoError(t, err)
		require.Equal(t, "go-sdk", owners["common/.dagger/modules/mymod"])

		sdk, err := workspaceSDKFromEntry("common", "go-sdk", cfg().Modules["go-sdk"])
		require.NoError(t, err)
		require.Equal(t, "common/.dagger/modules/mymod", sdk.Modules[0].Source)
		require.Equal(t, "common/clients/one", sdk.Clients[0].Name)
		require.Equal(t, "common/sdk/api", sdk.Clients[0].Source)
	})
}
