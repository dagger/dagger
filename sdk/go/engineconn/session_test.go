package engineconn

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLISessionArgsIncludeWorkspace(t *testing.T) {
	t.Parallel()

	args := cliSessionArgs(&Config{
		Workspace: "github.com/acme/ws",
	})

	require.Contains(t, args, "--workspace")
	require.Contains(t, args, "github.com/acme/ws")
}

func TestGetRejectsWorkspaceForExistingSession(t *testing.T) {
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		Workspace: "github.com/acme/ws",
	})
	require.ErrorContains(t, err, "cannot configure workspace for existing session")
}
