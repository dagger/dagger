package core

import (
	"context"
	"crypto/rand"
	"fmt"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// A completed check must retain scalar configuration reads. The configuration
// can select another file after the workspace changes.
func (ArtifactsSuite) TestSourcePathFilteringDynamicReads(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = './probe'\nmain = true\n").
		WithNewFile("probe/dagger-module.toml", "name = 'probe'\nengineVersion = 'v1.0.0-beta.20'\n[runtime]\nsource = 'dang'\n").
		WithNewFile("probe/main.dang", fmt.Sprintf(`# %s
type Probe {
 pub read(ws: Workspace!): String! {
   let selected = ws.file("/config.txt").contents
   ws.file("/" + selected).contents
 }
 pub first(ws: Workspace!): Void @check { probe.read(ws: ws); null }
 pub second(ws: Workspace!): Void @check { probe.read(ws: ws); null }
 pub docs(ws: Workspace!): Void @check {
   ws.directory("/", include: ["docs/*.md"]).entries
   null
 }
 pub parent(ws: Workspace!): Void @check {
   ws.directory("/docs").directory("..").file("old.txt").contents
   null
 }
 pub linked(ws: Workspace!): Void @check { ws.file("/linked/readme.md").contents; null }
 pub ignored(ws: Workspace!): Void @check {
   ws.directory("/", include: ["docs/*.md"], gitignore: true).entries
   null
 }
 pub clock(ws: Workspace!): Void @check {
   Dagger.currentTimestamp
   ws.file("/config.txt").contents
   null
 }
 pub liveRead(ws: Workspace!): String! @cache(policy: FunctionCachePolicy.Never) {
   ws.file("/config.txt").contents
 }
 pub perCall(ws: Workspace!): Void @check { probe.liveRead(ws: ws); null }
}`, rand.Text())).
		WithNewFile("config.txt", "old.txt").
		WithNewFile("old.txt", "old input").
		WithNewFile("new.txt", "new input").
		WithNewFile("unrelated.txt", "before").
		WithNewFile("docs/readme.md", "documentation").
		WithNewFile(".gitignore", "docs/ignored.md\n").
		WithSymlink("docs", "linked")

	workspaceID := func(t *testctx.T, src *core.Directory) core.ID {
		t.Helper()
		id, err := src.AsWorkspace().ID(ctx)
		require.NoError(t, err)
		return id
	}
	selected := func(t *testctx.T, ws core.ID, check, path string) bool {
		t.Helper()
		got, err := testutil.QueryWithClient[struct {
			Node struct {
				Artifacts struct {
					FilterAffectedByPath struct {
						Items []struct{ URI string }
					}
				}
			}
		}](c, t, `query($ws: ID!, $check: String!, $path: WorkspacePath!) {
 node(id: $ws) { ... on Workspace { artifacts(include: [$check]) {
  filterAffectedByPath(path: $path) { items { uri } }
 } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": ws, "check": check, "path": path}})
		require.NoError(t, err)
		items := got.Node.Artifacts.FilterAffectedByPath.Items
		require.LessOrEqual(t, len(items), 1)
		return len(items) == 1
	}
	emptyDiffSelected := func(t *testctx.T, ws core.ID) bool {
		t.Helper()
		items, err := core.Ref[*core.Workspace](core.NewQuery(c), ws).
			Artifacts(core.WorkspaceArtifactsOpts{Include: []string{"first"}}).
			FilterAffectedByDiff(source.Changes(source)).Items(ctx)
		require.NoError(t, err)
		require.LessOrEqual(t, len(items), 1)
		return len(items) == 1
	}
	warm := func(t *testctx.T, ws core.ID, check string) {
		t.Helper()
		got, err := testutil.QueryWithClient[struct {
			Node struct {
				Artifacts struct {
					AsChecks []struct {
						Sync struct {
							Pass  bool
							Error *struct{ Message string }
						}
					}
				}
			}
		}](c, t, `query($ws: ID!, $check: String!) {
 node(id: $ws) { ... on Workspace { artifacts(include: [$check]) {
  asChecks { sync { pass error { message } } }
 } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": ws, "check": check}})
		require.NoError(t, err)
		require.Len(t, got.Node.Artifacts.AsChecks, 1)
		require.True(t, got.Node.Artifacts.AsChecks[0].Sync.Pass, "%+v", got.Node.Artifacts.AsChecks[0].Sync.Error)
	}

	base := workspaceID(t, source)
	require.True(t, selected(t, base, "first", "unrelated.txt"), "an unrun body has unknown reads")
	require.True(t, emptyDiffSelected(t, base), "an empty diff must retain unknown reads")
	warm(t, base, "first")
	require.False(t, selected(t, base, "first", "unrelated.txt"))
	require.False(t, emptyDiffSelected(t, base), "an empty diff can omit a completed known check")
	require.True(t, selected(t, base, "first", "config.txt"))
	require.True(t, selected(t, base, "first", "old.txt"))
	require.False(t, selected(t, base, "first", "new.txt"))
	require.True(t, selected(t, base, "first", "probe/main.dang"), "module source remains an input")

	// A different caller first reaches the same helper after its result is
	// cached. Its read summary must include the cached helper's scalar reads.
	warm(t, base, "second")
	require.True(t, selected(t, base, "second", "config.txt"))
	require.True(t, selected(t, base, "second", "old.txt"))
	require.False(t, selected(t, base, "second", "new.txt"))

	// Only the root changes. The files used by the check have equal bytes.
	unrelated := workspaceID(t, source.WithNewFile("unrelated.txt", "after"))
	require.False(t, selected(t, unrelated, "first", "unrelated.txt"), "unchanged inputs validate the prior summary")

	changed := workspaceID(t, source.WithNewFile("config.txt", "new.txt"))
	require.True(t, selected(t, changed, "first", "unrelated.txt"), "changed configuration invalidates the prior summary")
	warm(t, changed, "first")
	require.True(t, selected(t, changed, "first", "config.txt"))
	require.True(t, selected(t, changed, "first", "new.txt"))
	require.False(t, selected(t, changed, "first", "old.txt"), "the new complete summary replaces the old branch")
	require.False(t, selected(t, changed, "first", "unrelated.txt"))

	t.Run("filtered directory", func(_ context.Context, t *testctx.T) {
		warm(t, base, "docs")
		require.False(t, selected(t, base, "docs", "unrelated.txt"))
		require.False(t, selected(t, unrelated, "docs", "unrelated.txt"))
		require.True(t, selected(t, base, "docs", "docs/added.md"), "selectors include files that do not yet exist")
		added := workspaceID(t, source.WithNewFile("docs/added.md", "new documentation"))
		require.True(t, selected(t, added, "docs", "unrelated.txt"), "a new matching file invalidates the old summary")
		warm(t, added, "docs")
		require.True(t, selected(t, added, "docs", "docs/added.md"))
		require.False(t, selected(t, added, "docs", "unrelated.txt"))
	})
	for _, check := range []string{"parent", "linked", "ignored", "clock", "per-call"} {
		t.Run(check, func(_ context.Context, t *testctx.T) {
			warm(t, base, check)
			require.True(t, selected(t, base, check, "unrelated.txt"))
			require.True(t, selected(t, unrelated, check, "unrelated.txt"))
		})
	}
}
