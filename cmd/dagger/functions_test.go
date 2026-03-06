package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFunctionsTargetArgs(t *testing.T) {
	t.Run("no separator keeps function args", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"container", "from"}, -1)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"container", "from"}, functions)
	})

	t.Run("separator with explicit workspace and function args", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"github.com/acme/ws", "container", "from"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Equal(t, []string{"container", "from"}, functions)
	})

	t.Run("separator with explicit workspace only", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"github.com/acme/ws"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Empty(t, functions)
	})

	t.Run("separator without workspace", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"container", "from"}, 0)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"container", "from"}, functions)
	})

	t.Run("too many args before separator", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"a", "b", "container"}, 2)
		require.ErrorContains(t, err, "expected at most one workspace target before --")
		require.Nil(t, workspaceRef)
		require.Nil(t, functions)
	})

	t.Run("no separator infers workspace for URL-like first arg", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"https://github.com/dagger/dagger", "container"}, -1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Equal(t, []string{"container"}, functions)
	})
}
