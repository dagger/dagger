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

func TestShellCommandFlagValidation(t *testing.T) {
	parent := shellCmd.Parent()
	oldListMode := terminalListMode
	oldCommand := terminalCommand
	commandFlag := shellCmd.Flags().Lookup("command")
	listFlag := shellCmd.Flags().Lookup("list")
	t.Cleanup(func() {
		parent.AddCommand(shellCmd)
		terminalListMode = oldListMode
		terminalCommand = oldCommand
		commandFlag.Changed = false
		listFlag.Changed = false
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

	for _, tc := range []struct {
		args []string
		err  string
	}{
		{[]string{"-c", "ls"}, "--command requires a shell NAME"},
		{[]string{"-l", "-c", "ls"}, "--list and --command cannot be used together"},
		{[]string{"go:dev", "-l", "-c", "ls"}, "--list and --command cannot be used together"},
		{[]string{"go:dev", "ls"}, "accepts at most 1 arg(s), received 2"},
	} {
		t.Run(fmt.Sprint(tc.args), func(t *testing.T) {
			commandFlag.Changed = false
			listFlag.Changed = false
			terminalListMode = false
			setupCalled = false
			root.SetArgs(append([]string{"shell"}, tc.args...))

			err := root.Execute()
			require.EqualError(t, err, tc.err)
			require.False(t, setupCalled)
		})
	}

	help := renderHelp(t, shellCmd)
	require.Contains(t, help, "--command")
	require.Contains(t, help, "--list")
}
