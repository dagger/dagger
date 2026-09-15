package engineconn

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCLISessionArgsIncludeWorkspace verifies that session provisioning forwards
// workspace selection as a dedicated CLI flag.
func TestCLISessionArgsIncludeWorkspace(t *testing.T) {
	t.Parallel()

	args := cliSessionArgs(&Config{
		Workspace: "github.com/acme/ws",
	})

	require.Contains(t, args, "--workspace")
	require.Contains(t, args, "github.com/acme/ws")
}

func TestCLISessionArgsIncludeLoadWorkspaceModules(t *testing.T) {
	t.Parallel()

	args := cliSessionArgs(&Config{
		LoadWorkspaceModules: true,
	})

	require.Contains(t, args, "--load-workspace-modules")
	require.NotContains(t, args, "--skip-workspace-modules")
}

// TestGetRejectsWorkspaceForExistingSession verifies that an existing session's
// workspace binding cannot be overridden by client config.
func TestGetRejectsWorkspaceForExistingSession(t *testing.T) {
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		Workspace: "github.com/acme/ws",
	})
	require.ErrorContains(t, err, "cannot configure workspace for existing session")
}

func TestGetRejectsWorkspaceModuleLoadingForExistingSession(t *testing.T) {
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		LoadWorkspaceModules: true,
	})
	require.ErrorContains(t, err, "cannot configure workspace module loading for existing session")
}
