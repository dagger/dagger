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
			"go": {
				Source: "github.com/dagger/go-sdk",
				Pin:    "sha256:abc",
				AsSDK:  &workspace.ModuleAsSDK{},
			},
			"typescript": {
				Source: "github.com/dagger/typescript-sdk@v1.2.3",
				Pin:    "sha256:ignored",
				AsSDK:  &workspace.ModuleAsSDK{},
			},
			"plain": {
				Source: "github.com/dagger/plain",
			},
		},
	}

	entry, source, err := installedSDKSource(cfg, "go")
	require.NoError(t, err)
	require.Equal(t, "github.com/dagger/go-sdk", entry.Source)
	require.Equal(t, "github.com/dagger/go-sdk@sha256:abc", source)

	_, source, err = installedSDKSource(cfg, "typescript")
	require.NoError(t, err)
	require.Equal(t, "github.com/dagger/typescript-sdk@v1.2.3", source)

	_, _, err = installedSDKSource(cfg, "plain")
	require.EqualError(t, err, "\"plain\" is not installed as an SDK in this workspace; run `dagger sdk install plain` first")

	_, _, err = installedSDKSource(cfg, "missing")
	require.EqualError(t, err, "\"missing\" is not installed as an SDK in this workspace; run `dagger sdk install missing` first")
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
