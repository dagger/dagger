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

	_, err = store.LookupDeploymentByName(ctx, "notexist")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrDeploymentNotExist))

	st := &DeploymentState{
		Name: "test",
	}
	require.NoError(t, store.CreateDeployment(ctx, st))

	checkDeployments := func(store *Store) {
		r, err := store.LookupDeploymentByID(ctx, st.ID)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, "test", r.Name)

		r, err = store.LookupDeploymentByName(ctx, "test")
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, "test", r.Name)

		deployments, err := store.ListDeployments(ctx)
		require.NoError(t, err)
		require.Len(t, deployments, 1)
		require.Equal(t, "test", deployments[0].Name)
	}

	checkDeployments(store)

	// Reload the deployments from disk and check again
	newStore, err := NewStore(root)
	require.NoError(t, err)
	checkDeployments(newStore)
}

func TestStoreLookupByPath(t *testing.T) {
	ctx := context.TODO()

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	st := &DeploymentState{
		Name: "test",
	}
	require.NoError(t, st.SetInput("foo", DirInput("/test/path", []string{})))
	require.NoError(t, store.CreateDeployment(ctx, st))

	// Lookup by path
	r, err := store.LookupDeploymentByPath(ctx, "/test/path")
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, st.ID, r.ID)

	// Add a new path
	require.NoError(t, st.SetInput("bar", DirInput("/test/anotherpath", []string{})))
	require.NoError(t, store.UpdateDeployment(ctx, st, nil))

	// Lookup by the previous path
	r, err = store.LookupDeploymentByPath(ctx, "/test/path")
	require.NoError(t, err)
	require.Equal(t, st.ID, r.ID)

	// Lookup by the new path
	r, err = store.LookupDeploymentByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
	require.Equal(t, st.ID, r.ID)

	// Remove a path
	require.NoError(t, st.RemoveInputs("foo"))
	require.NoError(t, store.UpdateDeployment(ctx, st, nil))

	// Lookup by the removed path should fail
	_, err = store.LookupDeploymentByPath(ctx, "/test/path")
	require.Error(t, err)

	// Lookup by the other path should still work
	_, err = store.LookupDeploymentByPath(ctx, "/test/anotherpath")
	require.NoError(t, err)
}
