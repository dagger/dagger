package dagger

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	ctx := context.TODO()

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)
	store := NewStore(root)

	_, err = store.LookupRoute(ctx, "notexist", nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	r, err := store.CreateRoute(ctx, "test", nil)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "test", r.Name())

	r, err = store.LookupRoute(ctx, "test", nil)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "test", r.Name())

	routes, err := store.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, "test", routes[0])
}
