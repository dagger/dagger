package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceChangesetRooting pins where changeset paths are measured from.
// Every Workspace.changes call is measured from that workspace's cwd past the
// compatibility cutover. The caller supplies the baseline. ModuleSource's
// lower-level generatedContextChangeset remains context-relative because a
// ModuleSource does not carry workspace position.
//
// The fixture has two identical dang modules that only differ in their
// declared engineVersion. Each reports both a generated-context changeset and
// an ordinary Workspace.changes result. The compact dagger.json is staged at
// runtime so repository generation can keep the checked-in fixture normalized
// without making the generated diff empty.
func (WorkspaceSuite) TestWorkspaceChangesetRooting(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceFixture(t, c, "changeset-rooting").
		WithNewFile("app/dagger.json", `{"name":"app","engineVersion":"v1.0.0","sdk":{"source":"dang"},"source":"."}`).
		WithWorkdir("/work/app")

	t.Run("generated context changesets remain context-measured", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-new", "generated")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "app/dagger.json")
	})

	t.Run("ordinary workspace changes past the cutover are cwd-measured", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-new", "edited")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "edited.txt")
		require.NotContains(t, paths, "app/edited.txt")
	})

	t.Run("explicit comparisons past the cutover are cwd-measured", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-new", "compared")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "compared.txt")
		require.NotContains(t, paths, "app/compared.txt")
	})

	t.Run("explicit baseline excludes earlier overlays", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-new", "compared-after-edit")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "after.txt")
		require.NotContains(t, paths, "before.txt")
	})

	t.Run("explicit comparison captures modifications and removals", func(ctx context.Context, t *testctx.T) {
		modified, err := ctr.With(daggerReportCall("rooter-new", "compared-modification")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.Fields(modified), "main.dang")

		removed, err := ctr.With(daggerReportCall("rooter-new", "compared-removal")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.Fields(removed), "main.dang")
	})

	t.Run("derived workspace can become the next baseline", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-new", "nested-comparison")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "inner.txt")
		require.NotContains(t, paths, "outer.txt")
	})

	t.Run("callers can explicitly measure from the workspace root", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-new", "root-edited")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "app/root-edited.txt")
	})

	t.Run("a module before the cutover keeps root-measured paths", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-old", "generated")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "app/dagger.json")
	})

	t.Run("ordinary workspace changes before the cutover are also root-measured", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-old", "edited")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "app/edited.txt")
	})
}
