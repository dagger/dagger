package daggercmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestUpReportsStartRename(t *testing.T) {
	root := testRootCommand()
	oldArgs, oldProgress, oldWorkspaceRef, oldPreRun := os.Args, progress, workspaceRef, root.PersistentPreRunE
	// The real hook reaches the network for analytics and the update check.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		cmd.SilenceUsage = true
		return nil
	}
	t.Cleanup(func() {
		os.Args, progress, workspaceRef, root.PersistentPreRunE = oldArgs, oldProgress, oldWorkspaceRef, oldPreRun
		root.SetArgs(nil)
	})

	for _, tc := range []struct {
		cmdline []string
		start   string
	}{
		{[]string{"up"}, "dagger start"},
		{[]string{"up", "web", "api"}, "dagger start web api"},
		{[]string{"up", "-l"}, "dagger start -l"},
		{[]string{"up", "--list", "web"}, "dagger start --list web"},
		{[]string{"up", "-h"}, "dagger start -h"},
		{[]string{"up", "--help"}, "dagger start --help"},
		{[]string{"up", "--engine=cloud", "web"}, "dagger start --engine=cloud web"},
		{[]string{"up", "up"}, "dagger start up"},
		{[]string{"--progress=plain", "up", "web"}, "dagger --progress=plain start web"},
		{[]string{"-W", "../other app", "up", "-l"}, "dagger -W '../other app' start -l"},
	} {
		t.Run(strings.Join(tc.cmdline, " "), func(t *testing.T) {
			require.NoError(t, validateFlagCapabilities(root, tc.cmdline))

			os.Args = append([]string{"/usr/local/bin/dagger"}, tc.cmdline...)
			root.SetArgs(tc.cmdline)
			cmd, err := root.ExecuteC()
			require.Same(t, upCmd, cmd)
			require.EqualError(t, err,
				"\"dagger up\" has been renamed to \"dagger start\". Run this instead:\n\n  "+tc.start)
		})
	}
}

func TestUpHelpPointsToStart(t *testing.T) {
	var out bytes.Buffer
	upCmd.SetOut(&out)
	t.Cleanup(func() { upCmd.SetOut(nil) })

	require.NoError(t, upCmd.Help())
	require.Equal(t, upRenamedHelp, out.String())
}
