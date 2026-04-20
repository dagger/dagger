package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func newCoreDagqlServerForTest[T dagql.Typed](t testing.TB, root T) *dagql.Server {
	t.Helper()
	srv, err := dagql.NewServer(t.Context(), root)
	require.NoError(t, err)
	return srv
}
