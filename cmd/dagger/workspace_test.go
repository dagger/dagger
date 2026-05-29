package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestInstallAndUpdateCommandFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"install"})
	require.NoError(t, err)
	require.Nil(t, cmd.Flags().Lookup("mod"))
	require.Nil(t, cmd.Flags().Lookup("compat"))
	require.NotNil(t, cmd.Flags().Lookup("name"))
	require.Contains(t, cmd.Long, "If no workspace config is selected")

	cmd, _, err = rootCmd.Find([]string{"update"})
	require.NoError(t, err)
	require.Nil(t, cmd.Flags().Lookup("mod"))
	require.Nil(t, cmd.Flags().Lookup("compat"))
}

func TestWorkspaceCommandGrouping(t *testing.T) {
	require.Equal(t, workspaceGroup.ID, configCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, envCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, settingsCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, workspaceCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, moduleDepInstallCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, moduleUpdateCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, migrateCmd.GroupID)
	require.Equal(t, workspaceGroup.ID, lockCmd.GroupID)
}

func TestRemovedWorkspaceCommands(t *testing.T) {
	for _, cmd := range workspaceCmd.Commands() {
		require.NotEqual(t, "list", cmd.Name())
		require.NotEqual(t, "info", cmd.Name())
	}
}

func TestExecutionCommandGrouping(t *testing.T) {
	require.Equal(t, execGroup.ID, queryCmd.GroupID)
	require.Equal(t, execGroup.ID, runCmd.GroupID)
	require.Equal(t, execGroup.ID, checksCmd.GroupID)
	require.Equal(t, execGroup.ID, generateCmd.GroupID)
	require.Equal(t, execGroup.ID, upCmd.GroupID)
	require.False(t, checksCmd.Hidden)
	require.False(t, generateCmd.Hidden)
	require.False(t, upCmd.Hidden)
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
	require.NotEqual(t, -1, workspaceIdx)

	sectionEnd := len(help)
	if execIdx := strings.Index(help[workspaceIdx:], "EXECUTION COMMANDS"); execIdx != -1 {
		sectionEnd = workspaceIdx + execIdx
	}
	if moduleIdx := strings.Index(help[workspaceIdx:], "DAGGER MODULE COMMANDS"); moduleIdx != -1 && workspaceIdx+moduleIdx < sectionEnd {
		sectionEnd = workspaceIdx + moduleIdx
	}

	workspaceSection := help[workspaceIdx:sectionEnd]
	require.Contains(t, workspaceSection, "  config")
	require.Contains(t, workspaceSection, "  env")
	require.NotContains(t, workspaceSection, "  init")
	require.Contains(t, workspaceSection, "  install")
	require.Contains(t, workspaceSection, "  update")
	require.Contains(t, workspaceSection, "  settings")
	require.Contains(t, workspaceSection, "  workspace")
	require.Contains(t, workspaceSection, "  migrate")
	require.Contains(t, workspaceSection, "  lock")
}

func TestRootHelpShowsExecutionCommandGroup(t *testing.T) {
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
	require.Contains(t, help, "EXECUTION COMMANDS")

	require.Contains(t, help, "  check")
	require.Contains(t, help, "  generate")
	require.Contains(t, help, "  query")
	require.Contains(t, help, "  run")
	require.Contains(t, help, "  up")
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

func TestParseGlobalFlagsAfterDynamicCommand(t *testing.T) {
	oldWorkdir := workdir
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workdir = oldWorkdir
		workspaceRef = oldWorkspaceRef
	})

	workdir = "."
	workspaceRef = ""

	parseGlobalFlags([]string{"call", "--workdir", "/work/shell", "-W", "./ws", "identify"})

	require.Equal(t, "/work/shell", workdir)
	require.Equal(t, "./ws", workspaceRef)
}

func TestConfigAliasFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"config"})
	require.NoError(t, err)
	require.Same(t, configCmd, cmd)
	require.Nil(t, cmd.Flags().Lookup("mod"))
	require.Nil(t, cmd.Flags().Lookup("json"))
}

