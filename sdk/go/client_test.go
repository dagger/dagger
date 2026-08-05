package dagger

import (
	"testing"

	"dagger.io/dagger/engineconn"
	"github.com/stretchr/testify/require"
)

// TestWithWorkspace verifies that the public Go SDK option stores the opaque
// workspace ref in engine connection config.
func TestWithWorkspace(t *testing.T) {
	t.Parallel()

	cfg := &engineconn.Config{}
	WithWorkspace("github.com/acme/ws").setClientOpt(cfg)

	require.Equal(t, "github.com/acme/ws", cfg.Workspace)
}

func TestWithLoadWorkspaceModules(t *testing.T) {
	t.Parallel()

	cfg := &engineconn.Config{}
	WithLoadWorkspaceModules().setClientOpt(cfg)

	require.True(t, cfg.LoadWorkspaceModules)
}
