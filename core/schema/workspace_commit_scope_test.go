package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

// TestCommitRemovedPathsInScope covers the composition scopeChangesetToPaths
// relies on: AllRemoved is scoped first and only then collapsed. Collapsing
// first would let a path-scoped commit remove a whole directory when only one
// file beneath it was in scope.
func TestCommitRemovedPathsInScope(t *testing.T) {
	// What a removed "internal/buildkit" holding two files looks like in
	// AllRemoved: the directory plus every path beneath it.
	allRemoved := []string{
		"internal/buildkit/",
		"internal/buildkit/AUTHORS",
		"internal/buildkit/client/",
		"internal/buildkit/client/client.go",
		"other.txt",
	}

	for _, tc := range []struct {
		name     string
		resolved []string
		want     []string
	}{
		{
			name:     "unscoped collapses to the directory",
			resolved: nil,
			want:     []string{"internal/buildkit/", "other.txt"},
		},
		{
			name:     "scoped to the directory collapses to the directory",
			resolved: []string{"internal/buildkit"},
			want:     []string{"internal/buildkit/"},
		},
		{
			// The directory itself is out of scope, so it must not be
			// removed - only the one file the commit asked for.
			name:     "scoped to one file keeps just that file",
			resolved: []string{"internal/buildkit/AUTHORS"},
			want:     []string{"internal/buildkit/AUTHORS"},
		},
		{
			name:     "scoped to a subdirectory collapses to that subdirectory",
			resolved: []string{"internal/buildkit/client"},
			want:     []string{"internal/buildkit/client/"},
		},
		{
			name:     "scope matching nothing removes nothing",
			resolved: []string{"docs"},
			want:     nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := core.CollapseChildPaths(commitPathsInScope(allRemoved, tc.resolved))
			require.Equal(t, tc.want, got)
		})
	}
}
