package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceClientUnavailableError(t *testing.T) {
	t.Parallel()
	err := error(&SourceClientUnavailableError{ClientID: "source-client"})

	var unavailable *SourceClientUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, "source-client", unavailable.ClientID)
	require.EqualError(t, err, `source client "source-client" is unavailable`)
}
