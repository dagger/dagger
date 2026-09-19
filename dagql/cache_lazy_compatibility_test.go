package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLazyOperationCompatibilityCut(t *testing.T) {
	_, err := validateValueBundle(ValueBundle{Version: 1})
	require.ErrorContains(t, err, "unsupported value bundle version 1")
	_, err = decodePersistedResultEnvelope(t.Context(), nil, PersistedResultEnvelope{Version: 4}, true)
	require.ErrorContains(t, err, "unsupported version 4")
}
