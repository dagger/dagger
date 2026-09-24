package daggercmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpReportsStartRename(t *testing.T) {
	root := testRootCommand()
	oldArgs, oldProgress, oldWorkspaceRef := os.Args, progress, workspaceRef
	t.Cleanup(func() {
		os.Args, progress, workspaceRef = oldArgs, oldProgress, oldWorkspaceRef
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
			cmd, args, err := root.Traverse(tc.cmdline)
			require.NoError(t, err)
			require.Same(t, upCmd, cmd)

			os.Args = append([]string{"/usr/local/bin/dagger"}, tc.cmdline...)
			require.EqualError(t, cmd.RunE(cmd, args),
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
