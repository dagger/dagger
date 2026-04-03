package main

import (
	"bytes"
	"context"
	"io"
	"testing"

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

func TestInstallAndUpdateCommandFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"install"})
	require.NoError(t, err)
	require.Nil(t, cmd.Flags().Lookup("mod"))
	require.Nil(t, cmd.Flags().Lookup("compat"))
	require.NotNil(t, cmd.Flags().Lookup("name"))

	cmd, _, err = rootCmd.Find([]string{"module", "install"})
	require.NoError(t, err)
	require.NotNil(t, cmd.Flags().Lookup("mod"))
	require.NotNil(t, cmd.Flags().Lookup("compat"))
	require.NotNil(t, cmd.Flags().Lookup("name"))

	cmd, _, err = rootCmd.Find([]string{"update"})
	require.NoError(t, err)
	require.Nil(t, cmd.Flags().Lookup("mod"))
	require.Nil(t, cmd.Flags().Lookup("compat"))

	cmd, _, err = rootCmd.Find([]string{"module", "update"})
	require.NoError(t, err)
	require.NotNil(t, cmd.Flags().Lookup("mod"))
	require.NotNil(t, cmd.Flags().Lookup("compat"))
}

func TestGenHelpDoesNotPanicWithModuleSubcommands(t *testing.T) {
	oldOut := rootCmd.OutOrStdout()
	oldErr := rootCmd.ErrOrStderr()
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs([]string{"gen", "--help"})
	t.Cleanup(func() {
		rootCmd.SetOut(oldOut)
		rootCmd.SetErr(oldErr)
		rootCmd.SetArgs(nil)
	})

	_, err := rootCmd.ExecuteC()
	require.NoError(t, err)
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

	err := writeWorkspaceModuleList(context.Background(), &out, []workspaceModuleView{
		testWorkspaceModuleView{
			name:      "greeter",
			blueprint: true,
			source:    ".dagger/modules/greeter",
		},
		testWorkspaceModuleView{
			name:   "wolfi",
			source: "github.com/dagger/dagger/modules/wolfi",
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

type testWorkspaceModuleView struct {
	name      string
	source    string
	blueprint bool
}

func (m testWorkspaceModuleView) Name(context.Context) (string, error) {
	return m.name, nil
}

func (m testWorkspaceModuleView) Source(context.Context) (string, error) {
	return m.source, nil
}

func (m testWorkspaceModuleView) Blueprint(context.Context) (bool, error) {
	return m.blueprint, nil
}
