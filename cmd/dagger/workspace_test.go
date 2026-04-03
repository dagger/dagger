package main

import (
	"bytes"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestInitCommandRouting(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"init"})
	require.NoError(t, err)
	require.Same(t, initCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"module", "init"})
	require.NoError(t, err)
	require.Same(t, moduleInitCmd, cmd)
}

func TestWriteWorkspaceInfo(t *testing.T) {
	t.Run("prints config path when present", func(t *testing.T) {
		var out bytes.Buffer
		err := writeWorkspaceInfo(&out, workspaceInfoView{
			Address:    "github.com/acme/ws/toolchains/changelog@main",
			Path:       "toolchains/changelog",
			ConfigPath: ".dagger/config.toml",
		})
		require.NoError(t, err)
		require.Equal(t,
			"Address: github.com/acme/ws/toolchains/changelog@main\n"+
				"Path:    toolchains/changelog\n"+
				"Config:  .dagger/config.toml\n",
			out.String(),
		)
	})

	t.Run("prints none when config path is empty", func(t *testing.T) {
		var out bytes.Buffer
		err := writeWorkspaceInfo(&out, workspaceInfoView{
			Address: "github.com/acme/ws",
			Path:    ".",
		})
		require.NoError(t, err)
		require.Equal(t,
			"Address: github.com/acme/ws\n"+
				"Path:    .\n"+
				"Config:  none\n",
			out.String(),
		)
	})
}

func TestWriteWorkspaceModuleList(t *testing.T) {
	var out bytes.Buffer

	err := writeWorkspaceModuleList(&out, []dagger.WorkspaceModule{
		{
			Name:      "greeter",
			Blueprint: true,
			Source:    ".dagger/modules/greeter",
		},
		{
			Name:   "wolfi",
			Source: "github.com/dagger/dagger/modules/wolfi",
		},
	})
	require.NoError(t, err)
	require.Equal(t,
		"Source paths are relative to the workspace root\n"+
			"* indicates a module is a blueprint, with all its functions aliased to the root level\n"+
			"\n"+
			"Name       Source\n"+
			"greeter*   .dagger/modules/greeter\n"+
			"wolfi      github.com/dagger/dagger/modules/wolfi\n",
		out.String(),
	)
}
