package daggercmd

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestAPIClientInitCommandShape(t *testing.T) {
	t.Parallel()

	cmd, _, err := apiClientCmd.Find([]string{"init"})
	require.NoError(t, err)
	require.Same(t, apiClientInitCmd, cmd)
	require.Equal(t, "init <sdk> <path> <module>", cmd.Use)
	require.Nil(t, cmd.Flags().Lookup("sdk"))
	require.Nil(t, cmd.Flags().Lookup("module"))
	require.Nil(t, cmd.Flags().Lookup("option"))
	require.Contains(t, cmd.Long, "to add more choices")
}

func TestAPIClientEntries(t *testing.T) {
	t.Parallel()

	entries := apiClientEntries(&workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go-sdk": {
				Source: "github.com/dagger/go-sdk",
				AsSDK: &workspace.ModuleAsSDK{
					Clients: []workspace.SDKManagedClient{
						{
							Path:   "lib/go",
							Module: ".dagger/modules/api",
							Pin:    "abc123",
							Options: map[string]string{
								"go-module": "example.com/client",
							},
						},
					},
				},
			},
			"typescript-sdk": {
				Source: "github.com/dagger/typescript-sdk",
				AsSDK: &workspace.ModuleAsSDK{
					Clients: []workspace.SDKManagedClient{
						{Path: "lib/ts", Module: "github.com/dagger/postgres@v1.2.3"},
					},
				},
			},
		},
	})

	require.Equal(t, []apiClientListEntry{
		{
			SDK:    "go-sdk",
			Path:   "lib/go",
			Module: ".dagger/modules/api",
			Pin:    "abc123",
			Options: map[string]string{
				"go-module": "example.com/client",
			},
		},
		{
			SDK:    "typescript-sdk",
			Path:   "lib/ts",
			Module: "github.com/dagger/postgres@v1.2.3",
		},
	}, entries)
}
