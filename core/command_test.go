package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestContainerShellDefaults(t *testing.T) {
	ctr := &Container{}
	require.Equal(t, []string{"sh"}, ctr.Shell(false).Args)
	require.Equal(t, []string{"sh", "-c"}, ctr.Shell(true).Args)

	ctr.DefaultTerminalCmd = DefaultTerminalCmdOpts{
		Args:                          []string{"python"},
		ExperimentalPrivilegedNesting: dagql.Opt(dagql.Boolean(true)),
		InsecureRootCapabilities:      dagql.Opt(dagql.Boolean(true)),
	}
	require.Equal(t, []string{"python", "-c"}, ctr.Shell(true).Args)
	require.True(t, ctr.Shell(true).PrivilegedNesting)
	require.True(t, ctr.Shell(true).InsecureRootCapabilities)

	ctr.DefaultTerminalCmd.Batch = []string{"python", "-I", "-c"}
	shell := ctr.Shell(true)
	require.Equal(t, []string{"python", "-I", "-c"}, shell.Args)
	shell.Args[0] = "changed"
	require.Equal(t, []string{"python", "-I", "-c"}, ctr.Shell(true).Args)
	require.Equal(t, []string{"python"}, ctr.Shell(false).Args)

	inherited := ctr.WithTerminalDefaults(TerminalArgs{})
	require.Equal(t, []string{"python"}, inherited.Cmd)
	require.True(t, bool(inherited.ExperimentalPrivilegedNesting.Value))
	require.True(t, bool(inherited.InsecureRootCapabilities.Value))
	overridden := ctr.WithTerminalDefaults(TerminalArgs{
		ExperimentalPrivilegedNesting: dagql.Opt(dagql.Boolean(false)),
		InsecureRootCapabilities:      dagql.Opt(dagql.Boolean(false)),
	})
	require.False(t, bool(overridden.ExperimentalPrivilegedNesting.Value))
	require.False(t, bool(overridden.InsecureRootCapabilities.Value))
}
