package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCallTargetArgs(t *testing.T) {
	t.Run("keeps function path when first arg is not workspace-like", func(t *testing.T) {
		workspaceRef, functionPath, err := parseCallTargetArgs([]string{"container", "from"}, -1)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"container", "from"}, functionPath)
	})

	t.Run("infers workspace for URL-like first arg", func(t *testing.T) {
		workspaceRef, functionPath, err := parseCallTargetArgs([]string{"https://github.com/dagger/dagger", "container", "from"}, -1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Equal(t, []string{"container", "from"}, functionPath)
	})

	t.Run("separator form with explicit workspace", func(t *testing.T) {
		workspaceRef, functionPath, err := parseCallTargetArgs([]string{"https://github.com/dagger/dagger", "container", "from"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Equal(t, []string{"container", "from"}, functionPath)
	})
}

func TestStripHelpArgs(t *testing.T) {
	require.Equal(t, []string{"https://github.com/dagger/dagger"}, stripHelpArgs([]string{"https://github.com/dagger/dagger", "--help"}))
	require.Equal(t, []string{"container", "from"}, stripHelpArgs([]string{"container", "-h", "from"}))
}
