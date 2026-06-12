package main

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRegisterSDKInitCommandsFromConfig(t *testing.T) {
	moduleParent := &cobra.Command{
		Use:  "init <sdk> <name>",
		Args: cobra.NoArgs,
	}
	clientParent := &cobra.Command{
		Use:  "init <sdk> <path> <module>",
		Args: cobra.NoArgs,
	}
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go": {
				Source: "github.com/dagger/go-sdk",
				AsSDK:  &workspace.ModuleAsSDK{},
			},
			"plain": {
				Source: "github.com/dagger/plain",
			},
		},
	}

	registerSDKInitCommandsFromConfig(moduleParent, clientParent, cfg)

	moduleChild, _, err := moduleParent.Find([]string{"go", "myapp"})
	require.NoError(t, err)
	require.Equal(t, "go", moduleChild.Name())
	require.Equal(t, "go <name>", moduleChild.Use)

	clientChild, _, err := clientParent.Find([]string{"go", "./client", ".dagger/modules/api"})
	require.NoError(t, err)
	require.Equal(t, "go", clientChild.Name())
	require.Equal(t, "go <path> <module>", clientChild.Use)

	cmd, args, err := moduleParent.Find([]string{"plain", "myapp"})
	require.NoError(t, err)
	require.Same(t, moduleParent, cmd)
	require.Equal(t, []string{"plain", "myapp"}, args)
	require.ErrorContains(t, moduleParent.Args(moduleParent, args), `unknown command "plain"`)

	registerSDKInitCommandsFromConfig(moduleParent, clientParent, nil)
	cmd, args, err = moduleParent.Find([]string{"go", "myapp"})
	require.NoError(t, err)
	require.Same(t, moduleParent, cmd)
	require.Equal(t, []string{"go", "myapp"}, args)
}

func TestShouldRegisterSDKInitCommands(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "module init",
			args: []string{"module", "init", "go", "myapp"},
			want: true,
		},
		{
			name: "api client init",
			args: []string{"api", "client", "init", "typescript", "./client", "."},
			want: true,
		},
		{
			name: "global workspace flag",
			args: []string{"--workspace", "./ws", "module", "init", "go", "myapp"},
			want: true,
		},
		{
			name: "global workspace short flag",
			args: []string{"-W", "./ws", "api", "client", "init", "go", "./client", "."},
			want: true,
		},
		{
			name: "unrelated command",
			args: []string{"sdk", "list"},
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldRegisterSDKInitCommands(tt.args))
		})
	}
}
