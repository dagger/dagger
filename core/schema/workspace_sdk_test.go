package schema

import (
	"testing"

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

	sdk := workspaceSDKFromEntry("apps/demo", "custom-sdk", entry)
	require.Equal(t, "custom", sdk.Name)
	require.Equal(t, "apps/sdk@sha256:abc", sdk.Ref)
	require.Len(t, sdk.Modules, 1)
	require.Equal(t, "demo", sdk.Modules[0].Name)
	require.Equal(t, ".dagger/modules/demo", sdk.Modules[0].Source)
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

	removeClientEntryAtPath(cfg, "./lib/client")

	entry := cfg.Modules["go"]
	require.NotNil(t, entry.AsSDK)
	require.Empty(t, entry.AsSDK.Clients)
}
