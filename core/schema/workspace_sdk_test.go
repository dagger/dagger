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
