package main

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestParseAPIClientOptions(t *testing.T) {
	t.Parallel()

	got, err := parseAPIClientOptions([]string{
		"package-name=@my-app/dagger-client",
		"go-module=example.com/client",
	})
	require.NoError(t, err)
	require.Equal(t, []sdkOptionInput{
		{Key: "package-name", Value: "@my-app/dagger-client"},
		{Key: "go-module", Value: "example.com/client"},
	}, got)

	_, err = parseAPIClientOptions([]string{"package-name"})
	require.EqualError(t, err, `--option "package-name" must be in KEY=VAL form`)

	_, err = parseAPIClientOptions([]string{"path=lib/client"})
	require.EqualError(t, err, `--option "path" is reserved`)
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
