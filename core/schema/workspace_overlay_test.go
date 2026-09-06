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
		{name: "deep file keeps deepest parent", touched: []string{"a/b/c.txt"}, want: []string{"a/b"}},
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
