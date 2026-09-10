package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceEntrypointArguments(t *testing.T) {
	previousUnset := workspaceEntrypointUnset
	t.Cleanup(func() { workspaceEntrypointUnset = previousUnset })
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
			workspaceEntrypointUnset = tc.unset
			err := workspaceEntrypointCmd.Args(workspaceEntrypointCmd, tc.args)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWorkspaceEntrypointRejectsEnvWrites(t *testing.T) {
	previousEnv, previousUnset := workspaceEnv, workspaceEntrypointUnset
	t.Cleanup(func() { workspaceEnv, workspaceEntrypointUnset = previousEnv, previousUnset })
	workspaceEnv = "test"
	workspaceEntrypointUnset = false
	require.ErrorContains(t, workspaceEntrypointCmd.RunE(workspaceEntrypointCmd, []string{"tools"}), "entrypoints are set in the base workspace config")
	workspaceEntrypointUnset = true
	require.ErrorContains(t, workspaceEntrypointCmd.RunE(workspaceEntrypointCmd, nil), "entrypoints are set in the base workspace config")
}
