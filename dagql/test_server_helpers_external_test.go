package dagql_test

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func newExternalDagqlServerForTest[T dagql.Typed](t testing.TB, root T) *dagql.Server {
	t.Helper()
	srv, err := dagql.NewServer(t.Context(), root)
	require.NoError(t, err)
	return srv
}
