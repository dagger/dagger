package core

import (
	"context"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const workspaceRegressionTimeout = 30 * time.Second

// These tests define the source-backed Workspace contract. A Workspace has a
// private source backend internally, but callers only see Workspace behavior:
// filesystem reads, git state, module/config behavior, and functional updates.

// TestSyntheticWorkspaceSourceIsPrivateInSchema asserts that the backend source
// is an implementation detail. The schema should expose constructors and
// behavior, not a public backend enum or source-discriminator field.
func (WorkspaceSuite) TestSyntheticWorkspaceSourceIsPrivateInSchema(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	res, err := testutil.QueryWithClient[syntheticWorkspaceSchemaResult](c, t, `{
		workspace: __type(name: "Workspace") {
			fields {
				name
			}
		}
		directory: __type(name: "Directory") {
			fields {
				name
			}
		}
		gitRef: __type(name: "GitRef") {
			fields {
				name
			}
		}
		schema: __schema {
			types {
				name
			}
		}
	}`, nil)
	require.NoError(t, err)

	requireGraphQLField(t, res.Directory.Fields, "asWorkspace")
	requireGraphQLField(t, res.GitRef.Fields, "asWorkspace")
	requireGraphQLField(t, res.Workspace.Fields, "withNewFile")
	requireGraphQLField(t, res.Workspace.Fields, "withNewDirectory")
	requireGraphQLField(t, res.Workspace.Fields, "withChanges")
	requireGraphQLField(t, res.Workspace.Fields, "changes")

	for _, field := range []string{"backend", "backendKind", "source", "sourceKind", "workspaceSource", "hostPath", "rootfs", "clientID", "clientId"} {
		requireNoGraphQLField(t, res.Workspace.Fields, field)
	}
	requireNoGraphQLType(t, res.Schema.Types, "WorkspaceSource")
	requireNoGraphQLType(t, res.Schema.Types, "WorkspaceBackend")
}

// TestDirectoryBackedSyntheticWorkspaceUsesSourceContent asserts the core
// caller contract for Directory.asWorkspace: the supplied Directory is the
// workspace source. Filesystem APIs resolve from cwd, absolute paths resolve
// from the source root, and filters run against source content rather than a
// host workspace.
func (WorkspaceSuite) TestDirectoryBackedSyntheticWorkspaceUsesSourceContent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/app/nested",
	})

	cwd, err := ws.Cwd(ctx)
	require.NoError(t, err)
	require.Equal(t, "/app/nested", cwd)

	leaf, err := ws.File("leaf.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "leaf", leaf)

	root, err := ws.File("/README.md").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "root readme", root)

	found, err := ws.FindUp(ctx, "workspace.marker")
	require.NoError(t, err)
	require.Equal(t, "/workspace.marker", found)

	filtered, err := ws.Directory("/app", dagger.WorkspaceDirectoryOpts{Gitignore: true}).Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, filtered, "main.txt")
	requireEntry(t, filtered, "nested")
	requireNoEntry(t, filtered, "debug.log")

	unfiltered, err := ws.Directory("/app").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, unfiltered, "debug.log")
}

// TestGitRefBackedSyntheticWorkspaceUsesSelectedRef asserts the git-source
// contract: GitRef.asWorkspace keeps the selected ref as the source of truth.
// Filesystem reads come from that ref, ignored files that were never committed
// are absent, and workspace.git reports clean git state without depending on a
// materialized .git directory.
func (WorkspaceSuite) TestGitRefBackedSyntheticWorkspaceUsesSelectedRef(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := syntheticWorkspaceGitRef(ctx, t, c)
	refID, err := ref.ID(ctx)
	require.NoError(t, err)

	loadedRef := dagger.Ref[*dagger.GitRef](c, refID)
	commit, err := loadedRef.Commit(ctx)
	require.NoError(t, err)

	ws := loadedRef.AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "/app"})

	cwd, err := ws.Cwd(ctx)
	require.NoError(t, err)
	require.Equal(t, "/app", cwd)

	main, err := ws.File("main.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "app main", main)

	root, err := ws.File("/README.md").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "root readme", root)

	filtered, err := ws.Directory(".", dagger.WorkspaceDirectoryOpts{Gitignore: true}).Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, filtered, "main.txt")
	requireNoEntry(t, filtered, "debug.log")

	unfiltered, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireNoEntry(t, unfiltered, "debug.log")

	head, err := ws.Git().Head().Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(commit), strings.TrimSpace(head))

	empty, err := ws.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)
}

