package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/opencontainers/go-digest"
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
	commit, err := loadedRef.CommitSHA(ctx)
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

	head, err := ws.Git().Head().CommitSHA(ctx)
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

	commit, err := loadedRef.CommitSHA(controlCtx)
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

	head, err := loaded.Git().Head().CommitSHA(queryCtx)
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

// TestOverlayWorkspaceFunctionalRemovesDoNotMutateBaseSource asserts the
// functional-remove contract mirrors Directory.withoutFile /
// Directory.withoutDirectory: removing from a Workspace returns an overlay
// Workspace whose reads reflect the removal, while the base source remains
// readable and unchanged.
func (WorkspaceSuite) TestOverlayWorkspaceFunctionalRemovesDoNotMutateBaseSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ws := c.Directory().
		WithNewFile("app/keep.txt", "keep").
		WithNewFile("app/drop.txt", "drop").
		WithNewFile("app/sub/inner.txt", "inner").
		AsWorkspace(dagger.DirectoryAsWorkspaceOpts{Cwd: "/app"})

	withoutFile := ws.WithoutFile("drop.txt")
	fileEntries, err := withoutFile.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, fileEntries, "keep.txt")
	requireNoEntry(t, fileEntries, "drop.txt")

	removed, err := withoutFile.Changes(dagger.WorkspaceChangesOpts{From: ws}).RemovedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, removed, "drop.txt")

	withoutDir := ws.WithoutDirectory("sub")
	dirEntries, err := withoutDir.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, dirEntries, "keep.txt")
	requireNoEntry(t, dirEntries, "sub")

	// The base source is untouched by either removal.
	baseEntries, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, baseEntries, "keep.txt")
	requireEntry(t, baseEntries, "drop.txt")
	requireEntry(t, baseEntries, "sub")
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
	commit, err := loadedRef.CommitSHA(ctx)
	require.NoError(t, err)
	baseCommit := strings.TrimSpace(commit)

	ws := loadedRef.AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "/app"})

	cleanHead, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseCommit, strings.TrimSpace(cleanHead))

	cleanEmpty, err := ws.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, cleanEmpty)

	changed := ws.WithNewFile("overlay.txt", "overlay")
	overlayFile, err := changed.File("overlay.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "overlay", overlayFile)

	changedHead, err := changed.Git().Head().CommitSHA(ctx)
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
// value workspaces read and build against their own snapshot rather than the
// caller's current session. Only export remains local-Git-specific.
func (WorkspaceSuite) TestSyntheticWorkspaceManagementAPIsDoNotDependOnHostState(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace()

	assertSyntheticWorkspaceListsAreEmpty(ctx, t, ws)

	updated := ws.WithModule("github.com/dagger/dagger/modules/wolfi@v0.20.2")
	added, err := updated.Changes(dagger.WorkspaceChangesOpts{From: ws}).AddedPaths(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"dagger.lock", "dagger.toml"}, added)

	modules, err := updated.Modules(ctx)
	require.NoError(t, err)
	require.Len(t, modules, 1)
	name, err := modules[0].Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "wolfi", name)

	baseModules, err := ws.Modules(ctx)
	require.NoError(t, err)
	require.Empty(t, baseModules)

	err = updated.Export(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "cannot export a synthetic workspace")
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

