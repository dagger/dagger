package daggercmd

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

func TestSessionCmdAllowHostPortsFlag(t *testing.T) {
	cmd := sessionCmd()

	flag := cmd.Flags().Lookup("allow-host-ports")
	require.NotNil(t, flag)
}

// TestSessionClientParamsWorkspace verifies that the session command forwards
// its explicit workspace selection into engine client params.
func TestSessionClientParamsWorkspace(t *testing.T) {
	oldWorkspace := sessionWorkspace
	oldGlobalWorkspace := workspaceRef
	oldVersion := sessionVersion
	oldLoad := sessionLoadWorkspaceModules
	oldSkip := sessionSkipWorkspaceModules
	oldAllowedHostPorts := sessionAllowedHostPorts
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		workspaceRef = oldGlobalWorkspace
		sessionVersion = oldVersion
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
		sessionAllowedHostPorts = oldAllowedHostPorts
	})

	sessionWorkspace = "github.com/acme/ws"
	workspaceRef = ""
	sessionVersion = "v1.2.3"
	sessionLoadWorkspaceModules = true
	sessionAllowedHostPorts = []string{"local", "github.com/acme/mod@v1.2.3"}

	params, err := sessionClientParams("secret")
	require.NoError(t, err)

	require.Equal(t, "secret", params.SecretToken)
	require.Equal(t, "v1.2.3", params.Version)
	require.True(t, params.LoadWorkspaceModules)
	require.NotNil(t, params.Workspace)
	require.Equal(t, "github.com/acme/ws", *params.Workspace)
	require.Equal(t, []string{"local", "github.com/acme/mod@v1.2.3"}, params.AllowedHostPortModules)
}

func TestSessionClientParamsGlobalWorkspace(t *testing.T) {
	oldWorkspace := sessionWorkspace
	oldGlobalWorkspace := workspaceRef
	oldVersion := sessionVersion
	oldLoad := sessionLoadWorkspaceModules
	oldSkip := sessionSkipWorkspaceModules
	oldAllowedHostPorts := sessionAllowedHostPorts
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		workspaceRef = oldGlobalWorkspace
		sessionVersion = oldVersion
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
		sessionAllowedHostPorts = oldAllowedHostPorts
	})

	sessionWorkspace = ""
	workspaceRef = "github.com/acme/global"
	sessionVersion = ""
	sessionLoadWorkspaceModules = false
	sessionSkipWorkspaceModules = false
	sessionAllowedHostPorts = nil

	params, err := sessionClientParams("secret")
	require.NoError(t, err)

	require.NotNil(t, params.Workspace)
	require.Equal(t, "github.com/acme/global", *params.Workspace)
}

func TestSessionClientParamsRejectConflictingWorkspaceModuleFlags(t *testing.T) {
	oldWorkspace := sessionWorkspace
	oldGlobalWorkspace := workspaceRef
	oldLoad := sessionLoadWorkspaceModules
	oldSkip := sessionSkipWorkspaceModules
	oldAllowedHostPorts := sessionAllowedHostPorts
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		workspaceRef = oldGlobalWorkspace
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
		sessionAllowedHostPorts = oldAllowedHostPorts
	})

	sessionWorkspace = ""
	workspaceRef = ""
	sessionLoadWorkspaceModules = true
	sessionSkipWorkspaceModules = true
	sessionAllowedHostPorts = nil

	_, err := sessionClientParams("secret")
	require.ErrorContains(t, err, "mutually exclusive")
}