// TestGitRefBackedSyntheticWorkspaceRoundTripsFromID asserts the simplest ID
// contract for GitRef.asWorkspace: a workspace returned from a Git ref can be
// saved as an ID, loaded again, and still reads files from that Git ref.
func (WorkspaceSuite) TestGitRefBackedSyntheticWorkspaceRoundTripsFromID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := syntheticWorkspaceGitRef(ctx, t, c)
	refID, err := ref.ID(ctx)
	require.NoError(t, err)

	controlCtx, cancel := context.WithTimeout(ctx, workspaceRegressionTimeout)
	defer cancel()

	loadedRef := dagger.Ref[*dagger.GitRef](c, refID)

	commit, err := loadedRef.Commit(controlCtx)
	require.NoError(t, err)

	directMain, err := loadedRef.
		Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).
		File("app/main.txt").
		Contents(controlCtx)
	require.NoError(t, err, "direct GitRef.tree read should work before GitRef.asWorkspace ID round-trip")
	require.Equal(t, "app main", directMain)

	queryCtx, cancel := context.WithTimeout(ctx, workspaceRegressionTimeout)
	defer cancel()

	workspaceID, err := loadedRef.
		AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "/app"}).
		ID(queryCtx)
	require.NoError(t, err)

	loaded := dagger.Ref[*dagger.Workspace](c, workspaceID)

	cwd, err := loaded.Cwd(queryCtx)
	require.NoError(t, err)
	require.Equal(t, "/app", cwd)

	main, err := loaded.File("main.txt").Contents(queryCtx)
	require.NoError(t, err)
	require.Equal(t, "app main", main)

	root, err := loaded.File("/README.md").Contents(queryCtx)
	require.NoError(t, err)
	require.Equal(t, "root readme", root)

	head, err := loaded.Git().Head().Commit(queryCtx)
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(commit), strings.TrimSpace(head))

	empty, err := loaded.Git().Uncommitted().IsEmpty(queryCtx)
	require.NoError(t, err)
	require.True(t, empty)
}

// TestOverlayWorkspaceFunctionalWritesDoNotMutateBaseSource asserts the future
// functional-write contract. Writing to a Workspace returns an overlay
// Workspace; the base source remains readable and unchanged.
func (WorkspaceSuite) TestOverlayWorkspaceFunctionalWritesDoNotMutateBaseSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ws := c.Directory().
		WithNewFile("app/base.txt", "base").
		AsWorkspace(dagger.DirectoryAsWorkspaceOpts{Cwd: "/app"})

	before, err := ws.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base", before)

	beforeEntries, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, beforeEntries, "base.txt")
	requireNoEntry(t, beforeEntries, "new.txt")

	changed := ws.WithNewFile("base.txt", "changed")
	after, err := changed.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "changed", after)

	added := changed.WithNewFile("new.txt", "new")
	newFile, err := added.File("new.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "new", newFile)

	afterEntries, err := added.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, afterEntries, "base.txt")
	requireEntry(t, afterEntries, "new.txt")

	afterBase, err := ws.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base", afterBase)

	afterBaseEntries, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, afterBaseEntries, "base.txt")
	requireNoEntry(t, afterBaseEntries, "new.txt")
}

