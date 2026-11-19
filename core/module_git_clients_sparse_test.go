package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

type countedGitCheckoutOp struct {
	*LazyState
	ref   *countedGitCheckoutRef
	evals int
}

func (op *countedGitCheckoutOp) Evaluate(ctx context.Context, d *Directory) error {
	return op.LazyState.Evaluate(ctx, "CountedGitCheckout", func(context.Context) error {
		op.evals++
		d.SetPath("/")
		d.SetSnapshot(op.ref)
		return nil
	})
}

func (op *countedGitCheckoutOp) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (op *countedGitCheckoutOp) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	return nil, nil
}

type countedGitCheckoutRef struct {
	*operationTreeRef
	mounts int
}

func (ref *countedGitCheckoutRef) Mount(ctx context.Context, readonly bool) (bkcache.MountableRef, error) {
	ref.mounts++
	return ref.operationTreeRef.Mount(ctx, readonly)
}

func TestSparseDeclaredGitClientsMemoReplaysReadsWithoutFullCheckout(t *testing.T) {
	const client = "github.com/acme/private/hello"
	server := &gitClientsTestServer{mockServer: &mockServer{}, memo: NewSchemaBuilderMemo()}
	query := &Query{Server: server}
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Workspace]{}))
	trees := installGitClientsTestTrees(srv)
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	const session = "sparse-git-clients"
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: session, SessionID: session})
	ctx = ContextWithQuery(dagql.ContextWithServer(dagql.ContextWithCache(ctx, cache), srv), query)
	t.Cleanup(func() { _ = cache.ReleaseSession(context.Background(), session) })

	fullPath := t.TempDir()
	fullRef := &countedGitCheckoutRef{operationTreeRef: &operationTreeRef{
		cacheVolumeTestImmutableRef: &cacheVolumeTestImmutableRef{id: "full-checkout", snapshotID: "full-checkout"},
		root:                        fullPath,
	}}
	state := NewLazyState()
	op := &countedGitCheckoutOp{LazyState: &state, ref: fullRef}
	fullDir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Lazy: op}
	fullFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "full-checkout", Type: dagql.NewResultCallType(fullDir.Type())}
	fullDetached, err := dagql.NewObjectResultForCall(fullDir, srv, fullFrame)
	require.NoError(t, err)
	full, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: fullFrame}, dagql.ValueFunc(fullDetached))
	require.NoError(t, err)

	sparsePath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sparsePath, gitSparseManifestFile), []byte("fdagger.toml\x00"), 0600))
	sparseDir := containerPersistenceTestDirectory("sparse-checkout", "/"+gitSparseTreeDir)
	sparseDir.SetSnapshot(&operationTreeRef{cacheVolumeTestImmutableRef: &cacheVolumeTestImmutableRef{id: "sparse-checkout", snapshotID: "sparse-checkout"}, root: sparsePath})
	trees.trees[sparseDir] = map[string]string{"dagger.toml": gitClientsTestConfig(client)}
	sparseFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "sparse-checkout", Type: dagql.NewResultCallType(sparseDir.Type())}
	sparseDetached, err := dagql.NewObjectResultForCall(sparseDir, srv, sparseFrame)
	require.NoError(t, err)
	sparse, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: sparseFrame}, dagql.ValueFunc(sparseDetached))
	require.NoError(t, err)
	ws := &Workspace{
		Address: "https://example.com/repo",
		rootfs:  full.(dagql.ObjectResult[*Directory]),
		source:  &WorkspaceSourceGitRef{Sparse: &GitSparseTree{Root: sparse.(dagql.ObjectResult[*Directory])}},
	}
	wsFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "sparse-git-workspace", Type: dagql.NewResultCallType(ws.Type())}
	wsDetached, err := dagql.NewObjectResultForCall(ws, srv, wsFrame)
	require.NoError(t, err)
	data, err := json.Marshal(workspaceInputFact{Operation: workspaceInputOperation(ctx, wsFrame), Complete: true, CapabilitiesComplete: true, Boundary: "git-workspace:https://example.com/repo"})
	require.NoError(t, err)
	value, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: wsFrame, ResultFactsPrepared: true, ResultFacts: []dagql.ResultFact{{Namespace: workspaceInputsNamespace, Payload: data}}}, dagql.ValueFunc(wsDetached))
	require.NoError(t, err)
	src := &ModuleSource{Kind: ModuleSourceKindGit, SourceRootSubpath: "modules/caller", Git: &GitModuleSource{}, Workspace: value.(dagql.ObjectResult[*Workspace])}

	clients, err := ModuleDeclaredGitClients(ctx, srv, src)
	require.NoError(t, err)
	require.Equal(t, []string{client}, clients)
	require.Equal(t, 1, trees.reads)
	require.Zero(t, op.evals)
	require.Zero(t, fullRef.mounts)

	reads := &workspaceInputReads{}
	trackedCtx := context.WithValue(ctx, workspaceInputReadsKey{}, reads)
	clients, err = ModuleDeclaredGitClients(trackedCtx, srv, src)
	require.NoError(t, err)
	require.Equal(t, []string{client}, clients)
	require.Equal(t, 1, trees.reads, "the tracked call reused the declarations memo")
	require.Zero(t, op.evals)
	require.Zero(t, fullRef.mounts)

	selectors, issues := reads.snapshot().forWorkspace("git-workspace:https://example.com/repo")
	require.Empty(t, issues)
	var paths []string
	for _, selector := range selectors {
		require.False(t, selector.Conservative)
		paths = append(paths, selector.Path)
	}
	require.ElementsMatch(t, []string{"/modules/caller/dagger.toml", "/modules/dagger.toml", "/dagger.toml"}, paths)
}

func TestDeclaredGitClientSelectorsRequireUnaliasedSparsePaths(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		conservative   bool
	}{
		{"missing", "fdagger.toml\x00", false},
		{"regular", "fsub/dagger.toml\x00", false},
		{"symlink ancestor", "lsub\x00", true},
		{"symlink candidate", "lsub/dagger.toml\x00", true},
		{"submodule", "gsub\x00", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := new(GitSparseTree)
			tree.manifestOnce.Do(func() { tree.manifest = parseGitSparseManifest([]byte(tc.manifest)) })
			ws := &Workspace{source: &WorkspaceSourceGitRef{Sparse: tree}}
			probe := &declaredGitClientPaths{visited: []string{"sub/dagger.toml"}}
			sel := probe.forWorkspace(&workspaceInputCollector{ctx: t.Context()}, ws)
			require.Len(t, sel, 1)
			require.Equal(t, tc.conservative, sel[0].Conservative)
			if tc.conservative {
				require.Equal(t, "/", sel[0].Path)
			} else {
				require.Equal(t, "/sub/dagger.toml", sel[0].Path)
			}
		})
	}
}
