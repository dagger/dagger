package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestTelemetryContextUsesClientLifetime(t *testing.T) {
	t.Parallel()

	internalCtx, cancelInternal := context.WithCancelCause(context.Background())
	client := &Client{internalCtx: internalCtx}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	telemetryCtx, cancelTelemetry := client.telemetryContext(requestCtx)
	defer cancelTelemetry(context.Canceled)

	cancelRequest()
	select {
	case <-telemetryCtx.Done():
		t.Fatal("request cancellation interrupted client telemetry drain")
	case <-time.After(10 * time.Millisecond):
	}

	clientClosed := errors.New("client initialization failed")
	cancelInternal(clientClosed)
	select {
	case <-telemetryCtx.Done():
		require.ErrorIs(t, context.Cause(telemetryCtx), clientClosed)
	case <-time.After(time.Second):
		t.Fatal("client lifetime cancellation did not stop telemetry")
	}
}

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
