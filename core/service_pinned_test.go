package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServicePinned(t *testing.T) {
	ctx := context.Background()
	svc := &Service{Args: []string{"serve"}}

	pinned := svc.WithPinned()
	require.True(t, pinned.Pinned)
	require.False(t, svc.Pinned, "WithPinned must not mutate the receiver")

	t.Run("persisted round trip keeps the pin", func(t *testing.T) {
		enc, err := pinned.EncodePersistedObject(ctx, nil)
		require.NoError(t, err)
		decoded, err := (&Service{}).DecodePersistedObject(ctx, nil, enc.JSON)
		require.NoError(t, err)
		require.True(t, decoded.(*Service).Pinned)

		enc, err = svc.EncodePersistedObject(ctx, nil)
		require.NoError(t, err)
		require.NotContains(t, string(enc.JSON), "pinned")
		decoded, err = (&Service{}).DecodePersistedObject(ctx, nil, enc.JSON)
		require.NoError(t, err)
		require.False(t, decoded.(*Service).Pinned)
	})

	t.Run("withHostname drops the pin", func(t *testing.T) {
		// a new hostname is a different service instance nothing has started
		require.False(t, pinned.WithHostname("other").Pinned)
	})
}
