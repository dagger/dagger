package daggercmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/client"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestInstallAndUpdateCommandFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"install"})
	require.NoError(t, err)
	require.False(t, cmd.Hidden)
	require.Nil(t, cmd.Flags().Lookup("load-module"))
	require.Nil(t, cmd.Flags().Lookup("compat"))
	require.NotNil(t, cmd.Flags().Lookup("name"))
	require.Contains(t, cmd.Long, "If no workspace config is selected")

	cmd, _, err = rootCmd.Find([]string{"update"})
	require.NoError(t, err)
	require.False(t, cmd.Hidden)
	require.Nil(t, cmd.Flags().Lookup("load-module"))
	require.Nil(t, cmd.Flags().Lookup("compat"))
}

func TestWorkspaceCommandAliases(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"ws"})
	require.NoError(t, err)
	require.Same(t, workspaceCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"i"})
	require.NoError(t, err)
	require.Same(t, moduleDepInstallCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"un"})
	require.NoError(t, err)
	require.Same(t, moduleDepUninstallCmd, cmd)
	require.False(t, cmd.Hidden)
}

func TestCosmeticCommandAliases(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"api", "call"})
	require.NoError(t, err)
	require.Same(t, apiCallCmd.Command(), cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"api", "functions"})
	require.NoError(t, err)
	require.Same(t, apiFunctionsCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"api", "client"})
	require.NoError(t, err)
	require.Same(t, apiClientCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"client"})
	require.NoError(t, err)
	require.NotSame(t, apiClientCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"call"})
	require.NoError(t, err)
	require.Same(t, callModCmd.Command(), cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"core"})
	require.NoError(t, err)
	require.Same(t, callCoreCmd.Command(), cmd)
	require.True(t, cmd.Hidden)
	require.Contains(t, cmd.Deprecated, "dagger -m core api call")

	// exec / run moved under `dagger api`; no longer reachable at root.
	cmd, _, err = rootCmd.Find([]string{"api", "exec"})
	require.NoError(t, err)
	require.Same(t, runCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"api", "run"})
	require.NoError(t, err)
	require.Same(t, runCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"api"})
	require.NoError(t, err)
	require.Same(t, apiCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"api", "query"})
	require.NoError(t, err)
	require.Same(t, apiQueryCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"query"})
	require.NoError(t, err)
	require.Same(t, queryCmd, cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"api", "listen"})
	require.NoError(t, err)
	require.Same(t, apiListenCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"listen"})
	require.NoError(t, err)
	require.Same(t, listenCmd, cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"api", "session"})
	require.NoError(t, err)
	require.Same(t, apiSessionCmd, cmd)

	cmd, _, err = rootCmd.Find([]string{"session"})
	require.NoError(t, err)
	require.Same(t, sessionAliasCmd, cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"workspace", "config"})
	require.NoError(t, err)
	require.Same(t, workspaceConfigCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"settings"})
	require.NoError(t, err)
	require.Same(t, settingsCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"uninstall"})
	require.NoError(t, err)
	require.Same(t, moduleDepUninstallCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"installed"})
	require.NoError(t, err)
	require.Same(t, installedCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"lock"})
	require.NoError(t, err)
	require.Same(t, lockCmd, cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"cloud", "login"})
	require.NoError(t, err)
	require.Same(t, cloudLoginCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"login"})
	require.NoError(t, err)
	require.Same(t, loginCmd, cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"cloud", "logout"})
	require.NoError(t, err)
	require.Same(t, cloudLogoutCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"logout"})
	require.NoError(t, err)
	require.Same(t, logoutCmd, cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"cloud", "org"})
	require.NoError(t, err)
	require.Same(t, cloudOrgCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"org"})
	require.NoError(t, err)
	require.Same(t, orgCmd, cmd)
	require.True(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"cloud", "billing"})
	require.NoError(t, err)
	require.Same(t, cloudBillingCmd, cmd)
	require.False(t, cmd.Hidden)

	cmd, _, err = rootCmd.Find([]string{"billing"})
	require.NoError(t, err)
	require.Same(t, billingCmd, cmd)
	require.True(t, cmd.Hidden)
}

func TestRemovedWorkspaceCommands(t *testing.T) {
	for _, cmd := range workspaceCmd.Commands() {
		require.NotEqual(t, "list", cmd.Name())
		require.NotEqual(t, "info", cmd.Name())
	}
}

