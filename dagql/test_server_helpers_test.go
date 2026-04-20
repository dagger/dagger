package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newDagqlServerForTest[T Typed](t testing.TB, root T) *Server {
	t.Helper()
	srv, err := NewServer(t.Context(), root)
	require.NoError(t, err)
	return srv
}
