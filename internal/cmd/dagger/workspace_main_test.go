package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceMainArguments(t *testing.T) {
	require.True(t, workspaceMainCmd.HasAlias("entrypoint"))
	previousUnset := workspaceMainUnset
	t.Cleanup(func() { workspaceMainUnset = previousUnset })
	for _, tc := range []struct {
		name  string
		unset bool
		args  []string
		err   string
	}{
		{name: "get"},
		{name: "set", args: []string{"tools"}},
		{name: "unset", unset: true},
		{name: "name and unset", unset: true, args: []string{"tools"}, err: "--unset cannot be used with NAME"},
		{name: "extra names", args: []string{"a", "b"}, err: "accepts at most 1 arg"},
		{name: "empty name", args: []string{""}, err: "NAME must be an installed module name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspaceMainUnset = tc.unset
			err := workspaceMainCmd.Args(workspaceMainCmd, tc.args)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWorkspaceMainRejectsEnvWrites(t *testing.T) {
	previousEnv, previousUnset := workspaceEnv, workspaceMainUnset
	t.Cleanup(func() { workspaceEnv, workspaceMainUnset = previousEnv, previousUnset })
	workspaceEnv = "test"
	workspaceMainUnset = false
	require.ErrorContains(t, workspaceMainCmd.RunE(workspaceMainCmd, []string{"tools"}), "it is set in the base workspace config")
	workspaceMainUnset = true
	require.ErrorContains(t, workspaceMainCmd.RunE(workspaceMainCmd, nil), "it is set in the base workspace config")
}
