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

	_, err = store.LookupEnvironmentByName(ctx, "notexist")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrEnvironmentNotExist))

	st := &EnvironmentState{
		Name: "test",
	}
	require.NoError(t, store.CreateEnvironment(ctx, st))

	checkEnvironments := func(store *Store) {
		r, err := store.LookupEnvironmentByID(ctx, st.ID)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, "test", r.Name)

		r, err = store.LookupEnvironmentByName(ctx, "test")
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, "test", r.Name)

		environments, err := store.ListEnvironments(ctx)
		require.NoError(t, err)
		require.Len(t, environments, 1)
		require.Equal(t, "test", environments[0].Name)
	}

	checkEnvironments(store)

	// Reload the environments from disk and check again
	newStore, err := NewStore(root)
	require.NoError(t, err)
	checkEnvironments(newStore)
}

func TestStoreLookupByPath(t *testing.T) {
	ctx := context.TODO()

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	st := &EnvironmentState{
		Name: "test",
	}
	require.NoError(t, st.SetInput("foo", DirInput("/test/path", []string{})))
	require.NoError(t, store.CreateEnvironment(ctx, st))

	// Lookup by path
	environments, err := store.LookupEnvironmentByPath(ctx, "/test/path")
	require.NoError(t, err)
	require.Len(t, environments, 1)
	require.Equal(t, st.ID, environments[0].ID)

	// Add a new path
	require.NoError(t, st.SetInput("bar", DirInput("/test/anotherpath", []string{})))
	require.NoError(t, store.UpdateEnvironment(ctx, st, nil))

	// Lookup by the previous path
	environments, err = store.LookupEnvironmentByPath(ctx, "/test/path")
	require.NoError(t, err)
	require.Len(t, environments, 1)
	require.Equal(t, st.ID, environments[0].ID)

	// Lookup by the new path
	environments, err = store.LookupEnvironmentByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
	require.Len(t, environments, 1)
	require.Equal(t, st.ID, environments[0].ID)

	// Remove a path
	require.NoError(t, st.RemoveInputs("foo"))
	require.NoError(t, store.UpdateEnvironment(ctx, st, nil))

	// Lookup by the removed path should fail
	environments, err = store.LookupEnvironmentByPath(ctx, "/test/path")
	require.NoError(t, err)
	require.Len(t, environments, 0)

	// Lookup by the other path should still work
	environments, err = store.LookupEnvironmentByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
	require.Len(t, environments, 1)

	// Add another environment using the same path
	otherSt := &EnvironmentState{
		Name: "test2",
	}
	require.NoError(t, otherSt.SetInput("foo", DirInput("/test/anotherpath", []string{})))
	require.NoError(t, store.CreateEnvironment(ctx, otherSt))

	// Lookup by path should return both environments
	environments, err = store.LookupEnvironmentByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
	require.Len(t, environments, 2)

	// Remove the first environment. Lookup by path should still return the
	// second environment.
	require.NoError(t, store.DeleteEnvironment(ctx, st, nil))
	environments, err = store.LookupEnvironmentByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
	require.Len(t, environments, 1)
	require.Equal(t, otherSt.ID, environments[0].ID)
}
