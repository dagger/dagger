package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// These tests define the Workspace.findRoots contract: cwd-aware project-root
// discovery from an explicit start path.
//
// Config files are named deno.json, not dagger.json, so the fixtures are not
// mistaken for real modules.

func findRootsSource(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("dir/coucou.txt", "x").
		WithNewFile("sub/deno.json", "{}").
		WithNewFile("sub/dir/sub/deno.json", "{}").
		WithNewFile("sub/toto/a.txt", "x")
}

// TestWorkspaceFindRootsWalkDown asserts that from the workspace root the
// walk-down finds every directory holding a config file, and the find-up does
// not duplicate the cwd.
func (WorkspaceSuite) TestWorkspaceFindRootsWalkDown(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findRootsSource(c).AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "sub", "sub/dir/sub"}, dirs)
}

// TestWorkspaceFindRootsSubdir asserts that results are cwd-relative and
// scoped: from a project subdirectory, the cwd's own project and the ones
// beneath it are returned, and the workspace-root project is not.
func (WorkspaceSuite) TestWorkspaceFindRootsSubdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findRootsSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/sub",
	})

	dirs, err := ws.FindRoots(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "dir/sub"}, dirs)
}

// TestWorkspaceFindRootsStart scopes discovery below an explicit path while
// keeping results relative to the workspace cwd.
func (WorkspaceSuite) TestWorkspaceFindRootsStart(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findRootsSource(c).AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"deno.json"}, dagger.WorkspaceFindRootsOpts{
		Start: "sub",
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "sub/dir/sub"}, dirs)
}

// TestWorkspaceFindRootsAncestor asserts that when the cwd holds no
// config, the nearest enclosing project is returned as a ".."-prefixed path
// that resolves through other workspace APIs unchanged.
func (WorkspaceSuite) TestWorkspaceFindRootsAncestor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findRootsSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/dir",
	})

	dirs, err := ws.FindRoots(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.Equal(t, []string{".."}, dirs)

	contents, err := ws.Directory("..").File("deno.json").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "{}", contents)
}

// TestWorkspaceFindRootsExclude asserts that exclude globs prune the
// walk-down, and that without them vendored hits appear.
func (WorkspaceSuite) TestWorkspaceFindRootsExclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findRootsSource(c).
		WithNewFile("sub/node_modules/pkg/deno.json", "{}").
		AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.Contains(t, dirs, "sub/node_modules/pkg")

	dirs, err = ws.FindRoots(ctx, []string{"deno.json"}, dagger.WorkspaceFindRootsOpts{
		Exclude: []string{"**/node_modules/**"},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "sub", "sub/dir/sub"}, dirs)
}

// TestWorkspaceFindRootsMultipleMarkers asserts that any of the given
// filenames matches, and a directory holding several of them is returned once.
func (WorkspaceSuite) TestWorkspaceFindRootsMultipleMarkers(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("deno.jsonc", "{}").
		WithNewFile("a/deno.jsonc", "{}").
		AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"deno.json", "deno.jsonc"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "a"}, dirs)
}

// TestWorkspaceFindRootsNearestMixedMarker asserts that the nearest
// enclosing project wins across filenames: a farther-up project never shadows
// a closer one written with a different filename.
func (WorkspaceSuite) TestWorkspaceFindRootsNearestMixedMarker(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("a/deno.jsonc", "{}").
		WithNewFile("a/b/keep.txt", "x").
		AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
			Cwd: "/a/b",
		})

	dirs, err := ws.FindRoots(ctx, []string{"deno.json", "deno.jsonc"})
	require.NoError(t, err)
	require.Equal(t, []string{".."}, dirs)
}

// TestWorkspaceFindRootsCwdShadowsAncestor asserts that a marker in the
// cwd itself suppresses the find-up entirely, even when the ancestor's config
// uses a different filename: the walk-down already covers the cwd.
func (WorkspaceSuite) TestWorkspaceFindRootsCwdShadowsAncestor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("sub/deno.jsonc", "{}").
		AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
			Cwd: "/sub",
		})

	dirs, err := ws.FindRoots(ctx, []string{"deno.json", "deno.jsonc"})
	require.NoError(t, err)
	require.Equal(t, []string{"."}, dirs)
}

// TestWorkspaceFindRootsValidatesMarkers asserts that markers are
// required and must be basenames: a slash or parent segment would turn a name
// match into path traversal.
func (WorkspaceSuite) TestWorkspaceFindRootsValidatesMarkers(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findRootsSource(c).AsWorkspace()

	_, err := ws.FindRoots(ctx, []string{})
	require.ErrorContains(t, err, "at least one marker")

	for _, name := range []string{"", ".", "..", "sub/deno.json", `sub\deno.json`} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			_, err := ws.FindRoots(ctx, []string{name})
			require.Error(t, err)
		})
	}
}
