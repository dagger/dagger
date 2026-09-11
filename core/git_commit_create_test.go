package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitCommitStagePaths(t *testing.T) {
	require.Equal(t, []string{":literal", "a/file", "b", "gone/file"}, commitStagePaths(&ChangesetPaths{
		Added:      []string{"a/", "a/file", "empty/", ":literal"},
		Modified:   []string{"b", "a/file"},
		AllRemoved: []string{"gone/", "gone/file"},
	}))
}
