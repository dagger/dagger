package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// workspaceWriteKind is one way a workspace comes into existence, prepared so
// that "target" already holds keep.txt. Writing a directory over a path has to
// mean the same thing on all of them, and the overlay underneath differs: a
// host-backed workspace accumulates its edits on an empty delta root diffed
// against a sparse host base, while value and git workspaces edit their full
// in-engine tree (see overlayEdit).
type workspaceWriteKind struct {
	name  string
	setup func(ctx context.Context, t *testctx.T) (*dagger.Client, *dagger.Workspace)
}

func workspaceWriteKinds() []workspaceWriteKind {
	return []workspaceWriteKind{
		{
			name: "host-backed",
			setup: func(ctx context.Context, t *testctx.T) (*dagger.Client, *dagger.Workspace) {
				workdir := t.TempDir()
				initGitRepo(ctx, t, workdir)
				require.NoError(t, os.MkdirAll(filepath.Join(workdir, "target"), 0o755))
				require.NoError(t, os.WriteFile(
					filepath.Join(workdir, "target", "keep.txt"), []byte("keep"), 0o644))
				c := connect(ctx, t, dagger.WithWorkdir(workdir))
				return c, c.CurrentWorkspace()
			},
		},
		{
			name: "value",
			setup: func(ctx context.Context, t *testctx.T) (*dagger.Client, *dagger.Workspace) {
				c := connect(ctx, t)
				return c, c.Directory().WithNewFile("target/keep.txt", "keep").AsWorkspace()
			},
		},
		{
			name: "git",
			setup: func(ctx context.Context, t *testctx.T) (*dagger.Client, *dagger.Workspace) {
				c := connect(ctx, t)
				content := c.Directory().WithNewFile("target/keep.txt", "keep")
				gitDaemon, repoURL := gitService(ctx, t, c, content)
				return c, c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
					Head().AsWorkspace()
			},
		},
	}
}

// TestWorkspaceWithDirectoryMerges covers the field callers reach for when they
// want Directory.withDirectory's merge on a workspace: writing into a path
// without discarding what is already there. That is what an SDK needs to
// scaffold a module beside the config the engine just wrote, and before this
// field existed the only spelling for it — withNewDirectory — merged on a value
// or git workspace but replaced on a host-backed one (dagger/dagger#13955).
func (WorkspaceSuite) TestWorkspaceWithDirectoryMerges(ctx context.Context, t *testctx.T) {
	for _, kind := range workspaceWriteKinds() {
		t.Run(kind.name, func(ctx context.Context, t *testctx.T) {
			c, ws := kind.setup(ctx, t)
			source := c.Directory().WithNewFile("new.txt", "new")

			t.Run("layers onto what the path already holds", func(ctx context.Context, t *testctx.T) {
				written := ws.WithDirectory("target", source)

				entries, err := written.Directory("target").Entries(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"keep.txt", "new.txt"}, entries)

				changes := written.Changes(dagger.WorkspaceChangesOpts{From: ws})
				removed, err := changes.RemovedPaths(ctx)
				require.NoError(t, err)
				require.Empty(t, removed)

				// Exactly the one file the source carries: no ancestor
				// directory the workspace already has may be reported as
				// added either.
				added, err := changes.AddedPaths(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"target/new.txt"}, added)

				// Reporting a file the caller never wrote is the same defect
				// wearing a different mask: an SDK is told it changed content
				// it only happened to be standing next to.
				modified, err := changes.ModifiedPaths(ctx)
				require.NoError(t, err)
				require.Empty(t, modified)
			})

			t.Run("layers onto the workspace root", func(ctx context.Context, t *testctx.T) {
				written := ws.WithDirectory("/", source)

				contents, err := written.File("new.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "new", contents)

				keep, err := written.File("target/keep.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "keep", keep)

				removed, err := written.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
				require.NoError(t, err)
				require.Empty(t, removed)
			})

			t.Run("creates a path the workspace does not have", func(ctx context.Context, t *testctx.T) {
				written := ws.WithDirectory("fresh/nested", source)

				contents, err := written.File("fresh/nested/new.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "new", contents)

				removed, err := written.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
				require.NoError(t, err)
				require.Empty(t, removed)
			})

			// A host-backed workspace applies the edit to its delta root, which
			// holds only what earlier edits wrote rather than the workspace's
			// own content. Both of these read that base, so they are where the
			// seeding it needs is visible.
			t.Run("keeps earlier edits under the path", func(ctx context.Context, t *testctx.T) {
				written := ws.WithNewFile("target/staged.txt", "staged").
					WithDirectory("target", source)

				entries, err := written.Directory("target").Entries(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"keep.txt", "new.txt", "staged.txt"}, entries)

				removed, err := written.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
				require.NoError(t, err)
				require.Empty(t, removed)
			})

			t.Run("does not resurrect removed content", func(ctx context.Context, t *testctx.T) {
				written := ws.WithoutFile("target/keep.txt").WithDirectory("target", source)

				entries, err := written.Directory("target").Entries(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"new.txt"}, entries)

				removed, err := written.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"target/keep.txt"}, removed)
			})
		})
	}
}

