package client

import (
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestClientMetadataUsesExplicitModuleInsteadOfWorkspaceModules(t *testing.T) {
	t.Parallel()

	client := &Client{
		Params: Params{
			ID:                   "client",
			SessionID:            "session",
			SecretToken:          "secret",
			Module:               "./explicit",
			LoadWorkspaceModules: true,
		},
	}

	md := client.clientMetadata()

	require.False(t, md.LoadWorkspaceModules)
	require.Equal(t, []engine.ExtraModule{{
		Ref:        "./explicit",
		Entrypoint: true,
	}}, md.ExtraModules)
}

func TestClientMetadataForwardsWorkspaceModuleScopeOnlyWithWorkspaceModules(t *testing.T) {
	t.Parallel()

	client := &Client{
		Params: Params{
			ID:                   "client",
			SessionID:            "session",
			SecretToken:          "secret",
			LoadWorkspaceModules: true,
			WorkspaceModuleScope: "good-mod",
		},
	}

	md := client.clientMetadata()
	require.True(t, md.LoadWorkspaceModules)
	require.Equal(t, "good-mod", md.WorkspaceModuleScope)

	// With an explicit -m module there are no pending workspace modules to
	// narrow, so the scope must not travel.
	client.Params.Module = "./explicit"
	md = client.clientMetadata()
	require.False(t, md.LoadWorkspaceModules)
	require.Empty(t, md.WorkspaceModuleScope)
}
