package dagql_test

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// Selecting a nullable field into a Nullable[T] destination is what callers do
// when they need to tell "absent" apart from the zero value — core/sdkmodule's
// Provider.FindClientRoot, for one.
func TestSelectIntoNullableDestination(t *testing.T) {
	srv := newExternalDagqlServerForTest(t, Query{})
	dagql.Fields[Query]{
		dagql.Func("maybeRoot", func(_ context.Context, _ Query, args struct {
			Present dagql.Boolean
		}) (dagql.Nullable[dagql.String], error) {
			if !args.Present.Bool() {
				return dagql.Null[dagql.String](), nil
			}
			return dagql.NonNull(dagql.NewString("/client/root")), nil
		}),
	}.Install(srv)

	ctx := dagql.ContextWithCache(testContext(), newCache(t))
	sel := func(present bool) dagql.Selector {
		return dagql.Selector{
			Field: "maybeRoot",
			Args:  []dagql.NamedInput{{Name: "present", Value: dagql.NewBoolean(present)}},
		}
	}

	t.Run("non-null", func(t *testing.T) {
		var got dagql.Nullable[dagql.String]
		require.NoError(t, srv.Select(ctx, srv.Root(), &got, sel(true)))
		require.True(t, got.Valid)
		require.Equal(t, "/client/root", got.Value.String())
	})

	t.Run("null", func(t *testing.T) {
		var got dagql.Nullable[dagql.String]
		require.NoError(t, srv.Select(ctx, srv.Root(), &got, sel(false)))
		require.False(t, got.Valid)
	})
}
