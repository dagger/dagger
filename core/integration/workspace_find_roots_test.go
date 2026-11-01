package core

import (
	"context"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// These tests define the Workspace.findRoots contract: cwd-aware project-root
// discovery from an explicit start path.
//
// Config files are named deno.json, not dagger.json, so the fixtures are not
// mistaken for real modules.

func findRootsSource(c *dagger.Client) *core.Directory {
	return core.NewQuery(c).Directory().
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
	ws := findRootsSource(c).AsWorkspace(core.DirectoryAsWorkspaceOpts{
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

	dirs, err := ws.FindRoots(ctx, []string{"deno.json"}, core.WorkspaceFindRootsOpts{
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
	ws := findRootsSource(c).AsWorkspace(core.DirectoryAsWorkspaceOpts{
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

	dirs, err = ws.FindRoots(ctx, []string{"deno.json"}, core.WorkspaceFindRootsOpts{
		Exclude: []string{"**/node_modules/**"},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "sub", "sub/dir/sub"}, dirs)
}

// TestWorkspaceFindRootsMultipleMarkers asserts that any of the given
// filenames matches, and a directory holding several of them is returned once.
func (WorkspaceSuite) TestWorkspaceFindRootsMultipleMarkers(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := core.NewQuery(c).Directory().
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
	ws := core.NewQuery(c).Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("a/deno.jsonc", "{}").
		WithNewFile("a/b/keep.txt", "x").
		AsWorkspace(core.DirectoryAsWorkspaceOpts{
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
	ws := core.NewQuery(c).Directory().
		WithNewFile("deno.json", "{}").
		WithNewFile("sub/deno.jsonc", "{}").
		AsWorkspace(core.DirectoryAsWorkspaceOpts{
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

func ignoreWorkspaceSource(c *dagger.Client, config string, extra ...func(*core.Directory) *core.Directory) *core.Directory {
	dir := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", config).
		WithNewFile("go.mod", "module example.com/root\n").
		WithNewFile("pkg/go.mod", "module example.com/pkg\n").
		WithNewFile(".delta/worktrees/nested/go.mod", "module example.com/delta\n").
		WithNewFile("pkg/.delta/nested/go.mod", "module example.com/pkgdelta\n").
		WithNewFile("pkg/vendor/go.mod", "module example.com/vendor\n")
	for _, apply := range extra {
		dir = apply(dir)
	}
	return dir
}

func requireWorkspaceRootEntries(ctx context.Context, t *testctx.T, ws *core.Workspace) []string {
	t.Helper()
	entries, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "pkg/")
	require.Contains(t, entries, "go.mod")
	require.Contains(t, entries, "dagger.toml")
	return entries
}

// TestWorkspaceFindRootsHonorsDaggerTomlIgnore asserts that top-level ignore
// in dagger.toml prunes findRoots and user-facing directory/file reads.
func (WorkspaceSuite) TestWorkspaceFindRootsHonorsDaggerTomlIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := ignoreWorkspaceSource(c, "ignore = [\".delta\"]\n").AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"go.mod"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "pkg", "pkg/.delta/nested", "pkg/vendor"}, dirs)

	entries := requireWorkspaceRootEntries(ctx, t, ws)
	require.NotContains(t, entries, ".delta/")

	_, err = ws.File(".delta/worktrees/nested/go.mod").Contents(ctx)
	require.Error(t, err)
}

// TestWorkspaceFindRootsIgnoreStartSubdir asserts ignore is relative to the
// selected config directory and rebased to the path being read: a root
// ignore of .delta does not hide pkg/.delta, while pkg/vendor does.
func (WorkspaceSuite) TestWorkspaceFindRootsIgnoreStartSubdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := ignoreWorkspaceSource(c, "ignore = [\".delta\", \"pkg/vendor\"]\n").AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"go.mod"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "pkg", "pkg/.delta/nested"}, dirs)

	dirs, err = ws.FindRoots(ctx, []string{"go.mod"}, core.WorkspaceFindRootsOpts{Start: "pkg"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"pkg", "pkg/.delta/nested"}, dirs)

	entries, err := ws.Directory("pkg").Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, ".delta/")
	require.NotContains(t, entries, "vendor/")
}

// TestWorkspaceFindRootsIgnoreConfigInSubdir asserts ignore in a nested
// dagger.toml is resolved from that config directory, not the workspace root.
func (WorkspaceSuite) TestWorkspaceFindRootsIgnoreConfigInSubdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := core.NewQuery(c).Directory().
		WithNewFile("app/dagger.toml", "ignore = [\".delta\"]\n").
		WithNewFile("app/go.mod", "module example.com/app\n").
		WithNewFile("app/pkg/go.mod", "module example.com/pkg\n").
		WithNewFile("app/.delta/nested/go.mod", "module example.com/delta\n").
		WithNewFile("go.mod", "module example.com/root\n").
		AsWorkspace(core.DirectoryAsWorkspaceOpts{Cwd: "/app"})

	dirs, err := ws.FindRoots(ctx, []string{"go.mod"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "pkg"}, dirs)

	entries, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "pkg/")
	require.Contains(t, entries, "go.mod")
	require.NotContains(t, entries, ".delta/")
}

// TestWorkspaceFindRootsIgnoreGlob asserts a glob ignore hides matching
// markers at any depth.
func (WorkspaceSuite) TestWorkspaceFindRootsIgnoreGlob(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := ignoreWorkspaceSource(c, "ignore = [\"**/.generated\"]\n", func(dir *core.Directory) *core.Directory {
		return dir.
			WithNewFile(".generated/go.mod", "module example.com/gen\n").
			WithNewFile("pkg/.generated/go.mod", "module example.com/pkggen\n")
	}).AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"go.mod"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "pkg", "pkg/.delta/nested", "pkg/vendor", ".delta/worktrees/nested"}, dirs)

	entries := requireWorkspaceRootEntries(ctx, t, ws)
	require.NotContains(t, entries, ".generated/")
}

// TestWorkspaceFindRootsIgnoreMalformedConfig asserts a broken dagger.toml
// adds no excludes and does not fail user-facing reads.
func (WorkspaceSuite) TestWorkspaceFindRootsIgnoreMalformedConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := ignoreWorkspaceSource(c, "ignore = [\n").AsWorkspace()

	dirs, err := ws.FindRoots(ctx, []string{"go.mod"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{".", "pkg", "pkg/.delta/nested", "pkg/vendor", ".delta/worktrees/nested"}, dirs)

	entries := requireWorkspaceRootEntries(ctx, t, ws)
	require.Contains(t, entries, ".delta/")
}

// TestWorkspaceFindRootsIgnoreRemovedConfig asserts user-facing reads do
// not fail when ConfigFile is still set but dagger.toml has been removed
// from the overlay. Ignore is snapshotted at load, so this does not
// require the overlay to re-parse config.
func (WorkspaceSuite) TestWorkspaceFindRootsIgnoreRemovedConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := ignoreWorkspaceSource(c, "ignore = [\".delta\"]\n").AsWorkspace().WithoutFile("dagger.toml")

	entries, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "go.mod")

	dirs, err := ws.FindRoots(ctx, []string{"go.mod"})
	require.NoError(t, err)
	require.NotEmpty(t, dirs)
}
