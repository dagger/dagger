package filesync

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	bkcontenthash "github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkcontainerd "github.com/dagger/dagger/engine/snapshots/containerd"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/fsutil"
	"github.com/dagger/dagger/util/fsxutil"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestLocalCopyOnlyPaths(t *testing.T) {
	t.Parallel()

	only := map[string]struct{}{
		"project":               {},
		"project/README.md":     {},
		"project/src/main.go":   {},
		"projector/unrelated":   {},
		"elsewhere/ignored.txt": {},
	}

	got := localCopyOnlyPaths(only, "project")

	assert.Assert(t, is.DeepEqual(map[string]struct{}{
		"":            {},
		"README.md":   {},
		"src/main.go": {},
	}, got))
}

// Regression: with "foo/" ignored but "!foo/bar.txt" re-included, Sync skipped
// creating foo/ in the mirror and then failed to write foo/bar.txt.
func TestSyncReincludedFileUnderIgnoredParent(t *testing.T) {
	t.Parallel()

	clientRoot := writeTree(t, map[string]string{
		".gitignore":      "foo/\n!foo/bar.txt\n",
		"foo/bar.txt":     "keep",
		"foo/ignored.txt": "drop",
	})
	base, err := fsutil.NewFS(clientRoot)
	assert.NilError(t, err)
	client, err := fsxutil.NewGitIgnoreMarkedFS(base, nil)
	assert.NilError(t, err)

	mirrorRoot := t.TempDir()
	local, err := newLocalFS(NewMirrorSharedState(mirrorRoot), "", nil, nil, nil, "")
	assert.NilError(t, err)

	// Sync runs in two stages: write the client's files into the mirror, then
	// copy the mirror into an immutable snapshot. The bug was in the first
	// stage (foo/ was never created, so writing foo/bar.txt failed), so
	// forParents=true stops Sync right after it, keeping the test simple:
	// no cache manager needed (nil).
	_, _, err = local.Sync(context.Background(), readFS{client}, nil, true)
	assert.NilError(t, err)

	got, err := os.ReadFile(filepath.Join(mirrorRoot, "foo/bar.txt"))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "keep")

	_, err = os.ReadFile(filepath.Join(mirrorRoot, "foo/ignored.txt"))
	assert.Assert(t, os.IsNotExist(err))
}

// Verify that a full sync records the ignored parent directory it creates for
// a re-included child, so parent checksums do not report "not found".
func TestSyncReincludedFileUnderIgnoredParentCacheContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clientRoot := writeTree(t, map[string]string{
		".gitignore":      "foo/\n!foo/bar.txt\n",
		"foo/bar.txt":     "keep",
		"foo/ignored.txt": "drop",
	})
	base, err := fsutil.NewFS(clientRoot)
	assert.NilError(t, err)
	client, err := fsxutil.NewGitIgnoreMarkedFS(base, nil)
	assert.NilError(t, err)

	local, err := newLocalFS(NewMirrorSharedState(t.TempDir()), "", nil, nil, nil, "")
	assert.NilError(t, err)

	snapshotter, err := native.NewSnapshotter(t.TempDir())
	assert.NilError(t, err)
	t.Cleanup(func() {
		assert.NilError(t, snapshotter.Close())
	})
	cacheManager, err := bkcache.NewSnapshotManager(bkcache.SnapshotManagerOpt{
		Snapshotter:   bkcontainerd.NewSnapshotter("native", snapshotter, "filesync-test"),
		MountPoolRoot: t.TempDir(),
	})
	assert.NilError(t, err)
	t.Cleanup(func() {
		assert.NilError(t, cacheManager.Close())
	})

	syncAndAssertParentChecksum := func() {
		t.Helper()

		ref, _, err := local.Sync(ctx, readFS{client}, cacheManager, false)
		assert.NilError(t, err)
		defer func() {
			assert.NilError(t, ref.Release(context.Background()))
		}()

		dgst, err := bkcontenthash.Checksum(ctx, ref, "/foo", bkcontenthash.ChecksumOpts{})
		assert.NilError(t, err)
		assert.Assert(t, dgst != "")
	}

	syncAndAssertParentChecksum() // cold mirror
	syncAndAssertParentChecksum() // warm mirror, re-included file is ChangeKindNone
}

type readFS struct {
	fsutil.FS
}