// workspaceInitOverExistingFixture is an SDK module whose module-init scaffolds
// through Workspace.withDirectory, which is what an SDK needs once its
// destination may already hold something: the user's own files, and the module
// config the engine writes in the same init. dagger/go-sdk#30 pinned that
// guarantee from the SDK side by reading the destination back and layering onto
// it by hand; withDirectory is that read-back moved into the API.
func workspaceInitOverExistingFixture(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return goGitBase(t, c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c)).
		WithNewFile("dagger.toml", `[modules.init-fixture]
source = "sdk/init-fixture"

[modules.init-fixture.as-sdk]
name = "fixture"
`).
		WithNewFile("sdk/init-fixture/dagger.json", `{
  "name": "init-fixture",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": "."
}`).
		WithNewFile("sdk/init-fixture/main.go", `package main

import (
	"context"

	"dagger/init-fixture/internal/dagger"
)

type InitFixture struct{}

// InitModule scaffolds the SDK-owned files for a new module, onto whatever the
// destination already holds.
func (m *InitFixture) InitModule(ctx context.Context, ws *dagger.Workspace, name string, path string) (*dagger.Changeset, error) {
	scaffold := dag.Directory().WithNewFile("scaffold.txt", name+"\n")
	return ws.WithDirectory(path, scaffold).Changes(dagger.WorkspaceChangesOpts{From: ws}), nil
}
`)
}

// TestModuleInitScaffoldsOverExistingContent is the flow the split was found
// in: `dagger module init` against a real checkout, where the destination is
// not empty. The engine's dagger-module.toml and the SDK's scaffold arrive in
// the same apply, and neither may take the user's files with it.
func (WorkspaceSuite) TestModuleInitScaffoldsOverExistingContent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	initialized := workspaceInitOverExistingFixture(t, c).
		WithNewFile(".dagger/modules/newmod/keep.txt", "written before init\n").
		With(daggerExec("module", "init", "fixture", "newmod", "--no-generate", "--auto-apply"))

	out, err := initialized.CombinedOutput(ctx)
	require.NoError(t, err, out)

	keep, err := initialized.File(".dagger/modules/newmod/keep.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "written before init\n", keep)

	scaffold, err := initialized.File(".dagger/modules/newmod/scaffold.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "newmod\n", scaffold)

	_, err = initialized.File(".dagger/modules/newmod/dagger-module.toml").Contents(ctx)
	require.NoError(t, err)
}

// The comparison dagger/dagger#13955 was reported with: one call, one source,
// one path, put to a host-backed workspace and to the synthetic workspace made
// out of that same workspace's own content. They answered differently about
// what they had removed.
func (WorkspaceSuite) TestWorkspaceWithNewDirectoryAgreesWithSyntheticCopy(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "target"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workdir, "target", "keep.txt"), []byte("do not delete me\n"), 0o644))

	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	source := c.Directory().WithNewFile("new.txt", "written by withNewDirectory")

	hostBacked := c.CurrentWorkspace()
	synthetic := hostBacked.Directory("/").AsWorkspace()

	hostRemoved, err := hostBacked.WithNewDirectory("/target", source).
		Changes(dagger.WorkspaceChangesOpts{From: hostBacked}).RemovedPaths(ctx)
	require.NoError(t, err)
	syntheticRemoved, err := synthetic.WithNewDirectory("/target", source).
		Changes(dagger.WorkspaceChangesOpts{From: synthetic}).RemovedPaths(ctx)
	require.NoError(t, err)

	require.Equal(t, []string{"target/keep.txt"}, hostRemoved)
	require.Equal(t, hostRemoved, syntheticRemoved)
}

