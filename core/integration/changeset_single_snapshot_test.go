package core

import (
	"context"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ChangesetSuite) TestSingleChangesetPreservesSnapshots(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	q := core.NewQuery(c)
	for _, plural := range []bool{false, true} {
		name := "withChangeset"
		if plural {
			name = "withChangesets"
		}
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			merge := func(parent, change *core.Changeset) *core.Changeset {
				if plural {
					return parent.WithChangesets([]*core.Changeset{change})
				}
				return parent.WithChangeset(change)
			}
			for _, gitPath := range []string{".git/config", ".git"} {
				t.Run(gitPath, func(ctx context.Context, t *testctx.T) {
					before := q.Directory().
						WithNewFile("keep", "unchanged").
						WithNewFile("text", "before").
						WithNewFile("removed", "remove").
						WithNewFile(gitPath, "original")
					private := q.Directory().WithNewFile("text", "after", core.DirectoryWithNewFileOpts{Permissions: 0o600})
					after := before.WithoutFile("removed").
						WithFile("text", private.File("text")).
						WithNewFile("app", "binary\x00payload\n", core.DirectoryWithNewFileOpts{Permissions: 0o750}).
						WithNewDirectory("empty", core.DirectoryWithNewDirectoryOpts{Permissions: 0o700}).
						WithNewFile(gitPath, "modified")
					merged := merge(q.Changeset(), after.Changes(before))
					for path, want := range map[string]int{"app": 0o750, "text": 0o600, "empty": 0o700} {
						stat, err := merged.After().Stat(ctx, path)
						require.NoError(t, err)
						mode, err := stat.Permissions(ctx)
						require.NoError(t, err)
						require.Equal(t, want, mode, path)
					}
					for path, want := range map[string]string{"app": "binary\x00payload\n", "text": "after", "keep": "unchanged"} {
						contents, err := merged.After().File(path).Contents(ctx)
						require.NoError(t, err)
						require.Equal(t, want, contents, path)
					}
					contents, err := merged.Before().File("removed").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "remove", contents)
					exists, err := merged.After().Exists(ctx, "removed")
					require.NoError(t, err)
					require.False(t, exists)
					for _, dir := range []*core.Directory{merged.Before(), merged.After()} {
						exists, err := dir.Exists(ctx, ".git")
						require.NoError(t, err)
						require.False(t, exists)
					}
				})
			}
			t.Run("unchanged parent retains its base", func(ctx context.Context, t *testctx.T) {
				base := q.Directory().WithNewFile("parent", "retain")
				before := q.Directory().WithNewFile("source", "before")
				after := before.WithNewFile("source", "after")
				merged := merge(base.Changes(base), after.Changes(before))
				for _, dir := range []*core.Directory{merged.Before(), merged.After()} {
					contents, err := dir.File("parent").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "retain", contents)
				}
				contents, err := merged.After().File("source").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "after", contents)
			})
			t.Run("parent deletion is retained", func(ctx context.Context, t *testctx.T) {
				empty := q.Directory()
				base := empty.WithNewFile("removed", "remove")
				before := empty.WithNewFile("source", "before")
				after := before.WithNewFile("source", "after")
				merged := merge(empty.Changes(base), after.Changes(before))
				removed, err := merged.RemovedPaths(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"removed"}, removed)
				contents, err := merged.After().File("source").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "after", contents)
			})
		})
	}
}
