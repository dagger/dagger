package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
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
