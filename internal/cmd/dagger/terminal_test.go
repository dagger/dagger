package daggercmd

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestShellCommandWithoutTargetShowsGuidance(t *testing.T) {
	oldListMode := terminalListMode
	t.Cleanup(func() { terminalListMode = oldListMode })
	terminalListMode = false

	var out bytes.Buffer
	cmd := &cobra.Command{Use: shellCmd.Use}
	cmd.SetOut(&out)
	err := runTerminalCommand(cmd, nil)
	require.NoError(t, err)
	require.Equal(t, `Choose a shell to open.

  dagger shell -l       List available shells
  dagger shell <NAME>   Open a shell from that list
`, out.String())
}

func TestShellLegacyCommandStopsBeforeSetup(t *testing.T) {
	parent := shellCmd.Parent()
	oldListMode := terminalListMode
	oldSilenceUsage := shellCmd.SilenceUsage
	commandFlag := shellCmd.Flags().Lookup("command")
	oldCommand := commandFlag.Value.String()
	oldCommandChanged := commandFlag.Changed
	listFlag := shellCmd.Flags().Lookup("list")
	oldListChanged := listFlag.Changed
	t.Cleanup(func() {
		parent.AddCommand(shellCmd)
		terminalListMode = oldListMode
		shellCmd.SilenceUsage = oldSilenceUsage
		require.NoError(t, commandFlag.Value.Set(oldCommand))
		commandFlag.Changed = oldCommandChanged
		listFlag.Changed = oldListChanged
	})

	setupCalled := false
	root := &cobra.Command{
		Use:           "dagger",
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			setupCalled = true
			return fmt.Errorf("setup must not run")
		},
	}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.AddGroup(&cobra.Group{ID: shellCmd.GroupID})
	parent.RemoveCommand(shellCmd)
	root.AddCommand(shellCmd)

	for _, args := range [][]string{
		{"-c"},
		{"-c", ".echo hello"},
		{"-c", ""},
		{"--command=.echo hello"},
		{"--command"},
		{"-l", "-c", ".echo hello"},
		{"go:dev", "-c", ".echo hello"},
	} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			commandFlag.Changed = false
			listFlag.Changed = false
			terminalListMode = false
			shellCmd.SilenceUsage = oldSilenceUsage
			setupCalled = false
			root.SetArgs(append([]string{"shell"}, args...))

			err := root.Execute()
			require.EqualError(t, err, "'dagger shell -c' is no longer supported; use 'dagger -c' to run Dagger scripts")
			require.False(t, setupCalled)
			require.True(t, shellCmd.SilenceUsage)
		})
	}

	help := renderHelp(t, shellCmd)
	require.NotContains(t, help, "--command")
	require.Contains(t, help, "--list")
}