func TestRootHelpShowsImplicitCommandGrouping(t *testing.T) {
	help := renderHelp(t, rootCmd)
	require.Contains(t, help, "AVAILABLE COMMANDS")
	require.NotContains(t, help, "DAGGER CLOUD COMMANDS")
	require.NotContains(t, help, "DAGGER MODULE COMMANDS")
	require.NotContains(t, help, "DAGGER WORKSPACE COMMANDS")
	require.NotContains(t, help, "EXECUTION COMMANDS")
	require.NotContains(t, help, "check, checks")
	require.NotContains(t, help, "function, fn")
	require.NotContains(t, help, "module, mod")
	require.Contains(t, help, "workspace, ws")
	require.NotContains(t, help, "exec, run")

	names := rootHelpCommandNames(help)
	for _, name := range []string{
		"activity",
		"check",
		"generate",
		"install",
		"installed",
		"search",
		"settings",
		"setup",
		"uninstall",
		"up",
		"update",
		"version",
		"api",
		"cloud",
		"module",
		"sdk",
		"workspace",
	} {
		require.Contains(t, names, name)
	}

	for _, name := range []string{
		"billing",
		"call",
		"completion",
		"config",
		"core",
		"env",
		"exec",
		"function",
		"functions",
		"integration",
		"listen",
		"lock",
		"login",
		"logout",
		"org",
		"query",
		"run",
		"session",
	} {
		require.NotContains(t, names, name)
	}

	for _, leaf := range []string{"activity", "check", "generate", "install", "installed", "search", "settings", "setup", "uninstall", "up", "update"} {
		for _, parent := range []string{"api", "cloud", "module", "sdk", "workspace"} {
			require.Less(t, commandIndex(names, leaf), commandIndex(names, parent))
		}
	}
}

func TestHelpAliasesRespectHiddenAliases(t *testing.T) {
	require.Contains(t, renderHelp(t, workspaceCmd), "workspace, ws")

	execHelp := renderHelp(t, runCmd)
	require.NotContains(t, execHelp, "exec, run")
	require.NotContains(t, execHelp, "exec, r")
	require.NotContains(t, execHelp, "ALIASES")
}

func TestWorkspaceSettingConfigKeyQuotesDynamicSegments(t *testing.T) {
	require.Equal(t,
		`modules."my.module".settings."some.key"`,
		workspaceSettingConfigKey("my.module", "some.key"),
	)
}

func renderHelp(t *testing.T, cmd *cobra.Command) string {
	t.Helper()

	oldOut := cmd.OutOrStdout()
	oldErr := cmd.ErrOrStderr()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	t.Cleanup(func() {
		cmd.SetOut(oldOut)
		cmd.SetErr(oldErr)
	})

	require.NoError(t, cmd.Help())
	return out.String()
}

func rootHelpCommandNames(help string) []string {
	start := strings.Index(help, "AVAILABLE COMMANDS")
	if start == -1 {
		return nil
	}

	section := help[start:]
	if end := strings.Index(section, "\nOPTIONS"); end != -1 {
		section = section[:end]
	}
	if end := strings.Index(section, "\nINHERITED OPTIONS"); end != -1 {
		section = section[:end]
	}

	var names []string
	for _, line := range strings.Split(section, "\n") {
		if len(line) < 3 || !strings.HasPrefix(line, "  ") || line[2] == ' ' || line[2] == '-' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, strings.TrimSuffix(fields[0], ","))
		}
	}
	return names
}

func commandIndex(names []string, name string) int {
	for i, commandName := range names {
		if commandName == name {
			return i
		}
	}
	return -1
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

func TestWorkspaceFlagPolicy(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	oldWorkspaceEnv := workspaceEnv
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
		workspaceEnv = oldWorkspaceEnv
	})

	workspaceRef = "github.com/acme/ws"
	require.ErrorContains(t, validateWorkspaceFlagPolicy(settingsCmd, []string{"foo", "bar", "baz"}), "must be a local path")
	require.NoError(t, validateWorkspaceFlagPolicy(settingsCmd, []string{"foo", "bar"}))
	require.ErrorContains(t, validateWorkspaceFlagPolicy(workspaceSettingsCmd, []string{"foo", "bar", "baz"}), "must be a local path")
	require.NoError(t, validateWorkspaceFlagPolicy(workspaceSettingsCmd, []string{"foo", "bar"}))
	require.ErrorContains(t, validateWorkspaceFlagPolicy(workspaceConfigCmd, []string{"modules.foo.source", "x"}), "must be a local path")
	require.NoError(t, validateWorkspaceFlagPolicy(workspaceConfigCmd, []string{"modules.foo.source"}))

	workspaceRef = "./local-workspace"
	require.NoError(t, validateWorkspaceFlagPolicy(apiCallCmd.Command(), nil))
	require.NoError(t, validateWorkspaceFlagPolicy(callModCmd.Command(), nil))
	require.NoError(t, validateWorkspaceFlagPolicy(settingsCmd, []string{"foo", "bar", "baz"}))
	require.NoError(t, validateWorkspaceFlagPolicy(workspaceSettingsCmd, []string{"foo", "bar", "baz"}))
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

