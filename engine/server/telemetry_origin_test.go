package server

import (
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestTelemetryOriginScopeSessionIsolation(t *testing.T) {
	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "test", nil, nil)
	defer lease.Release()
	scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: "other", ClientID: "child"}, lease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: "session", ClientID: "parent"})
	require.Empty(t, telemetryOriginClientID(ctx, "session"), "mismatched scope must not fall back to host metadata")
	require.Equal(t, "child", telemetryOriginClientID(ctx, "other"))

	ctx = engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{SessionID: "session", ClientID: "parent"})
	require.Equal(t, "parent", telemetryOriginClientID(ctx, "session"), "unscoped emission retains metadata routing")
	require.Empty(t, telemetryOriginClientID(ctx, "other"))
}
