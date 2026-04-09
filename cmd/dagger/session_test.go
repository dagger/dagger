package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSessionCmdWorkspaceFlag verifies that phase 1 exposes workspace selection
// directly on the hidden session command.
func TestSessionCmdWorkspaceFlag(t *testing.T) {
	cmd := sessionCmd()

	flag := cmd.Flags().Lookup("workspace")
	require.NotNil(t, flag)
	require.Equal(t, "W", flag.Shorthand)
}

// TestSessionClientParamsWorkspace verifies that the session command forwards
// its explicit workspace selection into engine client params.
func TestSessionClientParamsWorkspace(t *testing.T) {
	oldWorkspace := sessionWorkspace
	oldVersion := sessionVersion
	oldSkip := sessionSkipWorkspaceModules
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		sessionVersion = oldVersion
		sessionSkipWorkspaceModules = oldSkip
	})

	sessionWorkspace = "github.com/acme/ws"
	sessionVersion = "v1.2.3"
	sessionSkipWorkspaceModules = true

	params := sessionClientParams("secret")

	require.Equal(t, "secret", params.SecretToken)
	require.Equal(t, "v1.2.3", params.Version)
	require.True(t, params.SkipWorkspaceModules)
	require.NotNil(t, params.Workspace)
	require.Equal(t, "github.com/acme/ws", *params.Workspace)
}
