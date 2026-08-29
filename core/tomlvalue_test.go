package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTOMLValuePersistedObjectRoundTrip(t *testing.T) {
	t.Parallel()

	// contents() distinguishes a nil Source (no original text to preserve)
	// from an empty one, so the round trip must not conflate them.
	for name, val := range map[string]*TOMLValue{
		"with source":  {Data: []byte(`{"title":"x"}`), Source: []byte("title = \"x\" # keep\n")},
		"nil source":   {Data: []byte(`{"title":"x"}`)},
		"empty source": {Data: []byte("{}"), Source: []byte{}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			encoded, err := val.EncodePersistedObject(context.Background(), nil)
			require.NoError(t, err)

			decodedTyped, err := (&TOMLValue{}).DecodePersistedObject(context.Background(), nil, 0, nil, encoded.JSON)
			require.NoError(t, err)
			decoded, ok := decodedTyped.(*TOMLValue)
			require.True(t, ok)
			require.Equal(t, val, decoded)
			require.Equal(t, val.Source == nil, decoded.Source == nil)
		})
	}
}
