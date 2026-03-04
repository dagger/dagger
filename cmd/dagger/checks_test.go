package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseChecksTargetArgs(t *testing.T) {
	t.Run("no separator keeps existing behavior", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"go:lint"}, -1)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"go:lint"}, patterns)
	})

	t.Run("separator with explicit workspace and pattern", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"github.com/acme/ws", "go:lint"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Equal(t, []string{"go:lint"}, patterns)
	})

	t.Run("separator with explicit workspace only", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"github.com/acme/ws"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Empty(t, patterns)
	})

	t.Run("separator without workspace", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"go:lint"}, 0)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"go:lint"}, patterns)
	})

	t.Run("separator with too many pre-target args errors", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"a", "b", "go:lint"}, 2)
		require.ErrorContains(t, err, "expected at most one workspace target before --")
		require.Nil(t, workspaceRef)
		require.Nil(t, patterns)
	})
}