func TestRenderWorkspaceRemoteRowsIncludesAutocheck(t *testing.T) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	renderWorkspaceRemoteRows(cmd, []*workspaceRemoteRow{{
		Kind:      "branch",
		Address:   "github.com/acme/mono@main",
		Autocheck: "on",
		Checks:    "🟢1",
	}})

	require.Contains(t, out.String(), "AUTOCHECK")
	require.Contains(t, out.String(), "on")
	require.Contains(t, out.String(), "🟢1")
}

func TestWorkspaceAutocheckSelectedSourceRepos(t *testing.T) {
	selected, enabled := workspaceSelectedSourceRepos([]cloudapi.SourceRepository{
		{Repository: "github.com/acme/one", Selected: true},
		{Repository: "https://github.com/acme/two", Selected: true},
		{Repository: "github.com/acme/three", Selected: false},
	}, "github.com/acme/two")

	require.True(t, enabled)
	require.Equal(t, []string{"github.com/acme/one", "github.com/acme/two"}, selected)
}

func TestSetWorkspaceAutocheckRepoSelected(t *testing.T) {
	selected := []string{"github.com/acme/one", "https://github.com/acme/two"}

	require.Equal(t,
		[]string{"github.com/acme/one"},
		setWorkspaceAutocheckRepoSelected(selected, "github.com/acme/two", false),
	)
	require.Equal(t,
		[]string{"github.com/acme/one", "github.com/acme/three", "github.com/acme/two"},
		setWorkspaceAutocheckRepoSelected(selected, "github.com/acme/three", true),
	)
	require.Equal(t,
		[]string{"github.com/acme/one", "github.com/acme/two"},
		setWorkspaceAutocheckRepoSelected(selected, "github.com/acme/two", true),
	)
}

func TestSelectedRemoteWorkspaceAddressInfersLocalGitWorkspace(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "remote", "add", "origin", "git@github.com:acme/mono.git")
	runGit(t, repo, "checkout", "-b", "feature/work")

	workspaceDir := filepath.Join(repo, "services", "api")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "dagger.toml"), []byte("# workspace\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, "subdir"), 0o755))

	workspaceRef = ""
	t.Chdir(filepath.Join(workspaceDir, "subdir"))
	remote, address, err := selectedRemoteWorkspaceAddress(t.Context(), "workspace activity")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/mono/services/api@feature/work", address)
	require.Equal(t, "github.com/acme/mono", remote.CloneRef)
	require.Equal(t, "services/api", remote.Path)
	require.Equal(t, "feature/work", remote.Version)

	workspaceRef = "."
	remote, address, err = selectedRemoteWorkspaceAddress(t.Context(), "workspace activity")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/mono/services/api@feature/work", address)
	require.Equal(t, "services/api", remote.Path)
}

func TestInferCleanLocalWorkspaceRemoteAddressUsesHeadCommit(t *testing.T) {
	repo, workspaceDir, sha := setupCleanWorkspaceRepo(t)

	t.Chdir(workspaceDir)
	remote, address, dirty, err := inferCleanLocalWorkspaceRemoteAddress(t.Context(), "")
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, "github.com/acme/mono/services/api@"+sha, address)
	require.Equal(t, "github.com/acme/mono", remote.CloneRef)
	require.Equal(t, "services/api", remote.Path)
	require.Equal(t, sha, remote.Version)

	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty\n"), 0o600))
	_, address, dirty, err = inferCleanLocalWorkspaceRemoteAddress(t.Context(), "")
	require.NoError(t, err)
	require.True(t, dirty)
	require.Empty(t, address)
}

func TestCurrentWorkspaceRemoteAddress(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	workspaceRef = "github.com/acme/mono/services/api@main"
	address, ok, err := currentWorkspaceRemoteAddress(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "github.com/acme/mono/services/api@main", address)

	t.Chdir(t.TempDir())
	workspaceRef = ""
	address, ok, err = currentWorkspaceRemoteAddress(t.Context())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, address)
}

func TestSelectedRemoteWorkspaceAddressRequiresRemoteOrGitWorkspace(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	t.Chdir(t.TempDir())

	workspaceRef = ""
	_, _, err := selectedRemoteWorkspaceAddress(t.Context(), "workspace activity")
	require.ErrorContains(t, err, "requires a remote workspace selected with -W or a local workspace with git origin")

	workspaceRef = "."
	_, _, err = selectedRemoteWorkspaceAddress(t.Context(), "workspace activity")
	require.ErrorContains(t, err, "only supports remote workspaces or local git workspaces")
}

