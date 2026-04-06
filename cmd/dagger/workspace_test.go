package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/pflag"
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
	require.Contains(t, cmd.Long, "If the current directory is not yet a workspace, this initializes one first.")

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

func TestWorkspaceCommandGrouping(t *testing.T) {
	require.Equal(t, workspaceGroup.ID, initCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, workspaceCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, moduleDepInstallCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, moduleUpdateCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, migrateCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, lockCmd.GroupID)
}

func TestRootHelpShowsWorkspaceCommandGroup(t *testing.T) {
	oldOut := rootCmd.OutOrStdout()
	oldErr := rootCmd.ErrOrStderr()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	t.Cleanup(func() {
		rootCmd.SetOut(oldOut)
		rootCmd.SetErr(oldErr)
	})

	require.NoError(t, rootCmd.Help())

	help := out.String()
	require.Contains(t, help, "DAGGER WORKSPACE COMMANDS")

	workspaceIdx := strings.Index(help, "DAGGER WORKSPACE COMMANDS")
	moduleIdx := strings.Index(help, "DAGGER MODULE COMMANDS")
	require.NotEqual(t, -1, workspaceIdx)
	require.NotEqual(t, -1, moduleIdx)

	workspaceSection := help[workspaceIdx:moduleIdx]
	require.Contains(t, workspaceSection, "  init")
	require.Contains(t, workspaceSection, "  install")
	require.Contains(t, workspaceSection, "  update")
	require.Contains(t, workspaceSection, "  workspace")
	require.Contains(t, workspaceSection, "  migrate")
	require.Contains(t, workspaceSection, "  lock")
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

func TestInstallGlobalFlagsWorkspaceSelection(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	installGlobalFlags(flags)

	workdirFlag := flags.Lookup("workdir")
	require.NotNil(t, workdirFlag)
	require.Empty(t, workdirFlag.Shorthand)
	require.True(t, workdirFlag.Hidden)

	workspaceFlag := flags.Lookup("workspace")
	require.NotNil(t, workspaceFlag)
	require.Equal(t, "W", workspaceFlag.Shorthand)
	require.False(t, workspaceFlag.Hidden)

	webFlag := flags.Lookup("web")
	require.NotNil(t, webFlag)
	require.Equal(t, "w", webFlag.Shorthand)
}

func TestWorkspaceFlagPolicy(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	workspaceRef = "github.com/acme/ws"
	require.ErrorContains(t, validateWorkspaceFlagPolicy(workspaceInitCmd, nil), "must be a local path")
	require.ErrorContains(t, validateWorkspaceFlagPolicy(migrateCmd, nil), "not supported")
	require.ErrorContains(t, validateWorkspaceFlagPolicy(workspaceConfigCmd, []string{"modules.foo.source", "x"}), "must be a local path")
	require.NoError(t, validateWorkspaceFlagPolicy(workspaceConfigCmd, []string{"modules.foo.source"}))

	workspaceRef = "./local-workspace"
	require.NoError(t, validateWorkspaceFlagPolicy(workspaceInitCmd, nil))
	require.NoError(t, validateWorkspaceFlagPolicy(callModCmd.Command(), nil))
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
		"Source paths below are resolved and shown relative to the workspace root\n"+
			"\"dagger workspace config\" prints the raw values stored in .dagger/config.toml, so local sources may look different there\n"+
			"* indicates a module is a blueprint, with all its functions aliased to the root level\n"+
			"\n"+
			"Name       Resolved Source\n"+
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