// TestOverlayWorkspaceFunctionalWritesRoundTripFromID asserts that each
// functional write returns a real Workspace ID. Loading the ID should show the
// file introduced by that one write.
func (WorkspaceSuite) TestOverlayWorkspaceFunctionalWritesRoundTripFromID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseDir := c.Directory().WithNewFile("base.txt", "base")
	baseWorkspace := baseDir.AsWorkspace()
	sourceDir := c.Directory().WithNewFile("nested.txt", "nested")
	changedDir := baseDir.WithNewFile("patched.txt", "patched")
	changes := changedDir.Changes(baseDir)

	for _, tc := range []struct {
		name  string
		apply func(*dagger.Workspace) *dagger.Workspace
		path  string
		want  string
	}{
		{
			name: "withNewFile",
			apply: func(ws *dagger.Workspace) *dagger.Workspace {
				return ws.WithNewFile("file.txt", "file")
			},
			path: "file.txt",
			want: "file",
		},
		{
			name: "withNewDirectory",
			apply: func(ws *dagger.Workspace) *dagger.Workspace {
				return ws.WithNewDirectory("dir", sourceDir)
			},
			path: "dir/nested.txt",
			want: "nested",
		},
		{
			name: "withChanges",
			apply: func(ws *dagger.Workspace) *dagger.Workspace {
				return ws.WithChanges(changes)
			},
			path: "patched.txt",
			want: "patched",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			queryCtx, cancel := context.WithTimeout(ctx, workspaceRegressionTimeout)
			defer cancel()

			workspaceID, err := tc.apply(baseWorkspace).ID(queryCtx)
			require.NoError(t, err)

			loadCtx, cancel := context.WithTimeout(ctx, workspaceRegressionTimeout)
			defer cancel()

			got, err := dagger.Ref[*dagger.Workspace](c, workspaceID).
				File(tc.path).
				Contents(loadCtx)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestOverlayGitRefWorkspaceReportsOverlayAsUncommitted asserts how functional
// writes compose with git state: the overlay keeps the base ref's commit and
// reports the overlay as uncommitted workspace state.
func (WorkspaceSuite) TestOverlayGitRefWorkspaceReportsOverlayAsUncommitted(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := syntheticWorkspaceGitRef(ctx, t, c)
	refID, err := ref.ID(ctx)
	require.NoError(t, err)

	loadedRef := dagger.Ref[*dagger.GitRef](c, refID)
	commit, err := loadedRef.Commit(ctx)
	require.NoError(t, err)
	baseCommit := strings.TrimSpace(commit)

	ws := loadedRef.AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "/app"})

	cleanHead, err := ws.Git().Head().Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, baseCommit, strings.TrimSpace(cleanHead))

	cleanEmpty, err := ws.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, cleanEmpty)

	changed := ws.WithNewFile("overlay.txt", "overlay")
	overlayFile, err := changed.File("overlay.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "overlay", overlayFile)

	changedHead, err := changed.Git().Head().Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, baseCommit, strings.TrimSpace(changedHead))

	changedEmpty, err := changed.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, changedEmpty)
}

// TestChainedOverlayGitRefWorkspaceReportsAllOverlayChanges asserts that
// uncommitted state is cumulative over nested overlays. A Git-backed workspace
// with two functional writes should report both writes as uncommitted, not just
// the diff from the immediate parent overlay to the latest overlay.
func (WorkspaceSuite) TestChainedOverlayGitRefWorkspaceReportsAllOverlayChanges(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := syntheticWorkspaceGitRef(ctx, t, c)
	refID, err := ref.ID(ctx)
	require.NoError(t, err)

	queryCtx, cancel := context.WithTimeout(ctx, workspaceRegressionTimeout)
	defer cancel()

	changed := dagger.Ref[*dagger.GitRef](c, refID).
		AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "/app"}).
		WithNewFile("a.txt", "a").
		WithNewFile("b.txt", "b")

	a, err := changed.File("a.txt").Contents(queryCtx)
	require.NoError(t, err)
	require.Equal(t, "a", a)

	b, err := changed.File("b.txt").Contents(queryCtx)
	require.NoError(t, err)
	require.Equal(t, "b", b)

	addedPaths, err := changed.Git().Uncommitted().AddedPaths(queryCtx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"app/a.txt", "app/b.txt"}, addedPaths)
}

// TestSyntheticWorkspaceManagementAPIsDoNotDependOnHostState asserts that
// value workspaces do not accidentally route non-filesystem workspace APIs
// through the caller's current session. Listing APIs return empty results
// because no module graph is loaded; local-only mutations reject.
func (WorkspaceSuite) TestSyntheticWorkspaceManagementAPIsDoNotDependOnHostState(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace()

	assertSyntheticWorkspaceListsAreEmpty(ctx, t, ws)

	_, err := ws.Install(ctx, "github.com/dagger/dagger/modules/wolfi")
	require.Error(t, err)
}

