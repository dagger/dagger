package core

// Coverage for Workspace.reloaded, whose whole job is a side effect: bumping
// the owning client's host-read epoch. The value it returns must therefore be
// the workspace it was called on, unchanged and — crucially — with an
// unchanged call chain.

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceReloadedKeepsCallChain locks in that reloaded does not appear in
// the call chain of its own result.
//
// The field carries dagql.PerCallInput so that it re-runs — and re-bumps the
// read epoch — on every call. Minting a result for the current call would
// therefore stamp a random nonce into the returned workspace's ID, and every
// workspace derived from it afterwards would carry that nonce for the rest of
// the session: a permanently novel chain nothing can be served from cache
// against, module loads included. An agent that reloads its own tools once
// would re-declare every module on every subsequent edit.
func (WorkspaceSuite) TestWorkspaceReloadedKeepsCallChain(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := c.Directory().WithNewFile("a.txt", "a\n").AsWorkspace()

	edited := workspaceRecipeFields(ctx, t, c, base.Reloaded().WithNewFile("b.txt", "b\n"))
	require.True(t, edited["asWorkspace"], "sanity: the workspace recipe was reached")
	require.True(t, edited["withNewFile"], "sanity: the edit is in the chain")
	require.False(t, edited["reloaded"],
		"an edit made after reloaded must not carry a reloaded segment: "+
			"reloaded must return its parent's own result, not a new one minted for the current call")

	// Not even the reload itself records one.
	reloaded := workspaceRecipeFields(ctx, t, c, base.Reloaded())
	require.False(t, reloaded["reloaded"])
}

// workspaceRecipeFields returns every field name in a workspace's recipe-form
// call chain.
//
// Workspace.id is an engine-local runtime handle, which says nothing about how
// the workspace was derived. LLM.portableID is the one place a client can see
// the recipe: it embeds the bound workspace's recipe ID as the argument of
// withWorkspace, and collectIDFieldNames walks into nested ID literals.
func workspaceRecipeFields(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	ws *dagger.Workspace,
) map[string]bool {
	t.Helper()
	portable, err := c.LLM().WithWorkspace(ws).PortableID(ctx)
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(string(portable)))
	fields := map[string]bool{}
	collectIDFieldNames(id, fields)
	return fields
}

// TestWorkspaceReloadedPreservesContent guards the other half of the contract:
// returning the parent must not lose the workspace's pending overlay.
func (WorkspaceSuite) TestWorkspaceReloadedPreservesContent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ws := c.Directory().WithNewFile("a.txt", "a\n").AsWorkspace().
		WithNewFile("b.txt", "b\n").
		Reloaded()

	contents, err := ws.File("b.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "b\n", contents)

	empty, err := ws.Changes().IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, empty, "the pending overlay must survive a reload")
}
