package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceCommitStagePaths(t *testing.T) {
	require.Equal(t, []string{":literal", "a/file", "b", "gone/file"}, commitStagePaths(&ChangesetPaths{
		Added:      []string{"a/", "a/file", "empty/", ":literal"},
		Modified:   []string{"b", "a/file"},
		AllRemoved: []string{"gone/", "gone/file"},
	}))
	require.True(t, commitPathSelected("a/file", []string{"a"}))
	require.True(t, commitPathSelected("anything", nil))
	require.True(t, commitPathSelected("anything", []string{"."}))
	require.False(t, commitPathSelected("ab/file", []string{"a"}))
	require.False(t, commitPathSelected("a/file", []string{"a/*"}))
	require.True(t, commitPathSelected("a/*", []string{"a/*"}))
}
