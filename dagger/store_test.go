package dagger

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreLoad(t *testing.T) {
	ctx := context.TODO()

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	_, err = store.LookupRouteByName(ctx, "notexist")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrRouteNotExist))

	st := &RouteState{
		Name: "test",
	}
	require.NoError(t, store.CreateRoute(ctx, st))

	checkRoutes := func(store *Store) {
		r, err := store.LookupRouteByID(ctx, st.ID)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, "test", r.Name)

		r, err = store.LookupRouteByName(ctx, "test")
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, "test", r.Name)

		routes, err := store.ListRoutes(ctx)
		require.NoError(t, err)
		require.Len(t, routes, 1)
		require.Equal(t, "test", routes[0].Name)
	}

	checkRoutes(store)

	// Reload the routes from disk and check again
	newStore, err := NewStore(root)
	require.NoError(t, err)
	checkRoutes(newStore)
}

func TestStoreLookupByPath(t *testing.T) {
	ctx := context.TODO()

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	st := &RouteState{
		Name: "test",
	}
	require.NoError(t, st.AddInput("foo", DirInput("/test/path", []string{})))
	require.NoError(t, store.CreateRoute(ctx, st))

	// Lookup by path
	r, err := store.LookupRouteByPath(ctx, "/test/path")
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, st.ID, r.ID)

	// Add a new path
	require.NoError(t, st.AddInput("bar", DirInput("/test/anotherpath", []string{})))
	require.NoError(t, store.UpdateRoute(ctx, st, nil))

	// Lookup by the previous path
	r, err = store.LookupRouteByPath(ctx, "/test/path")
	require.NoError(t, err)
	require.Equal(t, st.ID, r.ID)

	// Lookup by the new path
	r, err = store.LookupRouteByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
	require.Equal(t, st.ID, r.ID)

	// Remove a path
	require.NoError(t, st.RemoveInputs("foo"))
	require.NoError(t, store.UpdateRoute(ctx, st, nil))

	// Lookup by the removed path should fail
	_, err = store.LookupRouteByPath(ctx, "/test/path")
	require.Error(t, err)

	// Lookup by the other path should still work
	_, err = store.LookupRouteByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
}
