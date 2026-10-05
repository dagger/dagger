package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestGitPerClientInput(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{ClientID: "client", SessionID: "session"})
	resolve := func(ctx context.Context) dagql.Input {
		t.Helper()
		input, err := gitPerClientInput.Resolver(ctx, nil)
		require.NoError(t, err)
		return input
	}
	plain, err := dagql.PerClientInput.Resolver(ctx, nil)
	require.NoError(t, err)

	require.Equal(t, dagql.PerClientInput.Name, gitPerClientInput.Name)
	require.Equal(t, plain, resolve(ctx), "ordinary lookups keep their call digest")
	agent := resolve(core.WithAgentAddressResolution(ctx))
	require.NotEqual(t, plain, agent, "model-supplied addresses must not share the caller's lookups")
	require.Equal(t, agent, resolve(core.WithAgentAddressResolution(ctx)))
}
