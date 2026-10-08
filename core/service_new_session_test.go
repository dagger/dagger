package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

func TestServicePersistsDaggerInDaggerNewSession(t *testing.T) {
	ctx := context.Background()
	for _, newSession := range []bool{false, true} {
		svc := &Service{Args: []string{"daemon"}, DaggerInDaggerNewSession: newSession}
		encoded, err := svc.EncodePersistedObject(ctx, nil)
		require.NoError(t, err)
		decoded, err := (&Service{}).DecodePersistedObject(ctx, nil, encoded.JSON)
		require.NoError(t, err)
		require.Equal(t, newSession, decoded.(*Service).DaggerInDaggerNewSession)
	}
}

func TestExecutionMetadataDaggerInDaggerNewSessionSerialization(t *testing.T) {
	// Execs without the option keep the serialized form, and so the cache
	// keys, they had before the field existed.
	unset, err := json.Marshal(engineutil.ExecutionMetadata{})
	require.NoError(t, err)
	require.NotContains(t, string(unset), "DaggerInDaggerNewSession")

	set, err := json.Marshal(engineutil.ExecutionMetadata{DaggerInDaggerNewSession: true})
	require.NoError(t, err)
	var decoded engineutil.ExecutionMetadata
	require.NoError(t, json.Unmarshal(set, &decoded))
	require.True(t, decoded.DaggerInDaggerNewSession)
}
