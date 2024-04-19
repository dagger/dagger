package mounts

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	"github.com/containerd/containerd/leases"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/snapshot"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/winlayers"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/errgroup"
)

type cmOpt struct {
	snapshotterName string
	snapshotter     snapshots.Snapshotter
	tmpdir          string
}

type cmOut struct {
	manager cache.Manager
	lm      leases.Manager
	cs      content.Store
}

func newCacheManager(ctx context.Context, t *testing.T, opt cmOpt) (co *cmOut, err error) {
	ns, ok := namespaces.Namespace(ctx)
	if !ok {
		return nil, errors.Errorf("namespace required for test")
	}

	if opt.snapshotterName == "" {
		opt.snapshotterName = "native"
	}

	tmpdir := t.TempDir()

	if opt.tmpdir != "" {
		os.RemoveAll(tmpdir)
		tmpdir = opt.tmpdir
	}

	if opt.snapshotter == nil {
		snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
		if err != nil {
			return nil, err
		}
		opt.snapshotter = snapshotter
	}

	store, err := local.NewStore(tmpdir)
	if err != nil {
		return nil, err
	}

	db, err := bolt.Open(filepath.Join(tmpdir, "containerdmeta.db"), 0644, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	mdb := ctdmetadata.NewDB(db, store, map[string]snapshots.Snapshotter{
		opt.snapshotterName: opt.snapshotter,
	})
	if err := mdb.Init(context.TODO()); err != nil {
		return nil, err
	}

	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), ns)
	c := mdb.ContentStore()
	applier := winlayers.NewFileSystemApplierWithWindows(c, apply.NewFileSystemApplier(c))
	differ := winlayers.NewWalkingDiffWithWindows(c, walking.NewWalkingDiff(c))

	md, err := metadata.NewStore(filepath.Join(tmpdir, "metadata.db"))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, md.Close())
	})

	cm, err := cache.NewManager(cache.ManagerOpt{
		Snapshotter:    snapshot.FromContainerdSnapshotter(opt.snapshotterName, containerdsnapshot.NSSnapshotter(ns, mdb.Snapshotter(opt.snapshotterName)), nil),
		MetadataStore:  md,
		ContentStore:   c,
		Applier:        applier,
		Differ:         differ,
		LeaseManager:   lm,
		GarbageCollect: mdb.GarbageCollect,
		MountPoolRoot:  filepath.Join(tmpdir, "cachemounts"),
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, cm.Close())
	})

	return &cmOut{
		manager: cm,
		lm:      lm,
		cs:      mdb.ContentStore(),
	}, nil
}

func newRefGetter(m cache.Manager, shared *cacheRefs) *cacheRefGetter {
	return &cacheRefGetter{
		locker:          &sync.Mutex{},
		cacheMounts:     map[string]*cacheRefShare{},
		cm:              m,
		globalCacheRefs: shared,
	}
}

func TestCacheMountPrivateRefs(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)

	g1 := newRefGetter(co.manager, sharedCacheRefs)
	g2 := newRefGetter(co.manager, sharedCacheRefs)
	g3 := newRefGetter(co.manager, sharedCacheRefs)
	g4 := newRefGetter(co.manager, sharedCacheRefs)

	ref, err := g1.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)

	ref2, err := g1.getRefCacheDir(ctx, nil, "bar", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)

	// different ID returns different ref
	require.NotEqual(t, ref.ID(), ref2.ID())

	// same ID on same mount still shares the reference
	ref3, err := g1.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)

	require.Equal(t, ref.ID(), ref3.ID())

	// same ID on different mount gets a new ID
	ref4, err := g2.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)

	require.NotEqual(t, ref.ID(), ref4.ID())

	// releasing one of two refs still keeps first ID private
	ref.Release(context.TODO())

	ref5, err := g3.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)
	require.NotEqual(t, ref.ID(), ref5.ID())
	require.NotEqual(t, ref4.ID(), ref5.ID())

	// releasing all refs releases ID to be reused
	ref3.Release(context.TODO())

	ref5, err = g4.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)

	require.Equal(t, ref.ID(), ref5.ID())

	// other mounts still keep their IDs
	ref6, err := g2.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)
	require.Equal(t, ref4.ID(), ref6.ID())
}