func (r readFS) ReadFile(_ context.Context, path string) (io.ReadCloser, error) {
	return r.Open(path)
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for path, contents := range files {
		fullPath := filepath.Join(root, path)
		assert.NilError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		assert.NilError(t, os.WriteFile(fullPath, []byte(contents), 0o644))
	}
	return root
}

// A snapshot reused by content hash must be owned by the syncing operation,
// so its donor's release and GC before publication cannot collect it.
func TestSyncReusedSnapshotSurvivesDonorRelease(t *testing.T) {
	t.Parallel()

	store := testutil.NewStore(t)
	client, err := fsutil.NewFS(writeTree(t, map[string]string{"dagger.json": `{"name":"test"}`}))
	assert.NilError(t, err)
	local, err := newLocalFS(NewMirrorSharedState(t.TempDir()), "", nil, nil, nil, "")
	assert.NilError(t, err)

	donor, donorOwner := syncPublished(t, store, local, client)
	ctx, releaseOp, err := bkcache.WithLazyLease(context.Background(), store.Leases, bkcache.MakeTemporary)
	assert.NilError(t, err)
	defer releaseOp(context.Background())
	reused, _, err := local.Sync(ctx, readFS{client}, store.Manager, false)
	assert.NilError(t, err)
	defer reused.Release(context.Background())
	assert.Equal(t, reused.SnapshotID(), donor.SnapshotID())

	assert.NilError(t, donor.Release(context.Background()))
	assert.NilError(t, store.Manager.RemoveLease(context.Background(), donorOwner))
	store.GC(t)

	assert.NilError(t, store.Manager.AttachLease(context.Background(), "published-reuse", reused.SnapshotID()))
	defer store.Manager.RemoveLease(context.Background(), "published-reuse")
	assertSyncedFile(t, reused, "dagger.json", `{"name":"test"}`)
}

// A content-hash candidate collected before the operation leases it is
// skipped, and Sync falls back to a fresh copy.
func TestSyncCollectedCandidateFallsBackToCopy(t *testing.T) {
	t.Parallel()

	store := testutil.NewStore(t)
	client, err := fsutil.NewFS(writeTree(t, map[string]string{"dagger.json": `{"name":"test"}`}))
	assert.NilError(t, err)
	local, err := newLocalFS(NewMirrorSharedState(t.TempDir()), "", nil, nil, nil, "")
	assert.NilError(t, err)

	donor, donorOwner := syncPublished(t, store, local, client)
	donorSnapshot := donor.SnapshotID()
	collected := false
	store.BeforeAdd = func(_ context.Context, _ leases.Lease, resource leases.Resource) error {
		if resource.ID == donorSnapshot && !collected {
			collected = true
			assert.NilError(t, donor.Release(context.Background()))
			assert.NilError(t, store.Manager.RemoveLease(context.Background(), donorOwner))
			store.GC(t)
		}
		return nil
	}
	ctx, releaseOp, err := bkcache.WithLazyLease(context.Background(), store.Leases, bkcache.MakeTemporary)
	assert.NilError(t, err)
	defer releaseOp(context.Background())
	copied, _, err := local.Sync(ctx, readFS{client}, store.Manager, false)
	assert.NilError(t, err)
	defer copied.Release(context.Background())
	assert.Assert(t, collected)
	assert.Assert(t, copied.SnapshotID() != donorSnapshot)
	assertSyncedFile(t, copied, "dagger.json", `{"name":"test"}`)
}

// syncPublished syncs under its own operation lease and hands the snapshot to
// an owner lease, as publishing a result does.
func syncPublished(t *testing.T, store *testutil.Store, local *localFS, client fsutil.FS) (bkcache.ImmutableRef, string) {
	t.Helper()
	ctx, releaseOp, err := bkcache.WithLazyLease(context.Background(), store.Leases, bkcache.MakeTemporary)
	assert.NilError(t, err)
	ref, _, err := local.Sync(ctx, readFS{client}, store.Manager, false)
	assert.NilError(t, err)
	owner := "published-" + ref.SnapshotID()
	assert.NilError(t, store.Manager.AttachLease(context.Background(), owner, ref.SnapshotID()))
	assert.NilError(t, releaseOp(context.Background()))
	return ref, owner
}

func assertSyncedFile(t *testing.T, ref bkcache.ImmutableRef, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(testutil.Root(t, ref), name))
	assert.NilError(t, err)
	assert.Equal(t, string(got), want)
}
