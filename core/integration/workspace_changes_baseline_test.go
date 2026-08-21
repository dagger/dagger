package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceChangesAgainstExistingBaseline covers comparing a workspace
// against a baseline it already shares content with. Generation is the case
// that matters: an SDK rewrites a module's whole context, so the changeset it
// produces spans files that already sit on the workspace unchanged. Only the
// files whose content actually differs may come back from Workspace.changes —
// otherwise every regenerate re-reports the entire generated context,
// including engine-owned files the SDK never touched.
//
// The fixture stands in for codegen without needing an SDK: the changeset's
// after side is the before side with fresh timestamps, which is what a
// rewritten-in-place generated file looks like.
func (WorkspaceSuite) TestWorkspaceChangesAgainstExistingBaseline(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceFixture(t, c, "changes-baseline").
		WithNewFile("generated.txt", "generated")

	t.Run("rewriting existing content adds nothing", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("reporter", "rewritten-baseline")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"added.txt"}, strings.Fields(out))
	})

	t.Run("editing existing content reports a modification", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("reporter", "modified-baseline")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"generated.txt"}, strings.Fields(out))
	})
}
