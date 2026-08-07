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

func TestDaggerNestingEnvironmentValidation(t *testing.T) {
	t.Run("unknown marker", func(t *testing.T) {
		t.Setenv("DAGGER_NESTING", "UNKNOWN")
		_, err := Connect(t.Context(), Params{})
		require.ErrorContains(t, err, "unknown DAGGER_NESTING")
	})

	for _, mode := range []string{"NESTED_CLIENT", "INDEPENDENT_SESSIONS"} {
		t.Run("missing port "+mode, func(t *testing.T) {
			t.Setenv("DAGGER_NESTING", mode)
			_, err := Connect(t.Context(), Params{})
			require.ErrorContains(t, err, "requires DAGGER_SESSION_PORT")
		})
		t.Run("invalid port "+mode, func(t *testing.T) {
			t.Setenv("DAGGER_NESTING", mode)
			t.Setenv("DAGGER_SESSION_PORT", "0")
			_, err := Connect(t.Context(), Params{})
			require.ErrorContains(t, err, "requires a positive DAGGER_SESSION_PORT")
		})
	}

	t.Run("independent does not require inherited token", func(t *testing.T) {
		t.Setenv("DAGGER_NESTING", "INDEPENDENT_SESSIONS")
		t.Setenv("DAGGER_SESSION_PORT", "1")
		t.Setenv("DAGGER_SESSION_TOKEN", "")
		_, err := Connect(t.Context(), Params{})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "DAGGER_SESSION_TOKEN")
	})
}
