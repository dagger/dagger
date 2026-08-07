package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceChangesetRooting pins where changeset paths are measured from,
// on both sides of the cwd cutover. The engine measures generated changes
// from the workspace root, but a client applies a returned changeset at its
// own cwd — so generating from ./app used to need every module to translate
// the paths itself (the polyfill did it, #13769). Past the cutover the engine
// hands the changeset back already measured from the cwd; modules pinned to
// older engine versions keep the root-measured form they translate today.
//
// The fixture has two identical dang modules that only differ in their
// declared engineVersion, each generating the module the caller stands in
// (app/, whose compact dagger.json forces a config rewrite so the changeset
// is never empty).
func (WorkspaceSuite) TestWorkspaceChangesetRooting(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceFixture(t, c, "changeset-rooting").
		WithWorkdir("/work/app")

	t.Run("a module past the cutover gets cwd-measured paths", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-new", "generated")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "dagger.json")
		for _, p := range paths {
			require.NotContains(t, p, "app/", "path %q should be measured from the cwd, not the workspace root", p)
		}
	})

	t.Run("a module before the cutover keeps root-measured paths", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("rooter-old", "generated")).Stdout(ctx)
		require.NoError(t, err)
		paths := strings.Fields(out)
		require.Contains(t, paths, "app/dagger.json")
	})
}
