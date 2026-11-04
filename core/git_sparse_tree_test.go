package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/util/gitutil"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// sparseManifestTestServer is the query's route to the dagql server, which is
// the only one a workspace load has: its context carries no server of its own.
type sparseManifestTestServer struct {
	queryServer
	srv *dagql.Server
}

type queryServer = Server

func (s *sparseManifestTestServer) Server(context.Context) (*dagql.Server, error) {
	return s.srv, nil
}

// TestGitSparseTreeManifestOfAMergedRecord reads the manifest of a sparse tree
// this engine holds only as another engine's record, as loading a workspace
// does: from a context with no dagql server. Failing here leaves the workspace
// reading its full checkout, whatever the sparse tree holds.
func TestGitSparseTreeManifestOfAMergedRecord(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, filepath.Join(t.TempDir(), "a.db"), "a")
	bctx, b, bsrv := transferCache(t, bStore, filepath.Join(t.TempDir(), "b.db"), "b")

	ref, _ := aStore.Build(t, nil, gitSparseManifestFile, "fdagger.toml\x00agone.txt\x00")
	original := attachTransferObject(t, actx, a, asrv, "a", "sparseTree", partTestDirectory(ref, "/"+gitSparseTreeDir))
	require.NoError(t, a.Evaluate(actx, original))

	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{
		Roots:   []dagql.AnyResult{original},
		Outputs: []dagql.SelectedValueOutput{{Result: original, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
	}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1)
		b.SetPartContentSource(partTestContentSource{&testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}})
		reply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		imported := reply.Imported()
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)

		query, err := CurrentQuery(bctx)
		require.NoError(t, err)
		ctx := ContextWithQuery(dagql.ContextWithServer(bctx, nil), &Query{
			Server: &sparseManifestTestServer{queryServer: query.Server, srv: bsrv},
		})
		sparse := &GitSparseTree{Root: loaded.(dagql.ObjectResult[*Directory])}

		covered, err := sparse.coversPath(ctx, "dagger.toml")
		require.NoError(t, err)
		require.True(t, covered)
		covered, err = sparse.coversPath(ctx, "gone.txt")
		require.NoError(t, err)
		require.False(t, covered)
		return nil
	}))
}

// withMergedGitWorkspace merges a Git workspace's record, with its sparse tree,
// from one cache into another, and passes use the workspace as the receiving
// engine loads it. Its sparse tree's content comes through the provider
// wrapProvider makes.
func withMergedGitWorkspace(
	t *testing.T,
	wrapProvider func(content.InfoReaderProvider) content.InfoReaderProvider,
	use func(ctx context.Context, bStore *testutil.Store, cache *dagql.Cache, srv *dagql.Server, ws *Workspace),
) {
	t.Helper()
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, filepath.Join(t.TempDir(), "a.db"), "a")
	bctx, b, bsrv := transferCache(t, bStore, filepath.Join(t.TempDir(), "b.db"), "b")
	for _, srv := range []*dagql.Server{asrv, bsrv} {
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRepository]{}))
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Workspace]{}))
	}

	url, err := gitutil.ParseURL("https://example.com/repo.git")
	require.NoError(t, err)
	remote := &RemoteGitRepository{URL: url, Platform: Platform{OS: "linux", Architecture: "amd64"}}
	repo := attachTransferObject(t, actx, a, asrv, "a", "repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}})
	gitRef := &gitutil.Ref{SHA: strings.Repeat("a", 40)}
	ref := attachTransferObject(t, actx, a, asrv, "a", "ref", &GitRef{Repo: repo, Ref: gitRef, Backend: &RemoteGitRef{Ref: gitRef, repo: remote}})

	snapshot, _ := aStore.Build(t, nil, gitSparseManifestFile, "fdagger.toml\x00agone.txt\x00")
	root := attachTransferObject(t, actx, a, asrv, "a", "sparseTree", partTestDirectory(snapshot, "/"+gitSparseTreeDir))
	require.NoError(t, a.Evaluate(actx, root))

	ws := &Workspace{Address: "https://example.com/repo.git", Cwd: "."}
	ws.SetSource(&WorkspaceSourceGitRef{Ref: ref.Result, Sparse: &GitSparseTree{Root: root}})
	original := attachTransferObject(t, actx, a, asrv, "a", "workspace", ws)

	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{
		Roots:   []dagql.AnyResult{original},
		Outputs: []dagql.SelectedValueOutput{{Result: root, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
	}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1)
		b.SetPartContentSource(partTestContentSource{wrapProvider(exported.Chains.Entries[0].Provider)})
		reply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		imported := reply.Imported()
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		use(bctx, bStore, b, bsrv, loaded.(dagql.ObjectResult[*Workspace]).Self())
		return nil
	}))
}