// TestSyntheticWorkspaceFindUpValidatesNames asserts that Workspace.findUp
// searches for one path element while walking parents. Slash and parent
// segments would turn a name lookup into path traversal, but "." is kept as the
// current-directory sentinel used by existing SDK code.
func (WorkspaceSuite) TestSyntheticWorkspaceFindUpValidatesNames(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/app/nested",
	})

	currentDir, err := ws.FindUp(ctx, ".")
	require.NoError(t, err)
	require.Equal(t, "/app/nested", currentDir)

	for _, name := range []string{"", "..", "../workspace.marker", "nested/leaf.txt", `nested\leaf.txt`} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			_, err := ws.FindUp(ctx, name)
			require.Error(t, err)
		})
	}
}

func syntheticWorkspaceSource(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile(".gitignore", "*.log\nbuild/\n").
		WithNewFile("README.md", "root readme").
		WithNewFile("workspace.marker", "root marker").
		WithNewFile("root.log", "ignored").
		WithNewFile("build/root.bin", "ignored").
		WithNewFile("app/main.txt", "app main").
		WithNewFile("app/debug.log", "ignored").
		WithNewFile("app/nested/leaf.txt", "leaf")
}

func syntheticWorkspaceGitRef(ctx context.Context, t *testctx.T, c *dagger.Client) *dagger.GitRef {
	t.Helper()
	gitDaemon, repoURL := gitService(ctx, t, c, syntheticWorkspaceSource(c))
	return c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).Head()
}

func assertSyntheticWorkspaceListsAreEmpty(ctx context.Context, t *testctx.T, ws *dagger.Workspace) {
	t.Helper()

	checks, err := ws.Checks().List(ctx)
	require.NoError(t, err)
	require.Empty(t, checks)

	generators, err := ws.Generators().List(ctx)
	require.NoError(t, err)
	require.Empty(t, generators)

	services, err := ws.Services().List(ctx)
	require.NoError(t, err)
	require.Empty(t, services)

	modules, err := ws.ModuleList(ctx)
	require.NoError(t, err)
	require.Empty(t, modules)

	envs, err := ws.EnvList(ctx)
	require.NoError(t, err)
	require.Empty(t, envs)
}

func requireGraphQLField(t require.TestingT, fields []graphqlField, name string) {
	if hasGraphQLField(fields, name) {
		return
	}
	require.Failf(t, "missing GraphQL field", "expected field %q in %v", name, graphqlFieldNames(fields))
}

func requireNoGraphQLField(t require.TestingT, fields []graphqlField, name string) {
	if !hasGraphQLField(fields, name) {
		return
	}
	require.Failf(t, "unexpected GraphQL field", "did not expect field %q in %v", name, graphqlFieldNames(fields))
}

func requireNoGraphQLType(t require.TestingT, types []graphqlType, name string) {
	for _, typ := range types {
		if typ.Name == name {
			require.Failf(t, "unexpected GraphQL type", "did not expect type %q in schema", name)
		}
	}
}

func hasGraphQLField(fields []graphqlField, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func graphqlFieldNames(fields []graphqlField) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return names
}

func requireEntry(t require.TestingT, entries []string, name string) {
	if hasWorkspaceEntry(entries, name) {
		return
	}
	require.Failf(t, "missing workspace entry", "expected %q in %v", name, entries)
}

func requireNoEntry(t require.TestingT, entries []string, name string) {
	if !hasWorkspaceEntry(entries, name) {
		return
	}
	require.Failf(t, "unexpected workspace entry", "did not expect %q in %v", name, entries)
}

func hasWorkspaceEntry(entries []string, name string) bool {
	for _, entry := range entries {
		if entry == name || entry == name+"/" {
			return true
		}
	}
	return false
}

type syntheticWorkspaceSchemaResult struct {
	Workspace graphqlType `json:"workspace"`
	Directory graphqlType `json:"directory"`
	GitRef    graphqlType `json:"gitRef"`
	Schema    struct {
		Types []graphqlType `json:"types"`
	} `json:"schema"`
}

type graphqlType struct {
	Name   string         `json:"name"`
	Fields []graphqlField `json:"fields"`
}

type graphqlField struct {
	Name string `json:"name"`
}

type directoryEntries struct {
	Entries []string `json:"entries"`
}
