package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTouchedParentDirs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		touched []string
		want    []string
	}{
		{name: "empty"},
		{name: "root-level files only", touched: []string{"dagger.lock", "dagger.toml"}},
		{name: "nested file", touched: []string{"demo/dagger.lock"}, want: []string{"demo"}},
		{name: "deep file seeds every ancestor, outermost first", touched: []string{"a/b/c.txt"}, want: []string{"a", "a/b"}},
		{name: "ancestors shared by siblings are seeded once", touched: []string{"a/b/c.txt", "a/d/e.txt"}, want: []string{"a", "a/b", "a/d"}},
		{name: "de-duplicates parents", touched: []string{"sub/a.txt", "sub/b.txt"}, want: []string{"sub"}},
		{name: "touched directory itself has no parent seeded", touched: []string{"sub"}},
		{name: "paths under a touched directory are covered by it", touched: []string{"sub/x.txt", "sub", "sub/y/z.txt"}},
		{name: "sibling of a touched directory still seeds", touched: []string{"sub", "other/x.txt"}, want: []string{"other"}},
		{name: "cleans trailing slash and dot segments", touched: []string{"./sub/a.txt", "dir/"}, want: []string{"sub"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, touchedParentDirs(tc.touched))
		})
	}
}

func TestTouchedParentDirPatterns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		touched  []string
		includes []string
		excludes []string
	}{
		{name: "root-level files need no directory read", touched: []string{"dagger.lock"}},
		{name: "deepest directory of a chain only", touched: []string{"a/b/c.txt"},
			includes: []string{"a/b"}, excludes: []string{"a/b/*"}},
		{name: "separate chains each contribute", touched: []string{"a/b/c.txt", "x/y.txt"},
			includes: []string{"a/b", "x"}, excludes: []string{"a/b/*", "x/*"}},
		{name: "a shallower ancestor of another chain is dropped", touched: []string{"a/f.txt", "a/b/c/g.txt"},
			includes: []string{"a/b/c"}, excludes: []string{"a/b/c/*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			includes, excludes := touchedParentDirPatterns(tc.touched)
			require.Equal(t, tc.includes, includes)
			require.Equal(t, tc.excludes, excludes)
		})
	}
}
