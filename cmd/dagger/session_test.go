package main

import (
	"testing"

	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/pflag"
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
	oldGlobalWorkspace := workspaceRef
	oldVersion := sessionVersion
	oldLoad := sessionLoadWorkspaceModules
	oldSkip := sessionSkipWorkspaceModules
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		workspaceRef = oldGlobalWorkspace
		sessionVersion = oldVersion
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
	})

	sessionWorkspace = "github.com/acme/ws"
	workspaceRef = ""
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

func TestInstallGlobalFlagsWorkspaceSelection(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)

	installGlobalFlags(flags)

	flag := flags.Lookup("workspace")
	require.NotNil(t, flag)
	require.Equal(t, "W", flag.Shorthand)
	require.True(t, flag.Hidden)
}

func TestApplyWorkspaceClientParams(t *testing.T) {
	oldGlobalWorkspace := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldGlobalWorkspace
	})

	workspaceRef = "github.com/acme/global"
	params := client.Params{}
	applyWorkspaceClientParams(&params)

	require.NotNil(t, params.Workspace)
	require.Equal(t, "github.com/acme/global", *params.Workspace)

	explicit := "github.com/acme/explicit"
	params = client.Params{Workspace: &explicit}
	applyWorkspaceClientParams(&params)

	require.NotNil(t, params.Workspace)
	require.Equal(t, "github.com/acme/explicit", *params.Workspace)
}

func TestSessionClientParamsGlobalWorkspace(t *testing.T) {
	oldWorkspace := sessionWorkspace
	oldGlobalWorkspace := workspaceRef
	oldVersion := sessionVersion
	oldLoad := sessionLoadWorkspaceModules
	oldSkip := sessionSkipWorkspaceModules
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		workspaceRef = oldGlobalWorkspace
		sessionVersion = oldVersion
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
	})

	sessionWorkspace = ""
	workspaceRef = "github.com/acme/global"
	sessionVersion = ""
	sessionLoadWorkspaceModules = false
	sessionSkipWorkspaceModules = false

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
	t.Cleanup(func() {
		sessionWorkspace = oldWorkspace
		workspaceRef = oldGlobalWorkspace
		sessionLoadWorkspaceModules = oldLoad
		sessionSkipWorkspaceModules = oldSkip
	})

	sessionWorkspace = ""
	workspaceRef = ""
	sessionLoadWorkspaceModules = true
	sessionSkipWorkspaceModules = true

	_, err := sessionClientParams("secret")
	require.ErrorContains(t, err, "mutually exclusive")
}
