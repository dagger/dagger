package state

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspace(t *testing.T) {
	ctx := context.TODO()

	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)

	// Open should fail since the directory is not initialized
	_, err = Open(ctx, root)
	require.ErrorIs(t, ErrNotInit, err)

	// Init
	workspace, err := Init(ctx, root)
	require.NoError(t, err)
	require.Equal(t, root, workspace.Path)

	// Create
	st, err := workspace.Create(ctx, "test")
	require.NoError(t, err)
	require.Equal(t, "test", st.Name)

	// Open
	workspace, err = Open(ctx, root)
	require.NoError(t, err)
	require.Equal(t, root, workspace.Path)

	// List
	envs, err := workspace.List(ctx)
	require.NoError(t, err)
	require.Len(t, envs, 1)
	require.Equal(t, "test", envs[0].Name)

	// Get
	env, err := workspace.Get(ctx, "test")
	require.NoError(t, err)
	require.Equal(t, "test", env.Name)

	// Save
	require.NoError(t, env.SetInput("foo", TextInput("bar")))
	require.NoError(t, workspace.Save(ctx, env))
	workspace, err = Open(ctx, root)
	require.NoError(t, err)
	env, err = workspace.Get(ctx, "test")
	require.NoError(t, err)
	require.Contains(t, env.Inputs, "foo")
}