// TestGitWorkspaceKeepsItsSparseTreeInAMergedRecord merges a Git workspace's
// record between two caches. An engine can use such a record in place of the
// workspace it loaded, since the two have one identity. Without its sparse
// tree, the workspace reads its config from the full tree, which checks the
// whole repository out.
func TestGitWorkspaceKeepsItsSparseTreeInAMergedRecord(t *testing.T) {
	withMergedGitWorkspace(t, func(provider content.InfoReaderProvider) content.InfoReaderProvider {
		return provider
	}, func(ctx context.Context, _ *testutil.Store, _ *dagql.Cache, _ *dagql.Server, ws *Workspace) {
		sparse, ok := ws.GitSparseTree()
		require.True(t, ok, "a merged Git workspace must keep its sparse tree")
		covered, err := sparse.coversPath(ctx, "dagger.toml")
		require.NoError(t, err)
		require.True(t, covered)
		covered, err = sparse.coversPath(ctx, "gone.txt")
		require.NoError(t, err)
		require.False(t, covered)
	})
}

// TestGitWorkspaceReadsFullTreeWithoutSparseContent checks a merged workspace
// whose sparse tree's content can't be had: its reads must come from the full
// tree, with its files, and not fail. The record promises content the engine
// doesn't hold yet, so this is where a merged sparse tree can let a read down.
func TestGitWorkspaceReadsFullTreeWithoutSparseContent(t *testing.T) {
	withMergedGitWorkspace(t, func(provider content.InfoReaderProvider) content.InfoReaderProvider {
		return &testutil.Provider{
			InfoReaderProvider: provider,
			BeforeRead: func(context.Context, ocispec.Descriptor) error {
				return errPartInjectedProvider
			},
		}
	}, func(ctx context.Context, bStore *testutil.Store, cache *dagql.Cache, srv *dagql.Server, ws *Workspace) {
		sparse, ok := ws.GitSparseTree()
		require.True(t, ok, "a merged Git workspace must keep its sparse tree")

		fullRef, _ := bStore.Build(t, nil, "dagger.toml", "from the full tree")
		full := attachTransferObject(t, ctx, cache, srv, "b", "fullTree", partTestDirectory(fullRef, "/"))
		fs := &GitSparseTreeFS{Sparse: sparse, Full: full}

		// Twice: a failure must keep sending reads to the full tree, not only
		// the first one.
		for range 2 {
			require.Same(t, full.Self(), fs.dirFor(ctx, "dagger.toml").Self())
			require.Same(t, full.Self(), fs.dirFor(ctx, "gone.txt").Self())
			require.NoError(t, MountRef(ctx, fullRef, func(root string, _ *mount.Mount) error {
				got, err := os.ReadFile(filepath.Join(root, "dagger.toml"))
				require.NoError(t, err)
				require.Equal(t, "from the full tree", string(got))
				return nil
			}, mountRefAsReadOnly))

			// A filtered read's route: it reads the full tree on this error.
			_, err := sparse.CoversRead(ctx, ".", CopyFilter{Include: []string{"dagger.toml"}})
			require.ErrorIs(t, err, errPartInjectedProvider)
		}
	})
}