func TestCacheMountSharedRefs(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)

	g1 := newRefGetter(co.manager, sharedCacheRefs)
	g2 := newRefGetter(co.manager, sharedCacheRefs)
	g3 := newRefGetter(co.manager, sharedCacheRefs)

	ref, err := g1.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_SHARED)
	require.NoError(t, err)

	ref2, err := g1.getRefCacheDir(ctx, nil, "bar", pb.CacheSharingOpt_SHARED)
	require.NoError(t, err)

	// different ID returns different ref
	require.NotEqual(t, ref.ID(), ref2.ID())

	// same ID on same mount still shares the reference
	ref3, err := g1.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_SHARED)
	require.NoError(t, err)

	require.Equal(t, ref.ID(), ref3.ID())

	// same ID on different mount gets same ID
	ref4, err := g2.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_SHARED)
	require.NoError(t, err)

	require.Equal(t, ref.ID(), ref4.ID())

	// private gets a new ID
	ref5, err := g3.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_PRIVATE)
	require.NoError(t, err)
	require.NotEqual(t, ref.ID(), ref5.ID())
}

func TestCacheMountLockedRefs(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)

	g1 := newRefGetter(co.manager, sharedCacheRefs)
	g2 := newRefGetter(co.manager, sharedCacheRefs)

	ref, err := g1.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_LOCKED)
	require.NoError(t, err)

	ref2, err := g1.getRefCacheDir(ctx, nil, "bar", pb.CacheSharingOpt_LOCKED)
	require.NoError(t, err)

	// different ID returns different ref
	require.NotEqual(t, ref.ID(), ref2.ID())

	// same ID on same mount still shares the reference
	ref3, err := g1.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_LOCKED)
	require.NoError(t, err)

	require.Equal(t, ref.ID(), ref3.ID())

	// same ID on different mount blocks
	gotRef4 := make(chan struct{})
	go func() {
		ref4, err := g2.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_LOCKED)
		require.NoError(t, err)
		require.Equal(t, ref.ID(), ref4.ID())
		close(gotRef4)
	}()

	select {
	case <-gotRef4:
		require.FailNow(t, "mount did not lock")
	case <-time.After(500 * time.Millisecond):
	}

	ref.Release(ctx)
	ref3.Release(ctx)

	select {
	case <-gotRef4:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "mount did not unlock")
	}
}

// moby/buildkit#1322
func TestCacheMountSharedRefsDeadlock(t *testing.T) {
	// not parallel
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)

	var sharedCacheRefs = &cacheRefs{}

	g1 := newRefGetter(co.manager, sharedCacheRefs)
	g2 := newRefGetter(co.manager, sharedCacheRefs)

	ref, err := g1.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_SHARED)
	require.NoError(t, err)

	cacheRefReleaseHijack = func() {
		time.Sleep(200 * time.Millisecond)
	}
	cacheRefCloneHijack = func() {
		time.Sleep(400 * time.Millisecond)
	}
	defer func() {
		cacheRefReleaseHijack = nil
		cacheRefCloneHijack = nil
	}()
	eg, _ := errgroup.WithContext(context.TODO())

	eg.Go(func() error {
		return ref.Release(context.TODO())
	})
	eg.Go(func() error {
		_, err := g2.getRefCacheDir(ctx, nil, "foo", pb.CacheSharingOpt_SHARED)
		return err
	})

	done := make(chan struct{})
	go func() {
		err = eg.Wait()
		require.NoError(t, err)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "deadlock on releasing while getting new ref")
	}
}