// TestWorkspaceWithNewDirectoryReplaces pins withNewDirectory to replacement on
// every workspace kind. dagger/dagger#13955: it replaced on a host-backed
// workspace and merged on a value or git one, so the same call meant two
// different things depending only on where the workspace came from.
func (WorkspaceSuite) TestWorkspaceWithNewDirectoryReplaces(ctx context.Context, t *testctx.T) {
	for _, kind := range workspaceWriteKinds() {
		t.Run(kind.name, func(ctx context.Context, t *testctx.T) {
			c, ws := kind.setup(ctx, t)
			source := c.Directory().WithNewFile("new.txt", "new")

			t.Run("replaces what the path already holds", func(ctx context.Context, t *testctx.T) {
				written := ws.WithNewDirectory("target", source)

				entries, err := written.Directory("target").Entries(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"new.txt"}, entries)

				changes := written.Changes(dagger.WorkspaceChangesOpts{From: ws})
				removed, err := changes.RemovedPaths(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"target/keep.txt"}, removed)

				added, err := changes.AddedPaths(ctx)
				require.NoError(t, err)
				require.Contains(t, added, "target/new.txt")
			})

			t.Run("replaces the workspace root", func(ctx context.Context, t *testctx.T) {
				written := ws.WithNewDirectory("/", source)

				entries, err := written.Directory("/").Entries(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"new.txt"}, entries)

				// The whole directory goes, so it is reported as the directory
				// rather than file by file.
				removed, err := written.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
				require.NoError(t, err)
				require.Contains(t, removed, "target/")
			})

			t.Run("creates a path the workspace does not have", func(ctx context.Context, t *testctx.T) {
				written := ws.WithNewDirectory("fresh/nested", source)

				contents, err := written.File("fresh/nested/new.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "new", contents)

				removed, err := written.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
				require.NoError(t, err)
				require.Empty(t, removed)
			})

			// Replacement covers the overlay's own earlier writes at the path,
			// not just the workspace's base content: on a host-backed workspace
			// those live in the delta root the edit is applied to.
			t.Run("drops earlier edits under the path", func(ctx context.Context, t *testctx.T) {
				written := ws.WithNewFile("target/staged.txt", "staged").
					WithNewDirectory("target", source)

				entries, err := written.Directory("target").Entries(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"new.txt"}, entries)

				removed, err := written.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
				require.NoError(t, err)
				require.Equal(t, []string{"target/keep.txt"}, removed)
			})
		})
	}
}

// A host-backed overlay's changeset is diffed against a host base re-read at
// diff time, while its other side accumulates across edits. Nothing the caller
// never wrote may end up pinned on that accumulating side, or a file the user
// edits on disk mid-session comes back reported as modified — and exporting
// then writes the stale bytes over their edit.
func (WorkspaceSuite) TestWorkspaceWithDirectoryDoesNotPinHostContent(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "target"), 0o755))
	keepPath := filepath.Join(workdir, "target", "keep.txt")
	require.NoError(t, os.WriteFile(keepPath, []byte("keep"), 0o644))

	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	ws := c.CurrentWorkspace()
	source := c.Directory().WithNewFile("new.txt", "new")

	merged := ws.WithDirectory("target", source)
	_, err := merged.Changes(dagger.WorkspaceChangesOpts{From: ws}).AddedPaths(ctx)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(keepPath, []byte("edited on disk"), 0o644))

	t.Run("a later edit", func(ctx context.Context, t *testctx.T) {
		changes := merged.WithNewFile("elsewhere.txt", "x").
			Changes(dagger.WorkspaceChangesOpts{From: ws})
		modified, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.NotContains(t, modified, "target/keep.txt")
	})

	t.Run("a later edit after reloading", func(ctx context.Context, t *testctx.T) {
		changes := merged.Reloaded().WithNewFile("elsewhere.txt", "x").
			Changes(dagger.WorkspaceChangesOpts{From: ws})
		modified, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.NotContains(t, modified, "target/keep.txt")
	})
}