func TestSelectedRemoteWorkspaceAddressUsesExplicitRemoteWorkspace(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	workspaceRef = "github.com/acme/mono/services/api@main"
	remote, address, err := selectedRemoteWorkspaceAddress(t.Context(), "workspace activity")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/mono/services/api@main", address)
	require.Equal(t, "github.com/acme/mono", remote.CloneRef)
	require.Equal(t, "services/api", remote.Path)
	require.Equal(t, "main", remote.Version)
}

func TestCheckPastWorkspaceAddress(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	workspaceRef = "github.com/acme/mono/services/api@main"
	address, ok, reason, err := checkPastWorkspaceAddress(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, reason)
	require.Equal(t, "github.com/acme/mono/services/api@main", address)

	workspaceRef = ""
	t.Chdir(t.TempDir())
	address, ok, reason, err = checkPastWorkspaceAddress(t.Context())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, address)
	require.Contains(t, reason, "find git root")

	_, workspaceDir, sha := setupCleanWorkspaceRepo(t)
	workspaceRef = "."
	t.Chdir(workspaceDir)
	address, ok, reason, err = checkPastWorkspaceAddress(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, reason)
	require.Equal(t, "github.com/acme/mono/services/api@"+sha, address)

	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "dirty.txt"), []byte("dirty\n"), 0o600))
	address, ok, reason, err = checkPastWorkspaceAddress(t.Context())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, address)
	require.Equal(t, "workspace has uncommitted changes", reason)
}

func TestNormalizeWorkspaceGitOrigin(t *testing.T) {
	require.Equal(t, "github.com/acme/mono", normalizeWorkspaceGitOrigin("git@github.com:acme/mono.git"))
	require.Equal(t, "github.com/acme/mono", normalizeWorkspaceGitOrigin("https://github.com/acme/mono.git"))
	require.Equal(t, "ssh://git@example.com/acme/mono", normalizeWorkspaceGitOrigin("ssh://git@example.com/acme/mono.git"))
}

func setupCleanWorkspaceRepo(t *testing.T) (string, string, string) {
	t.Helper()

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "remote", "add", "origin", "git@github.com:acme/mono.git")
	runGit(t, repo, "checkout", "-b", "feature/work")

	workspaceDir := filepath.Join(repo, "services", "api")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "dagger.toml"), []byte("# workspace\n"), 0o600))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "initial")

	sha, err := gitOutput(t.Context(), repo, "rev-parse", "HEAD")
	require.NoError(t, err)
	return repo, workspaceDir, sha
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
}

func TestWorkspaceRootFromAddressUsesPublicCwd(t *testing.T) {
	t.Run("file address nested cwd", func(t *testing.T) {
		got, err := workspaceRootFromAddress("file:///tmp/repo/services/api", "/services/api")
		require.NoError(t, err)
		require.Equal(t, filepath.Join(string(filepath.Separator), "tmp", "repo"), got)
	})

	t.Run("file address root cwd", func(t *testing.T) {
		got, err := workspaceRootFromAddress("file:///tmp/repo", "/")
		require.NoError(t, err)
		require.Equal(t, filepath.Join(string(filepath.Separator), "tmp", "repo"), got)
	})

	t.Run("git address nested cwd", func(t *testing.T) {
		got, err := workspaceRootFromAddress("github.com/acme/repo/services/api@v1.2.3", "/services/api")
		require.NoError(t, err)
		require.Equal(t, "github.com/acme/repo@v1.2.3", got)
	})

	t.Run("rejects escaping cwd", func(t *testing.T) {
		_, err := workspaceRootFromAddress("file:///tmp/repo", "../outside")
		require.ErrorContains(t, err, "escapes workspace root")
	})

	t.Run("rejects public escaping cwd", func(t *testing.T) {
		_, err := workspaceRootFromAddress("file:///tmp/repo", "/../outside")
		require.ErrorContains(t, err, "escapes workspace root")
	})

	t.Run("rejects file address outside cwd", func(t *testing.T) {
		_, err := workspaceRootFromAddress("file:///tmp/repo/other", "/services/api")
		require.ErrorContains(t, err, "is not within workspace cwd")
	})

	t.Run("rejects git address outside cwd", func(t *testing.T) {
		_, err := workspaceRootFromAddress("github.com/acme/repo/other@v1.2.3", "/services/api")
		require.ErrorContains(t, err, "is not within workspace cwd")
	})
}
