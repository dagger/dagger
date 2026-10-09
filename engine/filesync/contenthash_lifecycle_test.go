package filesync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	ctdmount "github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcontenthash "github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/fsutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
)

func TestSyncContentHashesSurviveEviction(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	client, err := fsutil.NewFS(contenthashFixture(t))
	assert.NilError(t, err)
	local, err := newLocalFS(NewMirrorSharedState(t.TempDir()), "", nil, nil, nil, "")
	assert.NilError(t, err)
	for _, phase := range []string{"cold import", "warm import"} {
		t.Run(phase, func(t *testing.T) {
			ref, owner := syncPublished(t, store, local, client)
			defer store.Manager.RemoveLease(context.Background(), owner)
			defer ref.Release(context.Background())
			want, err := bkcontenthash.Checksum(ctx, ref, "/pkg", bkcontenthash.ChecksumOpts{})
			assert.NilError(t, err)
			md, ok := any(ref).(bkcache.RefMetadata)
			assert.Assert(t, ok)
			bkcontenthash.ClearCacheContext(md)
			got, err := bkcontenthash.Checksum(ctx, ref, "/pkg", bkcontenthash.ChecksumOpts{})
			assert.NilError(t, err)
			assert.Equal(t, got, want)
		})
	}
}

func TestSyncContentHashesSurviveCacheRestart(t *testing.T) {
	const session = "contenthash-restart"
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: session, SessionID: session})
	store := testutil.NewStore(t)
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cache, err := dagql.NewCache(ctx, dbPath, store.Manager, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, cache.CloseDiscardingPersistence()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv, err := dagql.NewServer(ctx, contenthashQuery{})
	assert.NilError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*contenthashSnapshot]{}))
	client, err := fsutil.NewFS(contenthashFixture(t))
	assert.NilError(t, err)
	local, err := newLocalFS(NewMirrorSharedState(t.TempDir()), "", nil, nil, nil, "")
	assert.NilError(t, err)
	ref, owner := syncPublished(t, store, local, client)
	defer func() { assert.NilError(t, store.Manager.RemoveLease(ctx, owner)) }()
	md := any(ref).(bkcache.RefMetadata)
	paths := []string{"/", "/pkg", "/pkg/src/lib.rs", "/pkg/hardlink", "/pkg/link", "/pkg/empty"}
	want := map[string]digest.Digest{}
	for _, path := range paths {
		want[path], err = bkcontenthash.Checksum(ctx, ref, path, bkcontenthash.ChecksumOpts{})
		assert.NilError(t, err)
	}
	root := testutil.Root(t, ref)
	snapshotID := ref.SnapshotID()
	value := &contenthashSnapshot{snapshotID: snapshotID}
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "importedSnapshot", Type: dagql.NewResultCallType(value.Type())}
	_, err = cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(value, srv, call)
	})
	assert.NilError(t, err)
	assert.NilError(t, ref.Release(ctx))
	bkcontenthash.ClearCacheContext(md)
	assert.NilError(t, store.Manager.RemoveLease(ctx, owner))
	assert.NilError(t, cache.ReleaseSession(ctx, session))
	assert.NilError(t, cache.Close(ctx))

	// Reopen twice. The second checkpoint happens before accessing the
	// snapshot, so hash records must also survive lazy metadata rehydration.
	for range 2 {
		store.Reload(t)
		// Discard testutil's in-memory copy; only the real SQLite checkpoint
		// may restore the imported hash tree.
		assert.NilError(t, store.Manager.LoadPersistentMetadata(bkcache.PersistentMetadataRows{}))
		cache, err = dagql.NewCache(ctx, dbPath, store.Manager, nil)
		assert.NilError(t, err)
		t.Cleanup(func() { assert.NilError(t, cache.CloseDiscardingPersistence()) })
		assert.NilError(t, cache.Close(ctx))
	}

	ctx, release, err := bkcache.WithLazyLease(ctx, store.Leases, bkcache.MakeTemporary)
	assert.NilError(t, err)
	defer release(context.Background())
	reopened, err := store.Manager.LeaseExistingSnapshot(ctx, snapshotID)
	assert.NilError(t, err)
	defer reopened.Release(context.Background())
	reopenedMD := any(reopened).(bkcache.RefMetadata)
	cc, err := bkcontenthash.GetCacheContext(ctx, reopenedMD)
	assert.NilError(t, err)
	mounts := &checksumBindRoot{path: root}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			got, err := cc.Checksum(ctx, mounts, path, bkcontenthash.ChecksumOpts{})
			assert.NilError(t, err)
			assert.Equal(t, got, want[path])
		})
	}
	assert.Equal(t, mounts.count, 0, "restored hash records should avoid a filesystem rescan")
}

type contenthashQuery struct{}

func (contenthashQuery) Type() *ast.Type {
	return &ast.Type{NamedType: "Query", NonNull: true}
}

type contenthashSnapshot struct{ snapshotID string }

func init() {
	dagql.RegisterPersistedObjectFamily(dagql.PersistedObjectFamily{
		Name: "filesync_test.ContenthashSnapshot", Typed: (*contenthashSnapshot)(nil), Visitor: contenthashSnapshot{},
	})
}

func (contenthashSnapshot) VisitPersistedReferences(v dagql.PersistedPayloadVisit, visit dagql.PersistedRefVisitor) (json.RawMessage, error) {
	if err := dagql.VisitPersistedSnapshotRoles(visit, dagql.PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	return v.Payload, nil
}

func (*contenthashSnapshot) Type() *ast.Type {
	return &ast.Type{NamedType: "ContenthashSnapshot", NonNull: true}
}

func (v *contenthashSnapshot) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	return []dagql.PersistedSnapshotRefLink{{RefKey: v.snapshotID, Role: "snapshot"}}
}

func (v *contenthashSnapshot) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return dagql.PersistedObjectEncoding{JSON: []byte(`{}`), SnapshotLinks: v.PersistedSnapshotRefLinks()}, nil
}

func contenthashFixture(t *testing.T) string {
	t.Helper()
	root := writeTree(t, map[string]string{
		"pkg/src/lib.rs": "pub fn value() -> u32 { 42 }\n",
		"pkg/Cargo.toml": "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\n",
		"pkg/empty":      "",
	})
	assert.NilError(t, os.Chmod(filepath.Join(root, "pkg/src/lib.rs"), 0751))
	assert.NilError(t, os.Link(filepath.Join(root, "pkg/src/lib.rs"), filepath.Join(root, "pkg/hardlink")))
	assert.NilError(t, os.Symlink("src/lib.rs", filepath.Join(root, "pkg/link")))
	return root
}

// The store's real snapshot is a host directory. Like testutil's applier and
// differ, use that directory directly to avoid a privileged read-only bind.
type checksumBindRoot struct {
	path  string
	count int
}

func (root *checksumBindRoot) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	root.count++
	return checksumBindMounts(root.path), nil
}

type checksumBindMounts string

func (root checksumBindMounts) Mount() ([]ctdmount.Mount, func() error, error) {
	return []ctdmount.Mount{{Type: "bind", Source: string(root), Options: []string{"rbind"}}}, func() error { return nil }, nil
}
