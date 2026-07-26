package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func touchedSet(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestMergeOverlaySearchResults(t *testing.T) {
	res := func(file string, line int) *core.SearchResult {
		return &core.SearchResult{FilePath: file, LineNumber: line}
	}

	t.Run("overlay replaces host results per file", func(t *testing.T) {
		host := []*core.SearchResult{
			res("untouched.txt", 1),
			res("edited.txt", 3), // stale host content
			res("doomed.txt", 2), // removed by the overlay
		}
		overlay := []*core.SearchResult{
			res("edited.txt", 5),
			res("created.txt", 1),
		}
		merged := mergeOverlaySearchResults(host, overlay,
			touchedSet("edited.txt", "doomed.txt", "created.txt"), nil)

		require.Equal(t, []*core.SearchResult{
			res("created.txt", 1),
			res("edited.txt", 5),
			res("untouched.txt", 1),
		}, merged)
	})

	t.Run("touched file with no overlay matches disappears", func(t *testing.T) {
		host := []*core.SearchResult{res("edited.txt", 1)}
		merged := mergeOverlaySearchResults(host, nil, touchedSet("edited.txt"), nil)
		require.Empty(t, merged)
	})

	t.Run("sorted by file then line", func(t *testing.T) {
		host := []*core.SearchResult{res("b.txt", 9), res("b.txt", 2)}
		overlay := []*core.SearchResult{res("a.txt", 4)}
		merged := mergeOverlaySearchResults(host, overlay, touchedSet("a.txt"), nil)
		require.Equal(t, []*core.SearchResult{
			res("a.txt", 4),
			res("b.txt", 2),
			res("b.txt", 9),
		}, merged)
	})

	t.Run("limit caps the merged set", func(t *testing.T) {
		host := []*core.SearchResult{res("a.txt", 1), res("b.txt", 1)}
		overlay := []*core.SearchResult{res("c.txt", 1)}
		limit := 2
		merged := mergeOverlaySearchResults(host, overlay, touchedSet("c.txt"), &limit)
		require.Equal(t, []*core.SearchResult{res("a.txt", 1), res("b.txt", 1)}, merged)
	})
}

func TestSearchPathInScopes(t *testing.T) {
	require.True(t, searchPathInScopes("docs/new.md", []string{"docs"}))
	require.True(t, searchPathInScopes("docs/new.md", []string{"docs/new.md"}))
	require.True(t, searchPathInScopes("docs/new.md", []string{"/docs"}))
	require.True(t, searchPathInScopes("docs/new.md", []string{"."}))
	require.True(t, searchPathInScopes("docs/new.md", []string{"src", "docs"}))
	require.False(t, searchPathInScopes("docs/new.md", []string{"src"}))
	require.False(t, searchPathInScopes("docs-extra/new.md", []string{"docs"}))
}

func TestMergeOverlayGlobMatches(t *testing.T) {
	t.Run("overlay replaces host matches per path", func(t *testing.T) {
		host := []string{"untouched.txt", "edited.txt", "doomed.txt"}
		overlay := []string{"edited.txt", "created.txt"}
		merged := mergeOverlayGlobMatches(host, overlay,
			touchedSet("edited.txt", "doomed.txt", "created.txt"))
		require.Equal(t, []string{"created.txt", "edited.txt", "untouched.txt"}, merged)
	})

	t.Run("shared parent directories dedup across trees", func(t *testing.T) {
		// The delta root scaffolds parent directories of touched files, so a
		// directory can match in both trees; slash suffixes must not defeat
		// the dedup.
		host := []string{"sub/", "sub/inner.txt"}
		overlay := []string{"sub/", "sub/new.txt"}
		merged := mergeOverlayGlobMatches(host, overlay, touchedSet("sub/new.txt"))
		require.Equal(t, []string{"sub/", "sub/inner.txt", "sub/new.txt"}, merged)
	})

	t.Run("removed path with no overlay match disappears", func(t *testing.T) {
		merged := mergeOverlayGlobMatches([]string{"doomed.txt"}, nil, touchedSet("doomed.txt"))
		require.Empty(t, merged)
	})
}
