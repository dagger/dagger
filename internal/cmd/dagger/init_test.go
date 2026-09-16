package daggercmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSetupCommandChoiceShowsExecutableCommand(t *testing.T) {
	for _, command := range []string{
		"dagger module recommend",
		"dagger cloud checks on",
		"dagger -W '/tmp/project with spaces' module recommend",
	} {
		selected := false
		choice := setupCommandChoice("Run the next setup step?", command, &selected)
		rendered := ansi.Strip(choice.View())
		require.Contains(t, rendered, "Run the next setup step?")
		require.Contains(t, rendered, command)
		require.Contains(t, rendered, "Run")
		require.Contains(t, rendered, "Skip")
	}
}

func TestWorkspaceEntrypointsShareRecommendations(t *testing.T) {
	oldWorkspace := workspaceRef
	workspaceRef = ""
	t.Cleanup(func() { workspaceRef = oldWorkspace })
	var initOutput, migrationOutput bytes.Buffer
	initCmd := &cobra.Command{Use: "init"}
	initCmd.SetOut(&initOutput)
	migrateCmd := &cobra.Command{Use: "migrate"}
	migrateCmd.SetOut(&migrationOutput)
	require.NoError(t, printWorkspaceNextSteps(initCmd))
	require.NoError(t, printWorkspaceNextSteps(migrateCmd))
	require.Equal(t, initOutput.String(), migrationOutput.String())
	require.Contains(t, migrationOutput.String(), "dagger module recommend\n\n# 2. Enable cloud checks\ndagger cloud checks on\n")
}

func TestInitRecommendationsAreShellCommands(t *testing.T) {
	for _, tc := range []struct {
		name              string
		explicitWorkspace bool
		explicitWorkdir   bool
	}{
		{name: "current workspace"},
		{name: "selected workspace", explicitWorkspace: true},
		{name: "selected working directory", explicitWorkdir: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project's $(printf wrong)")
			require.NoError(t, os.Mkdir(root, 0o755))
			t.Chdir(root)
			oldWorkspace := workspaceRef
			workspaceRef = ""
			if tc.explicitWorkspace {
				workspaceRef = root
			}
			t.Cleanup(func() { workspaceRef = oldWorkspace })
			cmd := &cobra.Command{}
			cmd.Flags().String("workdir", "", "")
			if tc.explicitWorkdir {
				require.NoError(t, cmd.Flags().Set("workdir", root))
			}
			var out bytes.Buffer
			cmd.SetOut(&out)
			state := &initState{ConfigPath: filepath.Join(root, "dagger.toml"), Created: true}
			require.NoError(t, printWorkspaceInitialized(cmd, state))
			require.NoError(t, printWorkspaceNextSteps(cmd))
			require.True(t, strings.HasPrefix(out.String(), "Workspace configuration initialized at ./dagger.toml\n"))
			_, script, ok := strings.Cut(out.String(), "To continue setup, run these commands in order:\n\n")
			require.True(t, ok)
			require.True(t, strings.HasPrefix(script, "#!/bin/sh\n"))
			// Execute the printed recipe with a shell function standing in for
			// dagger. This checks both command order and argument quoting.
			shell := exec.Command("sh")
			shell.Stdin = strings.NewReader("dagger() { printf '<%s>\\n' \"$@\"; }\n" + script)
			got, err := shell.CombinedOutput()
			require.NoError(t, err, string(got))
			prefix := ""
			if tc.explicitWorkspace || tc.explicitWorkdir {
				prefix = "<-W>\n<" + root + ">\n"
			}
			require.Equal(t, prefix+"<module>\n<recommend>\n"+prefix+"<cloud>\n<checks>\n<on>\n", string(got))
			cloud, remaining, err := testRootCommand().Find([]string{"cloud", "checks", "on"})
			require.NoError(t, err)
			require.Empty(t, remaining)
			require.Same(t, cloudCheckOnCmd, cloud)
		})
	}
}

func TestInitCommandAndHiddenSetupGuidance(t *testing.T) {
	root := testRootCommand()
	cmd, args, err := root.Find([]string{"init"})
	require.NoError(t, err)
	require.Empty(t, args)
	require.Same(t, initCmd, cmd)
	require.Empty(t, cmd.Aliases)
	cmd, args, err = root.Find([]string{"setup"})
	require.NoError(t, err)
	require.Empty(t, args)
	require.Same(t, setupCmd, cmd)
	require.True(t, cmd.Hidden)
	require.Contains(t, renderHelp(t, cmd), deprecatedSetupHint)
	require.NotContains(t, renderHelp(t, initCmd), "setup")
	require.NotContains(t, renderHelp(t, root), "\n  setup ")
}

func TestDeprecatedSetupOnlyPrintsGuidance(t *testing.T) {
	for _, config := range []string{"", "dagger.json", "dagger.toml"} {
		t.Run("config="+config, func(t *testing.T) {
			root := t.TempDir()
			original := []byte("leave this configuration unchanged\n")
			if config != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, config), original, 0o644))
			}
			daggerCloudWithEnv(t, []string{"DAGGER_ENGINE=tcp://127.0.0.1:1"}, []string{"-W", root, "setup", "-y"}, func(t *testing.T, err error, out, stderr *bytes.Buffer) {
				require.NoError(t, err, stderr.String())
				require.Equal(t, deprecatedSetupHint, out.String())
				files, err := os.ReadDir(root)
				require.NoError(t, err)
				if config == "" {
					require.Empty(t, files)
				} else {
					require.Len(t, files, 1)
					data, err := os.ReadFile(filepath.Join(root, config))
					require.NoError(t, err)
					require.Equal(t, original, data)
				}
			})
		})
	}
}

func TestInitOptionalOperationOrder(t *testing.T) {
	for _, tc := range []struct {
		name         string
		recommend    bool
		recommendErr error
		cloudEnabled bool
		wantCloud    bool
		wantCalls    []string
	}{
		{name: "accept both", recommend: true, wantCloud: true, wantCalls: []string{"module recommend", "install", "cloud status", "cloud checks on"}},
		{name: "skip modules", wantCloud: true, wantCalls: []string{"module recommend", "cloud status", "cloud checks on"}},
		{name: "cloud already enabled", recommend: true, cloudEnabled: true, wantCalls: []string{"module recommend", "install", "cloud status"}},
		{name: "recommendation failure stops sequence", recommend: true, recommendErr: errors.New("recommendation failed"), wantCalls: []string{"module recommend", "install"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			enableCloud, err := offerInitNextSteps(
				func(_, command string) (bool, error) {
					calls = append(calls, command)
					return command != "module recommend" || tc.recommend, nil
				},
				func() error {
					calls = append(calls, "install")
					return tc.recommendErr
				},
				func() bool {
					calls = append(calls, "cloud status")
					return tc.cloudEnabled
				},
			)
			require.ErrorIs(t, err, tc.recommendErr)
			require.Equal(t, tc.wantCloud, enableCloud)
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestInitNonInteractiveDoesNotPrompt(t *testing.T) {
	t.Setenv("DAGGER_TUI_CONSOLE", "")
	for _, mode := range []string{"report", "plain", "logs", "dots"} {
		require.False(t, canPromptForInit(mode, true, false))
	}
	require.False(t, canPromptForInit("tty", false, false))
	require.False(t, canPromptForInit("tty", true, true))
	require.True(t, canPromptForInit("tty", true, false))
}
