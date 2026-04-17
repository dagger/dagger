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
	oldLoad := sessionLoadWorkspaceModules
	oldSkip := sessionSkipWorkspaceModules
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		sessionVersion = oldVersion
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
	})

	sessionWorkspace = "github.com/acme/ws"
	sessionVersion = "v1.2.3"
	sessionLoadWorkspaceModules = true

	params, err := sessionClientParams("secret")
	require.NoError(t, err)

	require.Equal(t, "secret", params.SecretToken)
	require.Equal(t, "v1.2.3", params.Version)
	require.True(t, params.LoadWorkspaceModules)
	require.NotNil(t, params.Workspace)
	require.Equal(t, "github.com/acme/ws", *params.Workspace)
}

func TestSessionClientParamsRejectConflictingWorkspaceModuleFlags(t *testing.T) {
	oldLoad := sessionLoadWorkspaceModules
	oldSkip := sessionSkipWorkspaceModules
	t.Cleanup(func() {
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
	})

	sessionLoadWorkspaceModules = true
	sessionSkipWorkspaceModules = true

	_, err := sessionClientParams("secret")
	require.ErrorContains(t, err, "mutually exclusive")
}
