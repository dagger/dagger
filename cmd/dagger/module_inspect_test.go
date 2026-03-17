package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceLoadLocation(t *testing.T) {
	t.Run("nil workspace", func(t *testing.T) {
		require.Equal(t, ".", workspaceLoadLocation(nil))
	})

	t.Run("empty workspace", func(t *testing.T) {
		ref := ""
		require.Equal(t, ".", workspaceLoadLocation(&ref))
	})

	t.Run("explicit workspace", func(t *testing.T) {
		ref := "https://github.com/dagger/dagger"
		require.Equal(t, ref, workspaceLoadLocation(&ref))
	})
}