func TestWorkspaceFlagPolicy(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	oldWorkspaceEnv := workspaceEnv
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
		workspaceEnv = oldWorkspaceEnv
	})

	workspaceRef = "github.com/acme/ws"
	require.ErrorContains(t, validateWorkspaceFlagPolicy(workspaceInitCmd, nil), "must be a local path")
	require.ErrorContains(t, validateWorkspaceFlagPolicy(migrateCmd, nil), "not supported")
	require.ErrorContains(t, validateWorkspaceFlagPolicy(configCmd, []string{"modules.foo.source", "x"}), "must be a local path")
	require.NoError(t, validateWorkspaceFlagPolicy(configCmd, []string{"modules.foo.source"}))
	require.ErrorContains(t, validateWorkspaceFlagPolicy(settingsCmd, []string{"foo", "bar", "baz"}), "must be a local path")
	require.NoError(t, validateWorkspaceFlagPolicy(settingsCmd, []string{"foo", "bar"}))
	require.ErrorContains(t, validateWorkspaceFlagPolicy(workspaceConfigCmd, []string{"modules.foo.source", "x"}), "must be a local path")
	require.NoError(t, validateWorkspaceFlagPolicy(workspaceConfigCmd, []string{"modules.foo.source"}))
	require.ErrorContains(t, validateWorkspaceFlagPolicy(envCreateCmd, []string{"ci"}), "must be a local path")
	require.ErrorContains(t, validateWorkspaceFlagPolicy(envRmCmd, []string{"ci"}), "must be a local path")

	workspaceRef = "./local-workspace"
	require.NoError(t, validateWorkspaceFlagPolicy(workspaceInitCmd, nil))
	require.NoError(t, validateWorkspaceFlagPolicy(callModCmd.Command(), nil))
	require.NoError(t, validateWorkspaceFlagPolicy(settingsCmd, []string{"foo", "bar", "baz"}))
	require.NoError(t, validateWorkspaceFlagPolicy(envCreateCmd, []string{"ci"}))
}

func TestApplyWorkspaceClientParams(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	oldWorkspaceEnv := workspaceEnv
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
		workspaceEnv = oldWorkspaceEnv
	})

	workspaceRef = "github.com/acme/ws"
	workspaceEnv = "ci"

	params := client.Params{}
	require.NoError(t, applyWorkspaceClientParams(&params))
	require.NotNil(t, params.Workspace)
	require.NotNil(t, params.WorkspaceEnv)
	require.Equal(t, "github.com/acme/ws", *params.Workspace)
	require.Equal(t, "ci", *params.WorkspaceEnv)

	explicitWorkspace := "github.com/acme/other"
	explicitEnv := "prod"
	params = client.Params{
		Workspace:    &explicitWorkspace,
		WorkspaceEnv: &explicitEnv,
	}
	require.NoError(t, applyWorkspaceClientParams(&params))
	require.Equal(t, "github.com/acme/other", *params.Workspace)
	require.Equal(t, "prod", *params.WorkspaceEnv)
}

func TestApplyWorkspaceClientParamsResolvesLocalWorkspaceAfterWorkdir(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	oldWorkspaceEnv := workspaceEnv
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
		workspaceEnv = oldWorkspaceEnv
	})

	dir := t.TempDir()
	shellDir := filepath.Join(dir, "shell")
	workspaceDir := filepath.Join(shellDir, "ws")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	tests := []struct {
		name string
		cwd  string
		ref  string
	}{
		{
			name: "relative subdir",
			cwd:  shellDir,
			ref:  "./ws",
		},
		{
			name: "current directory",
			cwd:  workspaceDir,
			ref:  ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(tt.cwd)
			workspaceRef = tt.ref
			params := client.Params{}
			require.NoError(t, applyWorkspaceClientParams(&params))
			require.NotNil(t, params.Workspace)
			require.Equal(t, workspaceDir, *params.Workspace)
		})
	}
}

func TestParseWorkspaceRemoteAddressPreservesSubdir(t *testing.T) {
	remote, ok, err := parseWorkspaceRemoteAddress(t.Context(), "github.com/acme/mono/services/api@main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "github.com/acme/mono", remote.CloneRef)
	require.Equal(t, "services/api", remote.Path)
	require.Equal(t, "main", remote.Version)
	require.Equal(t, "github.com/acme/mono/services/api", remote.BaseAddress)

	remote, ok, err = parseWorkspaceRemoteAddress(t.Context(), "https://github.com/acme/mono#release-1.2:services/api")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "https://github.com/acme/mono", remote.CloneRef)
	require.Equal(t, "services/api", remote.Path)
	require.Equal(t, "release-1.2", remote.Version)
	require.Equal(t, "https://github.com/acme/mono/services/api", remote.BaseAddress)
}

func TestWorkspaceAddressLooksRemote(t *testing.T) {
	require.True(t, workspaceAddressLooksRemote("github.com/acme/mono/services/api@main"))
	require.True(t, workspaceAddressLooksRemote("https://github.com/acme/mono/services/api@main"))
	require.False(t, workspaceAddressLooksRemote("."))
	require.False(t, workspaceAddressLooksRemote("./services/api"))
	require.False(t, workspaceAddressLooksRemote("file:///repo/services/api"))
}

func TestWorkspaceRemoteVersionKind(t *testing.T) {
	require.Equal(t, "pr", workspaceRemoteVersionKind("pull/42/head"))
	require.Equal(t, "sha", workspaceRemoteVersionKind("abcdef1"))
	require.Equal(t, "ref", workspaceRemoteVersionKind("feature/name"))
}
