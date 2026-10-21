package core

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ChangesetSuite) TestMergedFilePermissions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	q := core.NewQuery(c)
	base := q.Directory().
		WithNewFile("keep", "untouched", core.DirectoryWithNewFileOpts{Permissions: 0o640}).
		WithNewFile("modified", "before", core.DirectoryWithNewFileOpts{Permissions: 0o644}).
		WithNewFile("deleted", "gone", core.DirectoryWithNewFileOpts{Permissions: 0o600})
	first := base.
		WithNewFile("a", "private", core.DirectoryWithNewFileOpts{Permissions: 0o600}).
		// Overwriting an existing file preserves its mode, so recreate it
		// to exercise a content change together with a permission change.
		WithoutFile("modified").
		WithNewFile("modified", "after", core.DirectoryWithNewFileOpts{Permissions: 0o640}).
		WithoutFile("deleted").Changes(base)
	second := base.
		WithNewFile("nested/b", "binary\x00payload", core.DirectoryWithNewFileOpts{Permissions: 0o750}).
		Changes(base)
	third := base.
		WithNewFile("c", "third", core.DirectoryWithNewFileOpts{Permissions: 0o440}).
		Changes(base)
	stat, err := first.After().Stat(ctx, "modified")
	require.NoError(t, err)
	permissions, err := stat.Permissions(ctx)
	require.NoError(t, err)
	require.Equal(t, 0o640, permissions, "input fixture mode")

	for _, tc := range []struct {
		name   string
		merged *core.Changeset
	}{
		{"multiple inputs into empty receiver", q.Changeset().WithChangesets([]*core.Changeset{first, second})},
		{"one input into non-empty receiver", first.WithChangeset(second)},
		{"multiple inputs into non-empty receiver", first.WithChangesets([]*core.Changeset{second, third})},
		{"sequential merges", first.WithChangeset(second).WithChangeset(third)},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			after := tc.merged.After()
			want := map[string]int{"keep": 0o640, "modified": 0o640, "a": 0o600, "nested/b": 0o750}
			if tc.name == "multiple inputs into non-empty receiver" || tc.name == "sequential merges" {
				want["c"] = 0o440
			}
			for p, permissions := range want {
				stat, err := after.Stat(ctx, p)
				require.NoError(t, err)
				actual, err := stat.Permissions(ctx)
				require.NoError(t, err)
				require.Equal(t, permissions, actual, p)
			}
			contents, err := after.File("nested/b").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "binary\x00payload", contents)
			contents, err = after.File("modified").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "after", contents)
			_, err = after.File("deleted").Sync(ctx)
			require.Error(t, err)

			dest := t.TempDir()
			_, err = after.Export(ctx, dest)
			require.NoError(t, err)
			for p, permissions := range want {
				info, err := os.Stat(filepath.Join(dest, p))
				require.NoError(t, err)
				require.Equal(t, os.FileMode(permissions), info.Mode().Perm(), p)
			}
		})
	}
}
