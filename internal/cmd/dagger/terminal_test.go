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
	oldCopies := terminalCopies
	flags := []string{"command", "list", "copy"}
	resetFlags := func() {
		for _, name := range flags {
			shellCmd.Flags().Lookup(name).Changed = false
		}
	}
	t.Cleanup(func() {
		parent.AddCommand(shellCmd)
		terminalListMode = oldListMode
		terminalCommand = oldCommand
		terminalCopies = oldCopies
		resetFlags()
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
		{[]string{"-l", "--copy", "src"}, "--list and --copy cannot be used together"},
		{[]string{"go:dev", "ls"}, "accepts at most 1 arg(s), received 2"},
	} {
		t.Run(fmt.Sprint(tc.args), func(t *testing.T) {
			resetFlags()
			terminalListMode = false
			terminalCopies = nil
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

func TestParseTerminalCopy(t *testing.T) {
	for _, tc := range []struct {
		arg, path, source, err string
	}{
		{arg: "./src", path: ".", source: "./src"},
		{arg: "/app=./src", path: "/app", source: "./src"},
		{arg: "app=https://github.com/dagger/dagger#main", path: "app", source: "https://github.com/dagger/dagger#main"},
		{arg: "https://example.com/repo?ref=main", path: ".", source: "https://example.com/repo?ref=main"},
		{arg: "=./src", err: `invalid --copy "=./src": expected [PATH=]SOURCE`},
		{arg: "/app=", err: `invalid --copy "/app=": expected [PATH=]SOURCE`},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			path, source, err := parseTerminalCopy(tc.arg)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.path, path)
			require.Equal(t, tc.source, source)
		})
	}
}
