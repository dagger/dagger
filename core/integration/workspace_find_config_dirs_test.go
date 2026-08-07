package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// These tests define the Workspace.findConfigDirs contract: cwd-aware config
// discovery, so a module run from a subdirectory acts on the project it is in
// and the projects beneath it. The fixture tree and cases mirror the polyfill
// design this API replaces (dagger/polyfill design/find-config-dirs.md).
//
// Config files are named deno.json, not dagger.json, so the fixtures are not
// mistaken for real modules.

func findConfigDirsSource(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("dir/coucou.txt", "x").
		WithNewFile("sub/deno.json", "{}").
		WithNewFile("sub/dir/sub/deno.json", "{}").
		WithNewFile("sub/toto/a.txt", "x")
}

// TestWorkspaceFindConfigDirsWalkDown asserts that from the workspace root the
// walk-down finds every directory holding a config file, and the find-up does
// not duplicate the cwd.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsWalkDown(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findConfigDirsSource(c).AsWorkspace()

	dirs, err := ws.FindConfigDirs(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "sub", "sub/dir/sub"}, dirs)
}

// TestWorkspaceFindConfigDirsSubdir asserts that results are cwd-relative and
// scoped: from a project subdirectory, the cwd's own project and the ones
// beneath it are returned, and the workspace-root project is not.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsSubdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findConfigDirsSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/sub",
	})

	dirs, err := ws.FindConfigDirs(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "dir/sub"}, dirs)
}

// TestWorkspaceFindConfigDirsAncestor asserts that when the cwd holds no
// config, the nearest enclosing project is returned as a ".."-prefixed path
// that resolves through other workspace APIs unchanged.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsAncestor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findConfigDirsSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/dir",
	})

	dirs, err := ws.FindConfigDirs(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.Equal(t, []string{".."}, dirs)

	contents, err := ws.Directory("..").File("deno.json").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "{}", contents)
}

// TestWorkspaceFindConfigDirsExclude asserts that exclude globs prune the
// walk-down, and that without them vendored hits appear.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsExclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findConfigDirsSource(c).
		WithNewFile("sub/node_modules/pkg/deno.json", "{}").
		AsWorkspace()

	dirs, err := ws.FindConfigDirs(ctx, []string{"deno.json"})
	require.NoError(t, err)
	require.Contains(t, dirs, "sub/node_modules/pkg")

	dirs, err = ws.FindConfigDirs(ctx, []string{"deno.json"}, dagger.WorkspaceFindConfigDirsOpts{
		Exclude: []string{"**/node_modules/**"},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "sub", "sub/dir/sub"}, dirs)
}

// TestWorkspaceFindConfigDirsMultiFilename asserts that any of the given
// filenames matches, and a directory holding several of them is returned once.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsMultiFilename(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("deno.jsonc", "{}").
		WithNewFile("a/deno.jsonc", "{}").
		AsWorkspace()

	dirs, err := ws.FindConfigDirs(ctx, []string{"deno.json", "deno.jsonc"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "a"}, dirs)
}

// TestWorkspaceFindConfigDirsNearestMixedFilename asserts that the nearest
// enclosing project wins across filenames: a farther-up project never shadows
// a closer one written with a different filename.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsNearestMixedFilename(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("a/deno.jsonc", "{}").
		WithNewFile("a/b/keep.txt", "x").
		AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
			Cwd: "/a/b",
		})

	dirs, err := ws.FindConfigDirs(ctx, []string{"deno.json", "deno.jsonc"})
	require.NoError(t, err)
	require.Equal(t, []string{".."}, dirs)
}

// TestWorkspaceFindConfigDirsCwdShadowsAncestor asserts that a config in the
// cwd itself suppresses the find-up entirely, even when the ancestor's config
// uses a different filename: the walk-down already covers the cwd.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsCwdShadowsAncestor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("sub/deno.jsonc", "{}").
		AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
			Cwd: "/sub",
		})

	dirs, err := ws.FindConfigDirs(ctx, []string{"deno.json", "deno.jsonc"})
	require.NoError(t, err)
	require.Equal(t, []string{"."}, dirs)
}

// TestWorkspaceFindConfigDirsValidatesFilenames asserts that filenames are
// required and must be basenames: a slash or parent segment would turn a name
// match into path traversal.
func (WorkspaceSuite) TestWorkspaceFindConfigDirsValidatesFilenames(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := findConfigDirsSource(c).AsWorkspace()

	_, err := ws.FindConfigDirs(ctx, []string{})
	require.ErrorContains(t, err, "at least one filename")

	for _, name := range []string{"", ".", "..", "sub/deno.json", `sub\deno.json`} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			_, err := ws.FindConfigDirs(ctx, []string{name})
			require.Error(t, err)
		})
	}
}
