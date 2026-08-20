package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServicePersistencePreservesDaggerNesting(t *testing.T) {
	t.Parallel()

	tests := map[string]*Service{
		"legacy": {
			ExperimentalPrivilegedNesting: true,
		},
		"independent sessions": {
			DaggerNesting: DaggerNestingIndependentSessions,
		},
	}
	for name, original := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			encoded, err := original.EncodePersistedObject(context.Background(), nil)
			require.NoError(t, err)

			decodedAny, err := (&Service{}).DecodePersistedObject(
				context.Background(), nil, 0, nil, encoded.JSON,
			)
			require.NoError(t, err)
			decoded, ok := decodedAny.(*Service)
			require.True(t, ok)
			require.Equal(t, original.ExperimentalPrivilegedNesting, decoded.ExperimentalPrivilegedNesting)
			require.Equal(t, original.DaggerNesting, decoded.DaggerNesting)
		})
	}
}
