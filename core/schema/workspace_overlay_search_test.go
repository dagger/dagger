package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func touchedSet(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestMergeSearchResults(t *testing.T) {
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
		merged := mergeSearchResults(host, overlay,
			touchedSet("edited.txt", "doomed.txt", "created.txt"), nil)

		require.Equal(t, []*core.SearchResult{
			res("created.txt", 1),
			res("edited.txt", 5),
			res("untouched.txt", 1),
		}, merged)
	})

	t.Run("touched file with no overlay matches disappears", func(t *testing.T) {
		host := []*core.SearchResult{res("edited.txt", 1)}
		merged := mergeSearchResults(host, nil, touchedSet("edited.txt"), nil)
		require.Empty(t, merged)
	})

	t.Run("sorted by file then line", func(t *testing.T) {
		host := []*core.SearchResult{res("b.txt", 9), res("b.txt", 2)}
		overlay := []*core.SearchResult{res("a.txt", 4)}
		merged := mergeSearchResults(host, overlay, touchedSet("a.txt"), nil)
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
		merged := mergeSearchResults(host, overlay, touchedSet("c.txt"), &limit)
		require.Equal(t, []*core.SearchResult{res("a.txt", 1), res("b.txt", 1)}, merged)
	})

	t.Run("mount points shadow source results beneath them", func(t *testing.T) {
		// The mounts merge uses Workspace.MountedPath as its predicate: any
		// source result at or under a mount point is replaced by the mounts
		// tree's view.
		ws := (&core.Workspace{}).WithMounted(dagql.ObjectResult[*core.Directory]{}, "deps/vendored")
		host := []*core.SearchResult{
			res("src/main.go", 1),
			res("deps/vendored/stale.go", 3),
		}
		mounted := []*core.SearchResult{res("deps/vendored/pinned.go", 7)}
		merged := mergeSearchResults(host, mounted, ws.MountedPath, nil)
		require.Equal(t, []*core.SearchResult{
			res("deps/vendored/pinned.go", 7),
			res("src/main.go", 1),
		}, merged)
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

func TestSearchSourcePaths(t *testing.T) {
	ctx := context.Background()
	s := &workspaceSchema{}
	ws := (&core.Workspace{}).WithMounted(dagql.ObjectResult[*core.Directory]{}, "deps/vendored")

	t.Run("no explicit paths pass through", func(t *testing.T) {
		paths, searchSource, err := s.searchSourcePaths(ctx, ws, nil)
		require.NoError(t, err)
		require.True(t, searchSource)
		require.Empty(t, paths)
	})

	t.Run("mount-covered paths are dropped from source operands", func(t *testing.T) {
		paths, searchSource, err := s.searchSourcePaths(ctx, ws,
			[]string{"src", "deps/vendored", "/deps/vendored/sub"})
		require.NoError(t, err)
		require.True(t, searchSource)
		require.Equal(t, []string{"src"}, paths)
	})

	t.Run("all paths mount-covered skips the source side", func(t *testing.T) {
		paths, searchSource, err := s.searchSourcePaths(ctx, ws,
			[]string{"deps/vendored", "deps/vendored/sub"})
		require.NoError(t, err)
		require.False(t, searchSource)
		require.Empty(t, paths)
	})

	t.Run("mount ancestor absent from the source is dropped", func(t *testing.T) {
		// "deps" exists in the workspace view only because the mount at
		// deps/vendored materializes its parents; this synthetic workspace has
		// no source content, so the ancestor must not reach ripgrep.
		paths, searchSource, err := s.searchSourcePaths(ctx, ws, []string{"deps"})
		require.NoError(t, err)
		require.False(t, searchSource)
		require.Empty(t, paths)
	})

	t.Run("paths uninvolved with mounts are kept verbatim", func(t *testing.T) {
		// A genuinely nonexistent path outside any mount still reaches the
		// source-side search, preserving today's error behavior.
		paths, searchSource, err := s.searchSourcePaths(ctx, ws, []string{"no/such/dir"})
		require.NoError(t, err)
		require.True(t, searchSource)
		require.Equal(t, []string{"no/such/dir"}, paths)
	})
}

func TestMergeGlobMatches(t *testing.T) {
	t.Run("overlay replaces host matches per path", func(t *testing.T) {
		host := []string{"untouched.txt", "edited.txt", "doomed.txt"}
		overlay := []string{"edited.txt", "created.txt"}
		merged := mergeGlobMatches(host, overlay,
			touchedSet("edited.txt", "doomed.txt", "created.txt"))
		require.Equal(t, []string{"created.txt", "edited.txt", "untouched.txt"}, merged)
	})

	t.Run("shared parent directories dedup across trees", func(t *testing.T) {
		// The delta root scaffolds parent directories of touched files, so a
		// directory can match in both trees; slash suffixes must not defeat
		// the dedup.
		host := []string{"sub/", "sub/inner.txt"}
		overlay := []string{"sub/", "sub/new.txt"}
		merged := mergeGlobMatches(host, overlay, touchedSet("sub/new.txt"))
		require.Equal(t, []string{"sub/", "sub/inner.txt", "sub/new.txt"}, merged)
	})

	t.Run("removed path with no overlay match disappears", func(t *testing.T) {
		merged := mergeGlobMatches([]string{"doomed.txt"}, nil, touchedSet("doomed.txt"))
		require.Empty(t, merged)
	})

	t.Run("mount points shadow source matches beneath them", func(t *testing.T) {
		ws := (&core.Workspace{}).WithMounted(dagql.ObjectResult[*core.Directory]{}, "deps/vendored")
		host := []string{"src/main.go", "deps/vendored/stale.go"}
		mounted := []string{"deps/vendored/pinned.go"}
		merged := mergeGlobMatches(host, mounted, ws.MountedPath)
		require.Equal(t, []string{"deps/vendored/pinned.go", "src/main.go"}, merged)
	})
}