func (WorkspaceSuite) TestGitCheckpointReconstructsPendingHistoryAndWorktree(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("tracked.txt", "base\n"))
	baseRef := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).Head()
	baseSHA, err := baseRef.CommitSHA(ctx)
	require.NoError(t, err)
	baseID, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).Ref(baseSHA).ID(ctx)
	require.NoError(t, err)

	const (
		firstDate  = "2026-01-02T03:04:05Z"
		secondDate = "2026-01-02T04:05:06Z"
	)
	packed := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding("checkpoint-git", gitDaemon).
		WithEnvVariable("REPO_URL", repoURL).
		WithEnvVariable("BASE_SHA", baseSHA).
		WithEnvVariable("FIRST_DATE", firstDate).
		WithEnvVariable("SECOND_DATE", secondDate).
		WithExec([]string{"sh", "-ec", `
			git clone "$REPO_URL" /repo
			cd /repo
			git config user.name Checkpoint
			git config user.email checkpoint@example.com
			printf 'one\n' > one.txt
			git add one.txt
			GIT_AUTHOR_DATE="$FIRST_DATE" GIT_COMMITTER_DATE="$FIRST_DATE" git commit -m 'local one'
			printf 'two\n' > two.txt
			git add two.txt
			GIT_AUTHOR_DATE="$SECOND_DATE" GIT_COMMITTER_DATE="$SECOND_DATE" git commit -m 'local two'
			git rev-list --reverse "$BASE_SHA"..HEAD > /commits
			git update-ref refs/heads/checkpoint HEAD
			git bundle create /bundle refs/heads/checkpoint "^$BASE_SHA"
			printf 'dirty\n' >> tracked.txt
			git diff --binary HEAD > /worktree.patch
			index=$(mktemp)
			rm -f "$index"
			GIT_INDEX_FILE="$index" git read-tree HEAD
			GIT_INDEX_FILE="$index" git add -A -f -- .
			GIT_INDEX_FILE="$index" git write-tree > /worktree.tree
			base64 /bundle | tr -d '\n' > /bundle.b64
			base64 /worktree.patch | tr -d '\n' > /worktree.b64
		`})

	bundle64, err := packed.File("/bundle.b64").Contents(ctx)
	require.NoError(t, err)
	bundle, err := base64.StdEncoding.DecodeString(strings.TrimSpace(bundle64))
	require.NoError(t, err)
	patch64, err := packed.File("/worktree.b64").Contents(ctx)
	require.NoError(t, err)
	patch, err := base64.StdEncoding.DecodeString(strings.TrimSpace(patch64))
	require.NoError(t, err)
	commitList, err := packed.File("/commits").Contents(ctx)
	require.NoError(t, err)
	commits := strings.Fields(commitList)
	require.Len(t, commits, 2)
	worktreeTree, err := packed.File("/worktree.tree").Contents(ctx)
	require.NoError(t, err)
	worktreeTree = strings.TrimSpace(worktreeTree)

	payload := func(data []byte) core.WorkspaceCheckpointPayload {
		d := digest.FromBytes(data).String()
		chunks := []core.WorkspaceCheckpointChunkDescriptor{}
		if len(data) > 0 {
			chunks = append(chunks, core.WorkspaceCheckpointChunkDescriptor{Size: len(data), Digest: d})
		}
		return core.WorkspaceCheckpointPayload{Size: int64(len(data)), Digest: d, Chunks: chunks}
	}
	manifest := core.WorkspaceGitCheckpointManifest{
		Version:              core.WorkspaceCheckpointFormatVersion,
		ObjectFormat:         "sha1",
		BaseSHA:              baseSHA,
		HeadSHA:              commits[1],
		BundleRef:            "refs/heads/checkpoint",
		Bundle:               payload(bundle),
		Worktree:             payload(patch),
		WorktreeTree:         worktreeTree,
		CapturePolicyVersion: "test-v1",
		Workspace: core.WorkspaceCheckpointWorkspace{
			Address:        "git-checkpoint://integration",
			Cwd:            ".",
			GitAuthorName:  "Checkpoint",
			GitAuthorEmail: "checkpoint@example.com",
		},
		Commits: []core.WorkspaceBundledCommit{
			{SHA: commits[0], Message: "local one", Date: firstDate, AuthorName: "Checkpoint", AuthorEmail: "checkpoint@example.com", Paths: []string{"one.txt"}},
			{SHA: commits[1], Message: "local two", Date: secondDate, AuthorName: "Checkpoint", AuthorEmail: "checkpoint@example.com", Paths: []string{"two.txt"}},
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	chunkID := func(data []byte) string {
		encoded, err := json.Marshal(data)
		require.NoError(t, err)
		res, err := testutil.QueryWithClient[struct {
			Chunk struct{ ID string }
		}](c, t, `query($data: String!) { chunk: _workspaceCheckpointChunk(data: $data) { id } }`, &testutil.QueryOptions{Variables: map[string]any{
			"data": string(encoded),
		}})
		require.NoError(t, err)
		return res.Chunk.ID
	}
	bundleChunk := chunkID(bundle)
	worktreeChunk := chunkID(patch)
	encodedManifest, err := json.Marshal(string(manifestJSON))
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Workspace struct {
			Cwd  string
			One  struct{ Contents string }
			Two  struct{ Contents string }
			Dirt struct{ Contents string }
			Git  struct {
				Head          struct{ Commit string }
				StagedCommits []struct {
					SHA     string
					Message string
				}
				Uncommitted struct{ ModifiedPaths []string }
			}
		}
	}](c, t, `query($base: ID!, $manifest: String!, $chunks: [ID!]!) {
		workspace: _workspaceFromGitCheckpoint(base: $base, manifest: $manifest, chunks: $chunks) {
			cwd
			one: file(path: "one.txt") { contents }
			two: file(path: "two.txt") { contents }
			dirt: file(path: "tracked.txt") { contents }
			git {
				head { commit }
				stagedCommits { sha message }
				uncommitted { modifiedPaths }
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"base":     baseID,
		"manifest": string(encodedManifest),
		"chunks":   []string{bundleChunk, worktreeChunk},
	}})
	require.NoError(t, err)
	require.Equal(t, "/", res.Workspace.Cwd)
	require.Equal(t, "one\n", res.Workspace.One.Contents)
	require.Equal(t, "two\n", res.Workspace.Two.Contents)
	require.Equal(t, "base\ndirty\n", res.Workspace.Dirt.Contents)
	require.Equal(t, commits[1], res.Workspace.Git.Head.Commit)
	require.Equal(t, []string{commits[0], commits[1]}, []string{
		res.Workspace.Git.StagedCommits[0].SHA,
		res.Workspace.Git.StagedCommits[1].SHA,
	})
	require.Equal(t, []string{"local one", "local two"}, []string{
		res.Workspace.Git.StagedCommits[0].Message,
		res.Workspace.Git.StagedCommits[1].Message,
	})
	require.Equal(t, []string{"tracked.txt"}, res.Workspace.Git.Uncommitted.ModifiedPaths)
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

	modules, err := ws.Modules(ctx)
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
