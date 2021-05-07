package state

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	ctx := context.TODO()

	// Init
	root, err := os.MkdirTemp(os.TempDir(), "dagger-*")
	require.NoError(t, err)
	st, err := Init(ctx, root, "test")
	require.Equal(t, "test", st.Name)
	require.Equal(t, root, st.Path)
	require.NoError(t, err)

	// Open
	_, err = Open(ctx, "/tmp/not/exist")
	require.Error(t, err)
	require.ErrorIs(t, ErrNotInit, err)

	st, err = Open(ctx, root)
	require.NoError(t, err)
	require.Equal(t, "test", st.Name)
	require.Equal(t, root, st.Path)

	// Save
	computed := `{"hello": "world"}`
	st.Computed = computed
	require.NoError(t, Save(ctx, st))
	st, err = Open(ctx, root)
	require.NoError(t, err)
	require.Equal(t, computed, st.Computed)
}
