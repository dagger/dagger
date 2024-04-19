package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ctdcompression "github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/archive/tarheader"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/labels"
	"github.com/containerd/containerd/leases"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/containerd/stargz-snapshotter/estargz"
	"github.com/klauspost/compress/zstd"
	"github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/util/contentutil"
	"github.com/moby/buildkit/util/converter"
	"github.com/moby/buildkit/util/iohelper"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/overlay"
	"github.com/moby/buildkit/util/winlayers"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
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
	manager Manager
	lm      leases.Manager
	cs      content.Store
}

func newCacheManager(ctx context.Context, t *testing.T, opt cmOpt) (co *cmOut, cleanup func(), err error) {
	ns, ok := namespaces.Namespace(ctx)
	if !ok {
		return nil, nil, errors.Errorf("namespace required for test")
	}

	if opt.snapshotterName == "" {
		opt.snapshotterName = "native"
	}

	tmpdir := t.TempDir()

	defers := make([]func() error, 0)
	cleanup = func() {
		var err error
		for i := range defers {
			if err1 := defers[len(defers)-1-i](); err1 != nil && err == nil {
				err = err1
			}
		}
		require.NoError(t, err)
	}
	defer func() {
		if err != nil && cleanup != nil {
			cleanup()
		}
	}()
	if opt.tmpdir != "" {
		os.RemoveAll(tmpdir)
		tmpdir = opt.tmpdir
	}

	if opt.snapshotter == nil {
		snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
		if err != nil {
			return nil, nil, err
		}
		opt.snapshotter = snapshotter
	}

	store, err := local.NewStore(tmpdir)
	if err != nil {
		return nil, nil, err
	}

	db, err := bolt.Open(filepath.Join(tmpdir, "containerdmeta.db"), 0644, nil)
	if err != nil {
		return nil, nil, err
	}
	defers = append(defers, func() error {
		return db.Close()
	})

	mdb := ctdmetadata.NewDB(db, store, map[string]snapshots.Snapshotter{
		opt.snapshotterName: opt.snapshotter,
	})
	if err := mdb.Init(context.TODO()); err != nil {
		return nil, nil, err
	}

	c := mdb.ContentStore()
	store = containerdsnapshot.NewContentStore(c, ns)
	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), ns)
	applier := winlayers.NewFileSystemApplierWithWindows(store, apply.NewFileSystemApplier(store))
	differ := winlayers.NewWalkingDiffWithWindows(store, walking.NewWalkingDiff(store))

	md, err := metadata.NewStore(filepath.Join(tmpdir, "metadata.db"))
	if err != nil {
		return nil, nil, err
	}
	defers = append(defers, func() error {
		return md.Close()
	})

	cm, err := NewManager(ManagerOpt{
		Snapshotter:    snapshot.FromContainerdSnapshotter(opt.snapshotterName, containerdsnapshot.NSSnapshotter(ns, mdb.Snapshotter(opt.snapshotterName)), nil),
		MetadataStore:  md,
		ContentStore:   store,
		LeaseManager:   lm,
		GarbageCollect: mdb.GarbageCollect,
		Applier:        applier,
		Differ:         differ,
		MountPoolRoot:  filepath.Join(tmpdir, "cachemounts"),
	})
	if err != nil {
		return nil, nil, err
	}
	defers = append(defers, func() error {
		return cm.Close()
	})

	return &cmOut{
		manager: cm,
		lm:      lm,
		cs:      store,
	}, cleanup, nil
}

func TestSharableMountPoolCleanup(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	// Emulate the situation where the pool dir is dirty
	mountPoolDir := filepath.Join(tmpdir, "cachemounts")
	require.NoError(t, os.MkdirAll(mountPoolDir, 0700))
	_, err := os.MkdirTemp(mountPoolDir, "buildkit")
	require.NoError(t, err)

	// Initialize cache manager and check if pool is cleaned up
	_, cleanup, err := newCacheManager(ctx, t, cmOpt{
		tmpdir: tmpdir,
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	files, err := os.ReadDir(mountPoolDir)
	require.NoError(t, err)
	require.Equal(t, 0, len(files))
}

func TestManager(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	cm := co.manager

	_, err = cm.Get(ctx, "foobar", nil)
	require.Error(t, err)

	checkDiskUsage(ctx, t, cm, 0, 0)

	active, err := cm.New(ctx, nil, nil, CachePolicyRetain)
	require.NoError(t, err)

	m, err := active.Mount(ctx, false, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(m)
	target, err := lm.Mount()
	require.NoError(t, err)

	fi, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, fi.IsDir(), true)

	err = lm.Unmount()
	require.NoError(t, err)

	_, err = cm.GetMutable(ctx, active.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, ErrLocked))

	checkDiskUsage(ctx, t, cm, 1, 0)

	snap, err := active.Commit(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 0)

	_, err = cm.GetMutable(ctx, active.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, ErrLocked))

	err = snap.Release(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 0, 1)

	active, err = cm.GetMutable(ctx, active.ID())
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 0)

	snap, err = active.Commit(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 0)

	err = snap.Finalize(ctx)
	require.NoError(t, err)

	err = snap.Release(ctx)
	require.NoError(t, err)

	_, err = cm.GetMutable(ctx, active.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	_, err = cm.GetMutable(ctx, snap.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errInvalid))

	snap, err = cm.Get(ctx, snap.ID(), nil)
	require.NoError(t, err)

	snap2, err := cm.Get(ctx, snap.ID(), nil)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 0)

	err = snap.Release(ctx)
	require.NoError(t, err)

	active2, err := cm.New(ctx, snap2, nil, CachePolicyRetain)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	snap3, err := active2.Commit(ctx)
	require.NoError(t, err)

	err = snap2.Release(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	err = snap3.Release(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 0, 2)

	buf := pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 0, 0)

	require.Equal(t, len(buf.all), 2)

	err = cm.Close()
	require.NoError(t, err)

	dirs, err := os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 0, len(dirs))
}

func TestLazyGetByBlob(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager

	// Test for #2226 https://github.com/moby/buildkit/issues/2226, create lazy blobs with the same diff ID but
	// different digests (due to different compression) and make sure GetByBlob still works
	_, desc, err := mapToBlob(map[string]string{"foo": "bar"}, true)
	require.NoError(t, err)
	descHandlers := DescHandlers(make(map[digest.Digest]*DescHandler))
	descHandlers[desc.Digest] = &DescHandler{}
	diffID, err := diffIDFromDescriptor(desc)
	require.NoError(t, err)

	_, err = cm.GetByBlob(ctx, desc, nil, descHandlers)
	require.NoError(t, err)

	_, desc2, err := mapToBlob(map[string]string{"foo": "bar"}, false)
	require.NoError(t, err)
	descHandlers2 := DescHandlers(make(map[digest.Digest]*DescHandler))
	descHandlers2[desc2.Digest] = &DescHandler{}
	diffID2, err := diffIDFromDescriptor(desc2)
	require.NoError(t, err)

	require.NotEqual(t, desc.Digest, desc2.Digest)
	require.Equal(t, diffID, diffID2)

	_, err = cm.GetByBlob(ctx, desc2, nil, descHandlers2)
	require.NoError(t, err)
}

func TestMergeBlobchainID(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager

	// create a merge ref that has 3 inputs, with each input being a 3 layer blob chain
	var mergeInputs []ImmutableRef
	var descs []ocispecs.Descriptor
	descHandlers := DescHandlers(map[digest.Digest]*DescHandler{})
	for i := 0; i < 3; i++ {
		contentBuffer := contentutil.NewBuffer()
		var curBlob ImmutableRef
		for j := 0; j < 3; j++ {
			blobBytes, desc, err := mapToBlob(map[string]string{strconv.Itoa(i): strconv.Itoa(j)}, true)
			require.NoError(t, err)
			cw, err := contentBuffer.Writer(ctx)
			require.NoError(t, err)
			_, err = cw.Write(blobBytes)
			require.NoError(t, err)
			err = cw.Commit(ctx, 0, cw.Digest())
			require.NoError(t, err)
			descHandlers[desc.Digest] = &DescHandler{
				Provider: func(_ session.Group) content.Provider { return contentBuffer },
			}
			curBlob, err = cm.GetByBlob(ctx, desc, curBlob, descHandlers)
			require.NoError(t, err)
			descs = append(descs, desc)
		}
		mergeInputs = append(mergeInputs, curBlob.Clone())
	}

	mergeRef, err := cm.Merge(ctx, mergeInputs, nil)
	require.NoError(t, err)

	_, err = mergeRef.GetRemotes(ctx, true, config.RefConfig{Compression: compression.New(compression.Default)}, false, nil)
	require.NoError(t, err)

	// verify the merge blobchain ID isn't just set to one of the inputs (regression test)
	mergeBlobchainID := mergeRef.(*immutableRef).getBlobChainID()
	for _, mergeInput := range mergeInputs {
		inputBlobchainID := mergeInput.(*immutableRef).getBlobChainID()
		require.NotEqual(t, mergeBlobchainID, inputBlobchainID)
	}

	// verify you get the merge ref when asking for an equivalent blob chain
	var curBlob ImmutableRef
	for _, desc := range descs[:len(descs)-1] {
		curBlob, err = cm.GetByBlob(ctx, desc, curBlob, descHandlers)
		require.NoError(t, err)
	}
	blobRef, err := cm.GetByBlob(ctx, descs[len(descs)-1], curBlob, descHandlers)
	require.NoError(t, err)
	require.Equal(t, mergeRef.ID(), blobRef.ID())
}

func TestSnapshotExtract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	cm := co.manager

	b, desc, err := mapToBlob(map[string]string{"foo": "bar"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref1", bytes.NewBuffer(b), desc)
	require.NoError(t, err)

	snap, err := cm.GetByBlob(ctx, desc, nil)
	require.NoError(t, err)

	require.Equal(t, false, !snap.(*immutableRef).getBlobOnly())

	b2, desc2, err := mapToBlob(map[string]string{"foo": "bar123"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref1", bytes.NewBuffer(b2), desc2)
	require.NoError(t, err)

	snap2, err := cm.GetByBlob(ctx, desc2, snap)
	require.NoError(t, err)

	size, err := snap2.(*immutableRef).size(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(len(b2)), size)

	require.Equal(t, false, !snap2.(*immutableRef).getBlobOnly())

	dirs, err := os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 0, len(dirs))

	checkNumBlobs(ctx, t, co.cs, 2)

	err = snap2.Extract(ctx, nil)
	require.NoError(t, err)

	require.Equal(t, true, !snap.(*immutableRef).getBlobOnly())
	require.Equal(t, true, !snap2.(*immutableRef).getBlobOnly())

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 2, len(dirs))

	buf := pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	require.Equal(t, len(buf.all), 0)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 2, len(dirs))

	checkNumBlobs(ctx, t, co.cs, 2)

	id := snap.ID()

	err = snap.Release(context.TODO())
	require.NoError(t, err)

	buf = pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 2, len(dirs))

	snap, err = cm.Get(ctx, id, nil)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	err = snap2.Release(context.TODO())
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 1)

	buf = pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 0)

	require.Equal(t, len(buf.all), 1)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 1, len(dirs))

	checkNumBlobs(ctx, t, co.cs, 1)

	err = snap.Release(context.TODO())
	require.NoError(t, err)

	buf = pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 0, 0)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 0, len(dirs))

	checkNumBlobs(ctx, t, co.cs, 0)
}

func TestExtractOnMutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	cm := co.manager

	active, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)

	snap, err := active.Commit(ctx)
	require.NoError(t, err)

	b, desc, err := mapToBlob(map[string]string{"foo": "bar"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref1", bytes.NewBuffer(b), desc)
	require.NoError(t, err)

	b2, desc2, err := mapToBlob(map[string]string{"foo2": "1"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref2", bytes.NewBuffer(b2), desc2)
	require.NoError(t, err)

	_, err = cm.GetByBlob(ctx, desc2, snap)
	require.Error(t, err)

	leaseCtx, done, err := leaseutil.WithLease(ctx, co.lm, leases.WithExpiration(0))
	require.NoError(t, err)

	err = snap.(*immutableRef).setBlob(leaseCtx, desc)
	done(context.TODO())
	require.NoError(t, err)
	err = snap.(*immutableRef).computeChainMetadata(leaseCtx, map[string]struct{}{snap.ID(): {}})
	require.NoError(t, err)

	snap2, err := cm.GetByBlob(ctx, desc2, snap)
	require.NoError(t, err)

	err = snap.Release(context.TODO())
	require.NoError(t, err)

	require.Equal(t, false, !snap2.(*immutableRef).getBlobOnly())

	size, err := snap2.(*immutableRef).size(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(len(b2)), size)

	dirs, err := os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 1, len(dirs))

	checkNumBlobs(ctx, t, co.cs, 2)

	err = snap2.Extract(ctx, nil)
	require.NoError(t, err)

	require.Equal(t, true, !snap.(*immutableRef).getBlobOnly())
	require.Equal(t, true, !snap2.(*immutableRef).getBlobOnly())

	buf := pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	require.Equal(t, len(buf.all), 0)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 2, len(dirs))

	err = snap2.Release(context.TODO())
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 0, 2)

	buf = pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 0, 0)

	require.Equal(t, len(buf.all), 2)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 0, len(dirs))

	checkNumBlobs(ctx, t, co.cs, 0)
}

func TestSetBlob(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	ctx, done, err := leaseutil.WithLease(ctx, co.lm, leaseutil.MakeTemporary)
	require.NoError(t, err)
	defer done(context.TODO())

	cm := co.manager

	active, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)

	snap, err := active.Commit(ctx)
	require.NoError(t, err)

	snapRef := snap.(*immutableRef)
	require.Equal(t, "", string(snapRef.getDiffID()))
	require.Equal(t, "", string(snapRef.getBlob()))
	require.Equal(t, "", string(snapRef.getChainID()))
	require.Equal(t, "", string(snapRef.getBlobChainID()))
	require.Equal(t, !snapRef.getBlobOnly(), true)

	ctx, clean, err := leaseutil.WithLease(ctx, co.lm)
	require.NoError(t, err)
	defer clean(context.TODO())

	b, desc, err := mapToBlob(map[string]string{"foo": "bar"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref1", bytes.NewBuffer(b), desc)
	require.NoError(t, err)

	err = snap.(*immutableRef).setBlob(ctx, ocispecs.Descriptor{
		Digest: digest.FromBytes([]byte("foobar")),
		Annotations: map[string]string{
			labels.LabelUncompressed: digest.FromBytes([]byte("foobar2")).String(),
		},
	})
	require.Error(t, err)

	err = snap.(*immutableRef).setBlob(ctx, desc)
	require.NoError(t, err)
	err = snap.(*immutableRef).computeChainMetadata(ctx, map[string]struct{}{snap.ID(): {}})
	require.NoError(t, err)

	snapRef = snap.(*immutableRef)
	require.Equal(t, desc.Annotations[labels.LabelUncompressed], string(snapRef.getDiffID()))
	require.Equal(t, desc.Digest, snapRef.getBlob())
	require.Equal(t, desc.MediaType, snapRef.getMediaType())
	require.Equal(t, snapRef.getDiffID(), snapRef.getChainID())
	require.Equal(t, digest.FromBytes([]byte(desc.Digest+" "+snapRef.getDiffID())), snapRef.getBlobChainID())
	require.Equal(t, snap.ID(), snapRef.getSnapshotID())
	require.Equal(t, !snapRef.getBlobOnly(), true)

	active, err = cm.New(ctx, snap, nil)
	require.NoError(t, err)

	snap2, err := active.Commit(ctx)
	require.NoError(t, err)

	b2, desc2, err := mapToBlob(map[string]string{"foo2": "bar2"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref2", bytes.NewBuffer(b2), desc2)
	require.NoError(t, err)

	err = snap2.(*immutableRef).setBlob(ctx, desc2)
	require.NoError(t, err)
	err = snap2.(*immutableRef).computeChainMetadata(ctx, map[string]struct{}{snap.ID(): {}, snap2.ID(): {}})
	require.NoError(t, err)

	snapRef2 := snap2.(*immutableRef)
	require.Equal(t, desc2.Annotations[labels.LabelUncompressed], string(snapRef2.getDiffID()))
	require.Equal(t, desc2.Digest, snapRef2.getBlob())
	require.Equal(t, desc2.MediaType, snapRef2.getMediaType())
	require.Equal(t, digest.FromBytes([]byte(snapRef.getChainID()+" "+snapRef2.getDiffID())), snapRef2.getChainID())
	require.Equal(t, digest.FromBytes([]byte(snapRef.getBlobChainID()+" "+digest.FromBytes([]byte(desc2.Digest+" "+snapRef2.getDiffID())))), snapRef2.getBlobChainID())
	require.Equal(t, snap2.ID(), snapRef2.getSnapshotID())
	require.Equal(t, !snapRef2.getBlobOnly(), true)

	b3, desc3, err := mapToBlob(map[string]string{"foo3": "bar3"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref3", bytes.NewBuffer(b3), desc3)
	require.NoError(t, err)

	snap3, err := cm.GetByBlob(ctx, desc3, snap)
	require.NoError(t, err)

	snapRef3 := snap3.(*immutableRef)
	require.Equal(t, desc3.Annotations[labels.LabelUncompressed], string(snapRef3.getDiffID()))
	require.Equal(t, desc3.Digest, snapRef3.getBlob())
	require.Equal(t, desc3.MediaType, snapRef3.getMediaType())
	require.Equal(t, digest.FromBytes([]byte(snapRef.getChainID()+" "+snapRef3.getDiffID())), snapRef3.getChainID())
	require.Equal(t, digest.FromBytes([]byte(snapRef.getBlobChainID()+" "+digest.FromBytes([]byte(desc3.Digest+" "+snapRef3.getDiffID())))), snapRef3.getBlobChainID())
	require.Equal(t, string(snapRef3.getChainID()), snapRef3.getSnapshotID())
	require.Equal(t, !snapRef3.getBlobOnly(), false)

	// snap4 is same as snap2
	snap4, err := cm.GetByBlob(ctx, desc2, snap)
	require.NoError(t, err)

	require.Equal(t, snap2.ID(), snap4.ID())

	// snap5 is same different blob but same diffID as snap2
	b5, desc5, err := mapToBlob(map[string]string{"foo5": "bar5"}, true)
	require.NoError(t, err)

	desc5.Annotations[labels.LabelUncompressed] = snapRef2.getDiffID().String()

	err = content.WriteBlob(ctx, co.cs, "ref5", bytes.NewBuffer(b5), desc5)
	require.NoError(t, err)

	snap5, err := cm.GetByBlob(ctx, desc5, snap)
	require.NoError(t, err)

	snapRef5 := snap5.(*immutableRef)
	require.NotEqual(t, snap2.ID(), snap5.ID())
	require.Equal(t, snapRef2.getSnapshotID(), snapRef5.getSnapshotID())
	require.Equal(t, snapRef2.getDiffID(), snapRef5.getDiffID())
	require.Equal(t, desc5.Digest, snapRef5.getBlob())

	require.Equal(t, snapRef2.getChainID(), snapRef5.getChainID())
	require.NotEqual(t, snapRef2.getBlobChainID(), snapRef5.getBlobChainID())
	require.Equal(t, digest.FromBytes([]byte(snapRef.getBlobChainID()+" "+digest.FromBytes([]byte(desc5.Digest+" "+snapRef2.getDiffID())))), snapRef5.getBlobChainID())

	// snap6 is a child of snap3
	b6, desc6, err := mapToBlob(map[string]string{"foo6": "bar6"}, true)
	require.NoError(t, err)

	err = content.WriteBlob(ctx, co.cs, "ref6", bytes.NewBuffer(b6), desc6)
	require.NoError(t, err)

	snap6, err := cm.GetByBlob(ctx, desc6, snap3)
	require.NoError(t, err)

	snapRef6 := snap6.(*immutableRef)
	require.Equal(t, desc6.Annotations[labels.LabelUncompressed], string(snapRef6.getDiffID()))
	require.Equal(t, desc6.Digest, snapRef6.getBlob())
	require.Equal(t, digest.FromBytes([]byte(snapRef3.getChainID()+" "+snapRef6.getDiffID())), snapRef6.getChainID())
	require.Equal(t, digest.FromBytes([]byte(snapRef3.getBlobChainID()+" "+digest.FromBytes([]byte(snapRef6.getBlob()+" "+snapRef6.getDiffID())))), snapRef6.getBlobChainID())
	require.Equal(t, string(snapRef6.getChainID()), snapRef6.getSnapshotID())
	require.Equal(t, !snapRef6.getBlobOnly(), false)

	_, err = cm.GetByBlob(ctx, ocispecs.Descriptor{
		Digest: digest.FromBytes([]byte("notexist")),
		Annotations: map[string]string{
			labels.LabelUncompressed: digest.FromBytes([]byte("notexist")).String(),
		},
	}, snap3)
	require.Error(t, err)

	clean(context.TODO())

	// snap.SetBlob()
}

func TestPrune(t *testing.T) {
	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	cm := co.manager

	active, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)

	snap, err := active.Commit(ctx)
	require.NoError(t, err)

	active, err = cm.New(ctx, snap, nil, CachePolicyRetain)
	require.NoError(t, err)

	snap2, err := active.Commit(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	dirs, err := os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 2, len(dirs))

	// prune with keeping refs does nothing
	buf := pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)
	require.Equal(t, len(buf.all), 0)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 2, len(dirs))

	err = snap2.Release(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 1)

	// prune with keeping single refs deletes one
	buf = pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 1, 0)
	require.Equal(t, len(buf.all), 1)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 1, len(dirs))

	err = snap.Release(ctx)
	require.NoError(t, err)

	active, err = cm.New(ctx, snap, nil, CachePolicyRetain)
	require.NoError(t, err)

	snap2, err = active.Commit(ctx)
	require.NoError(t, err)

	err = snap.Release(ctx)
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)

	// prune with parent released does nothing
	buf = pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 2, 0)
	require.Equal(t, len(buf.all), 0)

	// releasing last reference
	err = snap2.Release(ctx)
	require.NoError(t, err)
	checkDiskUsage(ctx, t, cm, 0, 2)

	buf = pruneResultBuffer()
	err = cm.Prune(ctx, buf.C, client.PruneInfo{})
	buf.close()
	require.NoError(t, err)

	checkDiskUsage(ctx, t, cm, 0, 0)
	require.Equal(t, len(buf.all), 2)

	dirs, err = os.ReadDir(filepath.Join(tmpdir, "snapshots/snapshots"))
	require.NoError(t, err)
	require.Equal(t, 0, len(dirs))
}

func TestLazyCommit(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		tmpdir:          tmpdir,
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	cm := co.manager

	active, err := cm.New(ctx, nil, nil, CachePolicyRetain)
	require.NoError(t, err)

	// after commit mutable is locked
	snap, err := active.Commit(ctx)
	require.NoError(t, err)

	_, err = cm.GetMutable(ctx, active.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, ErrLocked))

	// immutable refs still work
	snap2, err := cm.Get(ctx, snap.ID(), nil)
	require.NoError(t, err)
	require.Equal(t, snap.ID(), snap2.ID())

	err = snap.Release(ctx)
	require.NoError(t, err)

	err = snap2.Release(ctx)
	require.NoError(t, err)

	// immutable work after final release as well
	snap, err = cm.Get(ctx, snap.ID(), nil)
	require.NoError(t, err)
	require.Equal(t, snap.ID(), snap2.ID())

	// active can't be get while immutable is held
	_, err = cm.GetMutable(ctx, active.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, ErrLocked))

	err = snap.Release(ctx)
	require.NoError(t, err)

	// after release mutable becomes available again
	active2, err := cm.GetMutable(ctx, active.ID())
	require.NoError(t, err)
	require.Equal(t, active2.ID(), active.ID())

	// because ref was took mutable old immutable are cleared
	_, err = cm.Get(ctx, snap.ID(), nil)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	snap, err = active2.Commit(ctx)
	require.NoError(t, err)

	// this time finalize commit
	err = snap.Finalize(ctx)
	require.NoError(t, err)

	err = snap.Release(ctx)
	require.NoError(t, err)

	// mutable is gone after finalize
	_, err = cm.GetMutable(ctx, active2.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	// immutable still works
	snap2, err = cm.Get(ctx, snap.ID(), nil)
	require.NoError(t, err)
	require.Equal(t, snap.ID(), snap2.ID())

	err = snap2.Release(ctx)
	require.NoError(t, err)

	// test restarting after commit
	active, err = cm.New(ctx, nil, nil, CachePolicyRetain)
	require.NoError(t, err)

	// after commit mutable is locked
	snap, err = active.Commit(ctx)
	require.NoError(t, err)

	err = cm.Close()
	require.NoError(t, err)

	cleanup()

	// we can't close snapshotter and open it twice (especially, its internal bbolt store)
	co, cleanup, err = newCacheManager(ctx, t, cmOpt{
		tmpdir:          tmpdir,
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	cm = co.manager

	snap2, err = cm.Get(ctx, snap.ID(), nil)
	require.NoError(t, err)

	err = snap2.Release(ctx)
	require.NoError(t, err)

	active, err = cm.GetMutable(ctx, active.ID())
	require.NoError(t, err)

	_, err = cm.Get(ctx, snap.ID(), nil)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	snap, err = active.Commit(ctx)
	require.NoError(t, err)

	err = cm.Close()
	require.NoError(t, err)

	cleanup()

	co, cleanup, err = newCacheManager(ctx, t, cmOpt{
		tmpdir:          tmpdir,
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm = co.manager

	snap2, err = cm.Get(ctx, snap.ID(), nil)
	require.NoError(t, err)

	err = snap2.Finalize(ctx)
	require.NoError(t, err)

	err = snap2.Release(ctx)
	require.NoError(t, err)

	_, err = cm.GetMutable(ctx, active.ID())
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))
}

func TestLoopLeaseContent(t *testing.T) {
	t.Parallel()
	// windows fails when lazy blob is being extracted with "invalid windows mount type: 'bind'"
	if runtime.GOOS != "linux" {
		t.Skipf("unsupported GOOS: %s", runtime.GOOS)
	}

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager

	ctx, done, err := leaseutil.WithLease(ctx, co.lm, leaseutil.MakeTemporary)
	require.NoError(t, err)
	defer done(ctx)

	// store an uncompressed blob to the content store
	compressionLoop := []compression.Type{compression.Uncompressed, compression.Gzip, compression.Zstd, compression.EStargz}
	blobBytes, orgDesc, err := mapToBlob(map[string]string{"foo": "1"}, false)
	require.NoError(t, err)
	contentBuffer := contentutil.NewBuffer()
	descHandlers := DescHandlers(map[digest.Digest]*DescHandler{})
	cw, err := contentBuffer.Writer(ctx, content.WithRef(fmt.Sprintf("write-test-blob-%s", orgDesc.Digest)))
	require.NoError(t, err)
	_, err = cw.Write(blobBytes)
	require.NoError(t, err)
	require.NoError(t, cw.Commit(ctx, 0, cw.Digest()))
	descHandlers[orgDesc.Digest] = &DescHandler{
		Provider: func(_ session.Group) content.Provider { return contentBuffer },
	}

	// Create a compression loop
	ref, err := cm.GetByBlob(ctx, orgDesc, nil, descHandlers)
	require.NoError(t, err)
	allRefs := []ImmutableRef{ref}
	defer func() {
		for _, ref := range allRefs {
			ref.Release(ctx)
		}
	}()
	var chain []ocispecs.Descriptor
	for _, compressionType := range compressionLoop {
		remotes, err := ref.GetRemotes(ctx, true, config.RefConfig{Compression: compression.New(compressionType).SetForce(true)}, false, nil)
		require.NoError(t, err)
		require.Equal(t, 1, len(remotes))
		require.Equal(t, 1, len(remotes[0].Descriptors))

		desc := remotes[0].Descriptors[0]
		chain = append(chain, desc)
		ref, err = cm.GetByBlob(ctx, desc, nil, descHandlers)
		require.NoError(t, err)
		allRefs = append(allRefs, ref)
	}
	require.Equal(t, len(compressionLoop), len(chain))
	require.NoError(t, ref.(*immutableRef).linkBlob(ctx, chain[0])) // This creates a loop

	// Make sure a loop is created
	visited := make(map[digest.Digest]struct{})
	gotChain := []digest.Digest{orgDesc.Digest}
	cur := orgDesc
	previous := chain[len(chain)-1].Digest
	for i := 0; i < 1000; i++ {
		dgst := cur.Digest
		visited[dgst] = struct{}{}
		info, err := co.cs.Info(ctx, dgst)
		if err != nil && !errors.Is(err, errdefs.ErrNotFound) {
			require.NoError(t, err)
		}
		var children []ocispecs.Descriptor
		for k, dgstS := range info.Labels {
			if !strings.HasPrefix(k, blobVariantGCLabel) {
				continue
			}
			cDgst, err := digest.Parse(dgstS)
			if err != nil || cDgst == dgst || previous == cDgst {
				continue
			}
			cDesc, err := getBlobDesc(ctx, co.cs, cDgst)
			require.NoError(t, err)
			children = append(children, cDesc)
		}
		require.Equal(t, 1, len(children), "previous=%v, cur=%v, labels: %+v", previous, cur, info.Labels)
		previous = cur.Digest
		cur = children[0]
		if _, ok := visited[cur.Digest]; ok {
			break
		}
		gotChain = append(gotChain, cur.Digest)
	}
	require.Equal(t, len(chain), len(gotChain))

	// Prune all refs
	require.NoError(t, done(ctx))
	for _, ref := range allRefs {
		ref.Release(ctx)
	}
	ensurePrune(ctx, t, cm, len(gotChain)-1, 10)

	// Check if contents are cleaned up
	for _, d := range gotChain {
		_, err := co.cs.Info(ctx, d)
		require.ErrorIs(t, err, errdefs.ErrNotFound)
	}
}

func TestSharingCompressionVariant(t *testing.T) {
	t.Parallel()
	// windows fails when lazy blob is being extracted with "invalid windows mount type: 'bind'"
	if runtime.GOOS != "linux" {
		t.Skipf("unsupported GOOS: %s", runtime.GOOS)
	}

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	allCompressions := []compression.Type{compression.Uncompressed, compression.Gzip, compression.Zstd, compression.EStargz}

	do := func(test func(testCaseSharingCompressionVariant)) {
		for _, a := range exclude(allCompressions, compression.Uncompressed) {
			for _, aV1 := range exclude(allCompressions, a) {
				for _, aV2 := range exclude(allCompressions, a, aV1) {
					for _, b := range []compression.Type{aV1, aV2} {
						for _, bV1 := range exclude(allCompressions, a, aV1, aV2) {
							test(testCaseSharingCompressionVariant{
								a:         a,
								aVariants: []compression.Type{aV1, aV2},
								b:         b,
								bVariants: []compression.Type{bV1, a},
							})
						}
					}
				}
			}
		}
	}

	t.Logf("Test cases with possible compression types")
	do(func(testCase testCaseSharingCompressionVariant) {
		testCase.checkPrune = true
		testSharingCompressionVariant(ctx, t, co, testCase)
		require.NoError(t, co.manager.Prune(ctx, nil, client.PruneInfo{All: true}))
		checkDiskUsage(ctx, t, co.manager, 0, 0)
	})

	t.Logf("Test case with many parallel operation")
	eg, egctx := errgroup.WithContext(ctx)
	do(func(testCase testCaseSharingCompressionVariant) {
		eg.Go(func() error {
			testCase.checkPrune = false
			testSharingCompressionVariant(egctx, t, co, testCase)
			return nil
		})
	})
	require.NoError(t, eg.Wait())
}

func exclude(s []compression.Type, ts ...compression.Type) (res []compression.Type) {
EachElem:
	for _, v := range s {
		for _, t := range ts {
			if v == t {
				continue EachElem
			}
		}
		res = append(res, v)
	}
	return
}

// testCaseSharingCompressionVariant is one test case configuration for testSharingCompressionVariant.
// This configures two refs A and B.
// A creates compression variants configured by aVariants and
// B creates compression variants configured by bVariants.
// This test checks if aVariants are visible from B and bVariants are visible from A.
type testCaseSharingCompressionVariant struct {
	// a is the compression of the initial immutableRef's (called A) blob
	a compression.Type

	// aVariants are the compression variants created from A
	aVariants []compression.Type

	// b is another immutableRef (called B) which has one of the compression variants of A
	b compression.Type

	// bVariants are compression variants created from B
	bVariants []compression.Type

	// checkPrune is whether checking prune API. must be false if run tests in parallel.
	checkPrune bool
}

func testSharingCompressionVariant(ctx context.Context, t *testing.T, co *cmOut, testCase testCaseSharingCompressionVariant) {
	var (
		cm              = co.manager
		allCompressions = append(append([]compression.Type{testCase.a, testCase.b}, testCase.aVariants...), testCase.bVariants...)
		orgContent      = map[string]string{"foo": "1"}
	)
	test := func(customized bool) {
		defer cm.Prune(ctx, nil, client.PruneInfo{})

		// Prepare the original content
		_, orgContentDesc, err := mapToBlob(orgContent, false)
		require.NoError(t, err)
		blobBytes, aDesc, err := mapToBlobWithCompression(orgContent, func(w io.Writer) (io.WriteCloser, string, error) {
			cw, err := getCompressor(w, testCase.a, customized)
			if err != nil {
				return nil, "", err
			}
			return cw, testCase.a.MediaType(), nil
		})
		require.NoError(t, err)
		contentBuffer := contentutil.NewBuffer()
		descHandlers := DescHandlers(map[digest.Digest]*DescHandler{})
		cw, err := contentBuffer.Writer(ctx, content.WithRef(fmt.Sprintf("write-test-blob-%s", aDesc.Digest)))
		require.NoError(t, err)
		_, err = cw.Write(blobBytes)
		require.NoError(t, err)
		require.NoError(t, cw.Commit(ctx, 0, cw.Digest()))
		descHandlers[aDesc.Digest] = &DescHandler{
			Provider: func(_ session.Group) content.Provider { return contentBuffer },
		}

		// Create compression variants
		aRef, err := cm.GetByBlob(ctx, aDesc, nil, descHandlers)
		require.NoError(t, err)
		defer aRef.Release(ctx)
		var bDesc ocispecs.Descriptor
		for _, compressionType := range append([]compression.Type{testCase.a}, testCase.aVariants...) {
			remotes, err := aRef.GetRemotes(ctx, true, config.RefConfig{Compression: compression.New(compressionType).SetForce(true)}, false, nil)
			require.NoError(t, err)
			require.Equal(t, 1, len(remotes))
			require.Equal(t, 1, len(remotes[0].Descriptors))
			if compressionType == testCase.b {
				bDesc = remotes[0].Descriptors[0]
			}
		}
		require.NotEqual(t, "", bDesc.Digest, "compression B must be chosen from the variants of A")
		bRef, err := cm.GetByBlob(ctx, bDesc, nil, descHandlers)
		require.NoError(t, err)
		defer bRef.Release(ctx)
		for _, compressionType := range append([]compression.Type{testCase.b}, testCase.bVariants...) {
			remotes, err := bRef.GetRemotes(ctx, true, config.RefConfig{Compression: compression.New(compressionType).SetForce(true)}, false, nil)
			require.NoError(t, err)
			require.Equal(t, 1, len(remotes))
			require.Equal(t, 1, len(remotes[0].Descriptors))
		}

		// check if all compression variables are available on the both refs
		checkCompression := func(desc ocispecs.Descriptor, compressionType compression.Type) {
			require.Equal(t, compressionType.MediaType(), desc.MediaType, "compression: %v", compressionType)
			if compressionType == compression.EStargz {
				ok, err := compression.EStargz.Is(ctx, co.cs, desc.Digest)
				require.NoError(t, err, "compression: %v", compressionType)
				require.True(t, ok, "compression: %v", compressionType)
			}
		}
		for _, c := range allCompressions {
			aDesc, err := aRef.(*immutableRef).getBlobWithCompression(ctx, c)
			require.NoError(t, err, "compression: %v", c)
			bDesc, err := bRef.(*immutableRef).getBlobWithCompression(ctx, c)
			require.NoError(t, err, "compression: %v", c)
			checkCompression(aDesc, c)
			checkCompression(bDesc, c)
		}

		// check if compression variables are availalbe on B still after A is released
		if testCase.checkPrune && aRef.ID() != bRef.ID() {
			require.NoError(t, aRef.Release(ctx))
			ensurePrune(ctx, t, cm, 1, 10)
			checkDiskUsage(ctx, t, co.manager, 1, 0)
			for _, c := range allCompressions {
				_, err = bRef.(*immutableRef).getBlobWithCompression(ctx, c)
				require.NoError(t, err)
			}
		}

		// check if contents are valid
		for _, c := range allCompressions {
			bDesc, err := bRef.(*immutableRef).getBlobWithCompression(ctx, c)
			require.NoError(t, err, "compression: %v", c)
			uDgst := bDesc.Digest
			if c != compression.Uncompressed {
				convertFunc, err := converter.New(ctx, co.cs, bDesc, compression.New(compression.Uncompressed))
				require.NoError(t, err, "compression: %v", c)
				uDesc, err := convertFunc(ctx, co.cs, bDesc)
				require.NoError(t, err, "compression: %v", c)
				uDgst = uDesc.Digest
			}
			require.Equal(t, uDgst, orgContentDesc.Digest, "compression: %v", c)
		}
	}
	for _, customized := range []bool{true, false} {
		// tests in two patterns: whether making the initial blob customized
		test(customized)
	}
}

func ensurePrune(ctx context.Context, t *testing.T, cm Manager, pruneNum, maxRetry int) {
	sum := 0
	for i := 0; i <= maxRetry; i++ {
		buf := pruneResultBuffer()
		require.NoError(t, cm.Prune(ctx, buf.C, client.PruneInfo{All: true}))
		buf.close()
		sum += len(buf.all)
		if sum >= pruneNum {
			return
		}
		time.Sleep(100 * time.Millisecond)
		t.Logf("Retrying to prune (%v)", i)
	}
	require.Equal(t, true, sum >= pruneNum, "actual=%v, expected=%v", sum, pruneNum)
}

func getCompressor(w io.Writer, compressionType compression.Type, customized bool) (io.WriteCloser, error) {
	switch compressionType {
	case compression.Uncompressed:
		return nil, errors.Errorf("compression is not requested: %v", compressionType)
	case compression.Gzip:
		if customized {
			gz, _ := gzip.NewWriterLevel(w, gzip.NoCompression)
			gz.Header.Comment = "hello"
			gz.Close()
		}
		return gzip.NewWriter(w), nil
	case compression.EStargz:
		done := make(chan struct{})
		pr, pw := io.Pipe()
		level := gzip.BestCompression
		if customized {
			level = gzip.BestSpeed
		}
		go func() {
			defer close(done)
			gw := estargz.NewWriterLevel(w, level)
			if err := gw.AppendTarLossLess(pr); err != nil {
				pr.CloseWithError(err)
				return
			}
			if _, err := gw.Close(); err != nil {
				pr.CloseWithError(err)
				return
			}
			pr.Close()
		}()
		return &iohelper.WriteCloser{WriteCloser: pw, CloseFunc: func() error { <-done; return nil }}, nil
	case compression.Zstd:
		if customized {
			skippableFrameMagic := []byte{0x50, 0x2a, 0x4d, 0x18}
			s := []byte("hello")
			size := make([]byte, 4)
			binary.LittleEndian.PutUint32(size, uint32(len(s)))
			if _, err := w.Write(append(append(skippableFrameMagic, size...), s...)); err != nil {
				return nil, err
			}
		}
		return zstd.NewWriter(w)
	default:
		return nil, errors.Errorf("unknown compression type: %q", compressionType)
	}
}

func TestConversion(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skipf("unsupported GOOS: %s", runtime.GOOS)
	}

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	store := co.cs

	// Preapre the original tar blob using archive/tar and tar command on the system
	m := map[string]string{"foo1": "bar1", "foo2": "bar2"}

	orgBlobBytesGo, orgDescGo, err := mapToBlob(m, false)
	require.NoError(t, err)
	cw, err := store.Writer(ctx, content.WithRef(fmt.Sprintf("write-test-blob-%s", orgDescGo.Digest)))
	require.NoError(t, err)
	_, err = cw.Write(orgBlobBytesGo)
	require.NoError(t, err)
	err = cw.Commit(ctx, 0, cw.Digest())
	require.NoError(t, err)

	orgBlobBytesSys, orgDescSys, err := mapToSystemTarBlob(t, m)
	require.NoError(t, err)
	cw, err = store.Writer(ctx, content.WithRef(fmt.Sprintf("write-test-blob-%s", orgDescSys.Digest)))
	require.NoError(t, err)
	_, err = cw.Write(orgBlobBytesSys)
	require.NoError(t, err)
	err = cw.Commit(ctx, 0, cw.Digest())
	require.NoError(t, err)

	// Tests all combination of the conversions from type i to type j preserve
	// the uncompressed digest.
	allCompression := []compression.Type{compression.Uncompressed, compression.Gzip, compression.EStargz, compression.Zstd}
	eg, egctx := errgroup.WithContext(ctx)
	for _, orgDesc := range []ocispecs.Descriptor{orgDescGo, orgDescSys} {
		for _, i := range allCompression {
			compSrc := compression.New(i)
			for _, j := range allCompression {
				i, j, orgDesc := i, j, orgDesc
				compDest := compression.New(j)
				eg.Go(func() error {
					testName := fmt.Sprintf("%s=>%s", i, j)

					// Prepare the source compression type
					convertFunc, err := converter.New(egctx, store, orgDesc, compSrc)
					require.NoError(t, err, testName)
					srcDesc := &orgDesc
					if convertFunc != nil {
						srcDesc, err = convertFunc(egctx, store, orgDesc)
						require.NoError(t, err, testName)
					}

					// Convert the blob
					convertFunc, err = converter.New(egctx, store, *srcDesc, compDest)
					require.NoError(t, err, testName)
					resDesc := srcDesc
					if convertFunc != nil {
						resDesc, err = convertFunc(egctx, store, *srcDesc)
						require.NoError(t, err, testName)
					}

					// Check the uncompressed digest is the same as the original
					convertFunc, err = converter.New(egctx, store, *resDesc, compression.New(compression.Uncompressed))
					require.NoError(t, err, testName)
					recreatedDesc := resDesc
					if convertFunc != nil {
						recreatedDesc, err = convertFunc(egctx, store, *resDesc)
						require.NoError(t, err, testName)
					}
					require.Equal(t, recreatedDesc.Digest, orgDesc.Digest, testName)
					require.NotNil(t, recreatedDesc.Annotations)
					require.Equal(t, recreatedDesc.Annotations[labels.LabelUncompressed], orgDesc.Digest.String(), testName)
					return nil
				})
			}
		}
	}
	require.NoError(t, eg.Wait())
}

type idxToVariants []map[compression.Type]ocispecs.Descriptor

func TestGetRemotes(t *testing.T) {
	t.Parallel()
	// windows fails when lazy blob is being extracted with "invalid windows mount type: 'bind'"
	if runtime.GOOS != "linux" {
		t.Skipf("unsupported GOOS: %s", runtime.GOOS)
	}

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager

	ctx, done, err := leaseutil.WithLease(ctx, co.lm, leaseutil.MakeTemporary)
	require.NoError(t, err)
	defer done(context.TODO())

	contentBuffer := contentutil.NewBuffer()

	descHandlers := DescHandlers(map[digest.Digest]*DescHandler{})

	// make some lazy refs from blobs
	expectedContent := map[digest.Digest]struct{}{}
	var descs []ocispecs.Descriptor
	for i := 0; i < 2; i++ {
		blobmap := map[string]string{"foo": strconv.Itoa(i)}
		blobBytes, desc, err := mapToBlob(blobmap, true)
		require.NoError(t, err)

		expectedContent[desc.Digest] = struct{}{}
		descs = append(descs, desc)

		cw, err := contentBuffer.Writer(ctx)
		require.NoError(t, err)
		_, err = cw.Write(blobBytes)
		require.NoError(t, err)
		err = cw.Commit(ctx, 0, cw.Digest())
		require.NoError(t, err)

		descHandlers[desc.Digest] = &DescHandler{
			Provider: func(_ session.Group) content.Provider { return contentBuffer },
		}

		uncompressedBlobBytes, uncompressedDesc, err := mapToBlob(blobmap, false)
		require.NoError(t, err)
		expectedContent[uncompressedDesc.Digest] = struct{}{}

		esgzDgst, err := esgzBlobDigest(uncompressedBlobBytes)
		require.NoError(t, err)
		expectedContent[esgzDgst] = struct{}{}

		zstdDigest, err := zstdBlobDigest(uncompressedBlobBytes)
		require.NoError(t, err)
		expectedContent[zstdDigest] = struct{}{}
	}

	// Create 3 levels of mutable refs, where each parent ref has 2 children (this tests parallel creation of
	// overlapping blob chains).
	lazyRef, err := cm.GetByBlob(ctx, descs[0], nil, descHandlers)
	require.NoError(t, err)

	refs := []ImmutableRef{lazyRef}
	for i := 0; i < 3; i++ {
		var newRefs []ImmutableRef
		for j, ir := range refs {
			for k := 0; k < 2; k++ {
				mutRef, err := cm.New(ctx, ir, nil, descHandlers)
				require.NoError(t, err)

				m, err := mutRef.Mount(ctx, false, nil)
				require.NoError(t, err)

				lm := snapshot.LocalMounter(m)
				target, err := lm.Mount()
				require.NoError(t, err)

				f, err := os.Create(filepath.Join(target, fmt.Sprintf("%d-%d-%d", i, j, k)))
				require.NoError(t, err)
				err = os.Chtimes(f.Name(), time.Unix(0, 0), time.Unix(0, 0))
				require.NoError(t, err)

				_, desc, err := fileToBlob(f, true)
				require.NoError(t, err)
				expectedContent[desc.Digest] = struct{}{}
				uncompressedBlobBytes, uncompressedDesc, err := fileToBlob(f, false)
				require.NoError(t, err)
				expectedContent[uncompressedDesc.Digest] = struct{}{}

				esgzDgst, err := esgzBlobDigest(uncompressedBlobBytes)
				require.NoError(t, err)
				expectedContent[esgzDgst] = struct{}{}

				zstdDigest, err := zstdBlobDigest(uncompressedBlobBytes)
				require.NoError(t, err)
				expectedContent[zstdDigest] = struct{}{}

				f.Close()
				err = lm.Unmount()
				require.NoError(t, err)

				immutRef, err := mutRef.Commit(ctx)
				require.NoError(t, err)
				newRefs = append(newRefs, immutRef)
			}
		}
		refs = newRefs
	}

	// also test the original lazyRef to get coverage for refs that don't have to be extracted from the snapshotter
	lazyRef2, err := cm.GetByBlob(ctx, descs[1], nil, descHandlers)
	require.NoError(t, err)
	refs = append(refs, lazyRef2)

	checkNumBlobs(ctx, t, co.cs, 1)

	variantsMap := make(map[string]idxToVariants)
	var variantsMapMu sync.Mutex

	// Call GetRemotes on all the refs
	eg, egctx := errgroup.WithContext(ctx)
	for _, ir := range refs {
		ir := ir.(*immutableRef)
		for _, compressionType := range []compression.Type{compression.Uncompressed, compression.Gzip, compression.EStargz, compression.Zstd} {
			compressionType := compressionType
			refCfg := config.RefConfig{Compression: compression.New(compressionType).SetForce(true)}
			eg.Go(func() error {
				remotes, err := ir.GetRemotes(egctx, true, refCfg, false, nil)
				require.NoError(t, err)
				require.Equal(t, 1, len(remotes))
				remote := remotes[0]
				refChain := ir.layerChain()
				for i, desc := range remote.Descriptors {
					switch compressionType {
					case compression.Uncompressed:
						require.Equal(t, ocispecs.MediaTypeImageLayer, desc.MediaType)
					case compression.Gzip:
						require.Equal(t, ocispecs.MediaTypeImageLayerGzip, desc.MediaType)
					case compression.EStargz:
						require.Equal(t, ocispecs.MediaTypeImageLayerGzip, desc.MediaType)
					case compression.Zstd:
						require.Equal(t, ocispecs.MediaTypeImageLayer+"+zstd", desc.MediaType)
					default:
						require.Fail(t, "unhandled media type", compressionType)
					}
					dgst := desc.Digest
					require.Contains(t, expectedContent, dgst, "for %v", compressionType)
					checkDescriptor(ctx, t, co.cs, desc, compressionType)

					variantsMapMu.Lock()
					if len(variantsMap[ir.ID()]) == 0 {
						variantsMap[ir.ID()] = make(idxToVariants, len(remote.Descriptors))
					}
					variantsMapMu.Unlock()

					require.Equal(t, len(remote.Descriptors), len(variantsMap[ir.ID()]))

					variantsMapMu.Lock()
					if variantsMap[ir.ID()][i] == nil {
						variantsMap[ir.ID()][i] = make(map[compression.Type]ocispecs.Descriptor)
					}
					variantsMap[ir.ID()][i][compressionType] = desc
					variantsMapMu.Unlock()

					r := refChain[i]
					isLazy, err := r.isLazy(egctx)
					require.NoError(t, err)
					needs, err := compressionType.NeedsConversion(ctx, co.cs, desc)
					require.NoError(t, err)
					if needs {
						require.False(t, isLazy, "layer %q requires conversion so it must be unlazied", desc.Digest)
					}
					bDesc, err := r.getBlobWithCompression(egctx, compressionType)
					if isLazy {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						checkDescriptor(ctx, t, co.cs, bDesc, compressionType)
						require.Equal(t, desc.Digest, bDesc.Digest)
					}
				}
				return nil
			})
		}
	}
	require.NoError(t, eg.Wait())

	// verify there's a 1-to-1 mapping between the content store and what we expected to be there
	err = co.cs.Walk(ctx, func(info content.Info) error {
		dgst := info.Digest
		var matched bool
		for expected := range expectedContent {
			if dgst == expected {
				delete(expectedContent, expected)
				matched = true
				break
			}
		}
		require.True(t, matched, "unexpected blob: %s", info.Digest)
		checkInfo(ctx, t, co.cs, info)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, map[digest.Digest]struct{}{}, expectedContent)

	// Check if "all" option returns all available blobs
	for _, ir := range refs {
		ir := ir.(*immutableRef)
		variantsMapMu.Lock()
		variants, ok := variantsMap[ir.ID()]
		variantsMapMu.Unlock()
		require.True(t, ok, ir.ID())
		for _, compressionType := range []compression.Type{compression.Uncompressed, compression.Gzip, compression.EStargz, compression.Zstd} {
			compressionType := compressionType
			refCfg := config.RefConfig{Compression: compression.New(compressionType)}
			eg.Go(func() error {
				remotes, err := ir.GetRemotes(egctx, false, refCfg, true, nil)
				require.NoError(t, err)
				require.True(t, len(remotes) > 0, "for %s : %d", compressionType, len(remotes))
				gotMain, gotVariants := remotes[0], remotes[1:]

				// Check the main blob is compatible with all == false
				mainOnly, err := ir.GetRemotes(egctx, false, refCfg, false, nil)
				require.NoError(t, err)
				require.Equal(t, 1, len(mainOnly))
				mainRemote := mainOnly[0]
				require.Equal(t, len(mainRemote.Descriptors), len(gotMain.Descriptors))
				for i := 0; i < len(mainRemote.Descriptors); i++ {
					require.Equal(t, mainRemote.Descriptors[i].Digest, gotMain.Descriptors[i].Digest)
				}

				// Check all variants are covered
				checkVariantsCoverage(egctx, t, variants, len(remotes[0].Descriptors)-1, gotVariants, &compressionType)
				return nil
			})
		}
	}
	require.NoError(t, eg.Wait())
}

func checkVariantsCoverage(ctx context.Context, t *testing.T, variants idxToVariants, idx int, remotes []*solver.Remote, expectCompression *compression.Type) {
	if idx < 0 {
		for _, r := range remotes {
			require.Equal(t, len(r.Descriptors), 0)
		}
		return
	}

	// check the contents of the topmost blob of each remote
	got := make(map[digest.Digest][]*solver.Remote)
	for _, r := range remotes {
		require.Equal(t, len(r.Descriptors)-1, idx, "idx = %d", idx)

		// record this variant
		topmost, lower := r.Descriptors[idx], r.Descriptors[:idx]
		got[topmost.Digest] = append(got[topmost.Digest], &solver.Remote{Descriptors: lower, Provider: r.Provider})

		// check the contents
		r, err := r.Provider.ReaderAt(ctx, topmost)
		require.NoError(t, err)
		dgstr := digest.Canonical.Digester()
		_, err = io.Copy(dgstr.Hash(), io.NewSectionReader(r, 0, topmost.Size))
		require.NoError(t, err)
		require.NoError(t, r.Close())
		require.Equal(t, dgstr.Digest(), topmost.Digest)
	}

	// check the lowers as well
	eg, egctx := errgroup.WithContext(ctx)
	for _, lowers := range got {
		lowers := lowers
		eg.Go(func() error {
			checkVariantsCoverage(egctx, t, variants, idx-1, lowers, nil) // expect all compression variants
			return nil
		})
	}
	require.NoError(t, eg.Wait())

	// check the coverage of the variants
	targets := variants[idx]
	if expectCompression != nil {
		c, ok := variants[idx][*expectCompression]
		require.True(t, ok, "idx = %d, compression = %q, variants = %+v, got = %+v", idx, *expectCompression, variants[idx], got)
		targets = map[compression.Type]ocispecs.Descriptor{*expectCompression: c}
	}
	for c, d := range targets {
		_, ok := got[d.Digest]
		require.True(t, ok, "idx = %d, compression = %q, want = %+v, got = %+v", idx, c, d, got)
		delete(got, d.Digest)
	}
	require.Equal(t, 0, len(got))
}

// Make sure that media type and urls are persisted for non-distributable blobs.
func TestNondistributableBlobs(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	cm := co.manager

	ctx, done, err := leaseutil.WithLease(ctx, co.lm, leaseutil.MakeTemporary)
	require.NoError(t, err)
	defer done(context.TODO())

	contentBuffer := contentutil.NewBuffer()
	descHandlers := DescHandlers(map[digest.Digest]*DescHandler{})

	data, desc, err := mapToBlob(map[string]string{"foo": "bar"}, false)
	require.NoError(t, err)

	// Pretend like this is non-distributable
	desc.MediaType = ocispecs.MediaTypeImageLayerNonDistributable //nolint:staticcheck // ignore SA1019: Non-distributable layers are deprecated, and not recommended for future use.
	desc.URLs = []string{"https://buildkit.moby.dev/foo"}

	cw, err := contentBuffer.Writer(ctx)
	require.NoError(t, err)
	_, err = cw.Write(data)
	require.NoError(t, err)
	err = cw.Commit(ctx, 0, cw.Digest())
	require.NoError(t, err)

	descHandlers[desc.Digest] = &DescHandler{
		Provider: func(_ session.Group) content.Provider { return contentBuffer },
	}

	ref, err := cm.GetByBlob(ctx, desc, nil, descHandlers)
	require.NoError(t, err)

	remotes, err := ref.GetRemotes(ctx, true, config.RefConfig{PreferNonDistributable: true}, false, nil)
	require.NoError(t, err)

	desc2 := remotes[0].Descriptors[0]

	require.Equal(t, desc.MediaType, desc2.MediaType)
	require.Equal(t, desc.URLs, desc2.URLs)

	remotes, err = ref.GetRemotes(ctx, true, config.RefConfig{PreferNonDistributable: false}, false, nil)
	require.NoError(t, err)

	desc2 = remotes[0].Descriptors[0]

	require.Equal(t, ocispecs.MediaTypeImageLayer, desc2.MediaType)
	require.Len(t, desc2.URLs, 0)
}

func checkInfo(ctx context.Context, t *testing.T, cs content.Store, info content.Info) {
	if info.Labels == nil {
		return
	}
	uncompressedDgst, ok := info.Labels[labels.LabelUncompressed]
	if !ok {
		return
	}
	ra, err := cs.ReaderAt(ctx, ocispecs.Descriptor{Digest: info.Digest})
	require.NoError(t, err)
	defer ra.Close()
	decompressR, err := ctdcompression.DecompressStream(io.NewSectionReader(ra, 0, ra.Size()))
	require.NoError(t, err)

	diffID := digest.Canonical.Digester()
	_, err = io.Copy(diffID.Hash(), decompressR)
	require.NoError(t, err)
	require.Equal(t, diffID.Digest().String(), uncompressedDgst)
}

func checkDescriptor(ctx context.Context, t *testing.T, cs content.Store, desc ocispecs.Descriptor, compressionType compression.Type) {
	if desc.Annotations == nil {
		return
	}

	// Check annotations exist
	uncompressedDgst, ok := desc.Annotations[labels.LabelUncompressed]
	require.True(t, ok, "uncompressed digest annotation not found: %q", desc.Digest)
	var uncompressedSize int64
	if compressionType == compression.EStargz {
		_, ok := desc.Annotations[estargz.TOCJSONDigestAnnotation]
		require.True(t, ok, "toc digest annotation not found: %q", desc.Digest)
		uncompressedSizeS, ok := desc.Annotations[estargz.StoreUncompressedSizeAnnotation]
		require.True(t, ok, "uncompressed size annotation not found: %q", desc.Digest)
		var err error
		uncompressedSize, err = strconv.ParseInt(uncompressedSizeS, 10, 64)
		require.NoError(t, err)
	}

	// Check annotation values are valid
	c := new(iohelper.Counter)
	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil && errdefs.IsNotFound(err) {
		return // lazy layer
	}
	require.NoError(t, err)
	defer ra.Close()
	decompressR, err := ctdcompression.DecompressStream(io.NewSectionReader(ra, 0, ra.Size()))
	require.NoError(t, err)

	diffID := digest.Canonical.Digester()
	_, err = io.Copy(io.MultiWriter(diffID.Hash(), c), decompressR)
	require.NoError(t, err)
	require.Equal(t, diffID.Digest().String(), uncompressedDgst)
	if compressionType == compression.EStargz {
		require.Equal(t, c.Size(), uncompressedSize)
	}
}

func TestMergeOp(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "freebsd" {
		t.Skipf("Depends on unimplemented merge-op support on %s", runtime.GOOS)
	}

	// This just tests the basic Merge method and some of the logic with releasing merge refs.
	// Tests for the fs merge logic are in client_test and snapshotter_test.
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager

	emptyMerge, err := cm.Merge(ctx, nil, nil)
	require.NoError(t, err)
	require.Nil(t, emptyMerge)

	var baseRefs []ImmutableRef
	for i := 0; i < 6; i++ {
		active, err := cm.New(ctx, nil, nil)
		require.NoError(t, err)
		m, err := active.Mount(ctx, false, nil)
		require.NoError(t, err)
		lm := snapshot.LocalMounter(m)
		target, err := lm.Mount()
		require.NoError(t, err)
		err = fstest.Apply(
			fstest.CreateFile(strconv.Itoa(i), []byte(strconv.Itoa(i)), 0777),
		).Apply(target)
		require.NoError(t, err)
		err = lm.Unmount()
		require.NoError(t, err)
		snap, err := active.Commit(ctx)
		require.NoError(t, err)
		baseRefs = append(baseRefs, snap)
		size, err := snap.(*immutableRef).size(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 8192, size)
	}

	singleMerge, err := cm.Merge(ctx, baseRefs[:1], nil)
	require.NoError(t, err)
	require.True(t, singleMerge.(*immutableRef).getCommitted())
	m, err := singleMerge.Mount(ctx, true, nil)
	require.NoError(t, err)
	ms, unmount, err := m.Mount()
	require.NoError(t, err)
	require.Len(t, ms, 1)
	require.Equal(t, ms[0].Type, "bind")
	err = fstest.CheckDirectoryEqualWithApplier(ms[0].Source, fstest.Apply(
		fstest.CreateFile(strconv.Itoa(0), []byte(strconv.Itoa(0)), 0777),
	))
	require.NoError(t, err)
	require.NoError(t, unmount())
	require.NoError(t, singleMerge.Release(ctx))

	err = cm.Prune(ctx, nil, client.PruneInfo{Filter: []string{
		"id==" + singleMerge.ID(),
	}})
	require.NoError(t, err)

	merge1, err := cm.Merge(ctx, baseRefs[:3], nil)
	require.NoError(t, err)
	require.True(t, merge1.(*immutableRef).getCommitted())
	_, err = merge1.Mount(ctx, true, nil)
	require.NoError(t, err)
	size1, err := merge1.(*immutableRef).size(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4096, size1) // hardlinking means all but the first snapshot doesn't take up space
	checkDiskUsage(ctx, t, cm, 7, 0)

	merge2, err := cm.Merge(ctx, baseRefs[3:], nil)
	require.NoError(t, err)
	require.True(t, merge2.(*immutableRef).getCommitted())
	_, err = merge2.Mount(ctx, true, nil)
	require.NoError(t, err)
	size2, err := merge2.(*immutableRef).size(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4096, size2)
	checkDiskUsage(ctx, t, cm, 8, 0)

	for _, ref := range baseRefs {
		require.NoError(t, ref.Release(ctx))
	}
	checkDiskUsage(ctx, t, cm, 8, 0)
	// should still be able to use merges based on released refs

	merge3, err := cm.Merge(ctx, []ImmutableRef{merge1, merge2}, nil)
	require.NoError(t, err)
	require.True(t, merge3.(*immutableRef).getCommitted())
	require.NoError(t, merge1.Release(ctx))
	require.NoError(t, merge2.Release(ctx))
	_, err = merge3.Mount(ctx, true, nil)
	require.NoError(t, err)
	size3, err := merge3.(*immutableRef).size(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4096, size3)
	require.Len(t, merge3.(*immutableRef).mergeParents, 6)
	checkDiskUsage(ctx, t, cm, 7, 2)

	require.NoError(t, merge3.Release(ctx))
	checkDiskUsage(ctx, t, cm, 0, 9)
	err = cm.Prune(ctx, nil, client.PruneInfo{All: true})
	require.NoError(t, err)
	checkDiskUsage(ctx, t, cm, 0, 0)
}

func TestDiffOp(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "freebsd" {
		t.Skipf("Depends on unimplemented diff-op support on %s", runtime.GOOS)
	}

	// This just tests the basic Diff method and some of the logic with releasing diff refs.
	// Tests for the fs diff logic are in client_test and snapshotter_test.
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager

	newLower, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)
	lowerA, err := newLower.Commit(ctx)
	require.NoError(t, err)
	newUpper, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)
	upperA, err := newUpper.Commit(ctx)
	require.NoError(t, err)

	// verify that releasing parents does not invalidate a diff ref until it is released
	diff, err := cm.Diff(ctx, lowerA, upperA, nil)
	require.NoError(t, err)
	checkDiskUsage(ctx, t, cm, 3, 0)
	require.NoError(t, lowerA.Release(ctx))
	require.NoError(t, upperA.Release(ctx))
	checkDiskUsage(ctx, t, cm, 3, 0)
	require.NoError(t, cm.Prune(ctx, nil, client.PruneInfo{All: true}))
	checkDiskUsage(ctx, t, cm, 3, 0)
	_, err = diff.Mount(ctx, true, nil)
	require.NoError(t, err)
	require.NoError(t, diff.Release(ctx))
	checkDiskUsage(ctx, t, cm, 0, 3)
	require.NoError(t, cm.Prune(ctx, nil, client.PruneInfo{All: true}))
	checkDiskUsage(ctx, t, cm, 0, 0)

	// test "unmerge" diffs that are defined as a merge of single-layer diffs
	newRef, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)
	a, err := newRef.Commit(ctx)
	require.NoError(t, err)
	newRef, err = cm.New(ctx, a, nil)
	require.NoError(t, err)
	b, err := newRef.Commit(ctx)
	require.NoError(t, err)
	newRef, err = cm.New(ctx, b, nil)
	require.NoError(t, err)
	c, err := newRef.Commit(ctx)
	require.NoError(t, err)
	newRef, err = cm.New(ctx, c, nil)
	require.NoError(t, err)
	d, err := newRef.Commit(ctx)
	require.NoError(t, err)
	newRef, err = cm.New(ctx, d, nil)
	require.NoError(t, err)
	e, err := newRef.Commit(ctx)
	require.NoError(t, err)

	diff, err = cm.Diff(ctx, c, e, nil)
	require.NoError(t, err)
	checkDiskUsage(ctx, t, cm, 8, 0) // 5 base refs + 2 diffs + 1 merge
	require.NoError(t, a.Release(ctx))
	require.NoError(t, b.Release(ctx))
	require.NoError(t, c.Release(ctx))
	require.NoError(t, d.Release(ctx))
	require.NoError(t, e.Release(ctx))
	checkDiskUsage(ctx, t, cm, 8, 0)
	require.NoError(t, cm.Prune(ctx, nil, client.PruneInfo{All: true}))
	checkDiskUsage(ctx, t, cm, 8, 0)
	_, err = diff.Mount(ctx, true, nil)
	require.NoError(t, err)
	require.NoError(t, diff.Release(ctx))
	checkDiskUsage(ctx, t, cm, 0, 8)
	require.NoError(t, cm.Prune(ctx, nil, client.PruneInfo{All: true}))
	checkDiskUsage(ctx, t, cm, 0, 0)

	// Test using nil as upper
	newLower, err = cm.New(ctx, nil, nil)
	require.NoError(t, err)
	lowerB, err := newLower.Commit(ctx)
	require.NoError(t, err)
	diff, err = cm.Diff(ctx, lowerB, nil, nil)
	require.NoError(t, err)
	checkDiskUsage(ctx, t, cm, 2, 0)
	require.NoError(t, lowerB.Release(ctx))
	require.NoError(t, diff.Release(ctx))
	checkDiskUsage(ctx, t, cm, 0, 2)
	require.NoError(t, cm.Prune(ctx, nil, client.PruneInfo{All: true}))
	checkDiskUsage(ctx, t, cm, 0, 0)
}

func TestLoadHalfFinalizedRef(t *testing.T) {
	// This test simulates the situation where a ref w/ an equalMutable has its
	// snapshot committed but there is a crash before the metadata is updated to
	// clear the equalMutable field. It's expected that the mutable will be
	// removed and the immutable ref will continue to be usable.
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		tmpdir:          tmpdir,
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager.(*cacheManager)

	mref, err := cm.New(ctx, nil, nil, CachePolicyRetain)
	require.NoError(t, err)
	mutRef := mref.(*mutableRef)

	iref, err := mutRef.Commit(ctx)
	require.NoError(t, err)
	immutRef := iref.(*immutableRef)

	require.NoError(t, mref.Release(ctx))

	_, err = co.lm.Create(ctx, func(l *leases.Lease) error {
		l.ID = immutRef.ID()
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	require.NoError(t, err)
	err = co.lm.AddResource(ctx, leases.Lease{ID: immutRef.ID()}, leases.Resource{
		ID:   immutRef.getSnapshotID(),
		Type: "snapshots/" + cm.Snapshotter.Name(),
	})
	require.NoError(t, err)

	err = cm.Snapshotter.Commit(ctx, immutRef.getSnapshotID(), mutRef.getSnapshotID())
	require.NoError(t, err)

	_, err = cm.Snapshotter.Stat(ctx, mutRef.getSnapshotID())
	require.Error(t, err)

	require.NoError(t, iref.Release(ctx))

	require.NoError(t, cm.Close())
	cleanup()

	co, cleanup, err = newCacheManager(ctx, t, cmOpt{
		tmpdir:          tmpdir,
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm = co.manager.(*cacheManager)

	_, err = cm.GetMutable(ctx, mutRef.ID())
	require.ErrorIs(t, err, errNotFound)

	iref, err = cm.Get(ctx, immutRef.ID(), nil)
	require.NoError(t, err)
	require.NoError(t, iref.Finalize(ctx))
	immutRef = iref.(*immutableRef)

	_, err = cm.Snapshotter.Stat(ctx, immutRef.getSnapshotID())
	require.NoError(t, err)
}

func TestMountReadOnly(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skipf("unsupported GOOS: %s", runtime.GOOS)
	}

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		snapshotter:     snapshotter,
		snapshotterName: "overlay",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager

	mutRef, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		rwMntable, err := mutRef.Mount(ctx, false, nil)
		require.NoError(t, err)
		rwMnts, release, err := rwMntable.Mount()
		require.NoError(t, err)
		defer release()
		require.Len(t, rwMnts, 1)
		require.False(t, isReadOnly(rwMnts[0]))

		roMntable, err := mutRef.Mount(ctx, true, nil)
		require.NoError(t, err)
		roMnts, release, err := roMntable.Mount()
		require.NoError(t, err)
		defer release()
		require.Len(t, roMnts, 1)
		require.True(t, isReadOnly(roMnts[0]))

		immutRef, err := mutRef.Commit(ctx)
		require.NoError(t, err)

		roMntable, err = immutRef.Mount(ctx, true, nil)
		require.NoError(t, err)
		roMnts, release, err = roMntable.Mount()
		require.NoError(t, err)
		defer release()
		require.Len(t, roMnts, 1)
		require.True(t, isReadOnly(roMnts[0]))

		rwMntable, err = immutRef.Mount(ctx, false, nil)
		require.NoError(t, err)
		rwMnts, release, err = rwMntable.Mount()
		require.NoError(t, err)
		defer release()
		require.Len(t, rwMnts, 1)
		// once immutable, even when readonly=false, the mount is still readonly
		require.True(t, isReadOnly(rwMnts[0]))

		// repeat with a ref that has a parent
		mutRef, err = cm.New(ctx, immutRef, nil)
		require.NoError(t, err)
	}
}

func TestLoadBrokenParents(t *testing.T) {
	// Test that a ref that has a parent that can't be loaded will not result in any leaks
	// of other parent refs
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	co, cleanup, err := newCacheManager(ctx, t, cmOpt{
		tmpdir:          tmpdir,
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm := co.manager.(*cacheManager)

	mutRef, err := cm.New(ctx, nil, nil)
	require.NoError(t, err)
	refA, err := mutRef.Commit(ctx)
	require.NoError(t, err)
	refAID := refA.ID()
	mutRef, err = cm.New(ctx, nil, nil)
	require.NoError(t, err)
	refB, err := mutRef.Commit(ctx)
	require.NoError(t, err)

	_, err = cm.Merge(ctx, []ImmutableRef{refA, refB}, nil)
	require.NoError(t, err)
	checkDiskUsage(ctx, t, cm, 3, 0)

	// set refB as deleted
	require.NoError(t, refB.(*immutableRef).queueDeleted())
	require.NoError(t, refB.(*immutableRef).commitMetadata())
	require.NoError(t, cm.Close())
	cleanup()

	co, cleanup, err = newCacheManager(ctx, t, cmOpt{
		tmpdir:          tmpdir,
		snapshotter:     snapshotter,
		snapshotterName: "native",
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	cm = co.manager.(*cacheManager)

	checkDiskUsage(ctx, t, cm, 0, 1)
	refA, err = cm.Get(ctx, refAID, nil)
	require.NoError(t, err)
	require.Len(t, refA.(*immutableRef).refs, 1)
}

func checkDiskUsage(ctx context.Context, t *testing.T, cm Manager, inuse, unused int) {
	du, err := cm.DiskUsage(ctx, client.DiskUsageInfo{})
	require.NoError(t, err)
	var inuseActual, unusedActual int
	for _, r := range du {
		if r.InUse {
			inuseActual++
		} else {
			unusedActual++
		}
	}
	require.Equal(t, inuse, inuseActual)
	require.Equal(t, unused, unusedActual)
}

func esgzBlobDigest(uncompressedBlobBytes []byte) (blobDigest digest.Digest, err error) {
	esgzDigester := digest.Canonical.Digester()
	w := estargz.NewWriterLevel(esgzDigester.Hash(), gzip.DefaultCompression)
	if err := w.AppendTarLossLess(bytes.NewReader(uncompressedBlobBytes)); err != nil {
		return "", err
	}
	if _, err := w.Close(); err != nil {
		return "", err
	}
	return esgzDigester.Digest(), nil
}

func zstdBlobDigest(uncompressedBlobBytes []byte) (digest.Digest, error) {
	b := bytes.NewBuffer(nil)
	w, err := zstd.NewWriter(b)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(uncompressedBlobBytes); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return digest.FromBytes(b.Bytes()), nil
}

func checkNumBlobs(ctx context.Context, t *testing.T, cs content.Store, expected int) {
	c := 0
	err := cs.Walk(ctx, func(_ content.Info) error {
		c++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, expected, c)
}

func pruneResultBuffer() *buf {
	b := &buf{C: make(chan client.UsageInfo), closed: make(chan struct{})}
	go func() {
		for c := range b.C {
			b.all = append(b.all, c)
		}
		close(b.closed)
	}()
	return b
}

type buf struct {
	C      chan client.UsageInfo
	closed chan struct{}
	all    []client.UsageInfo
}

func (b *buf) close() {
	close(b.C)
	<-b.closed
}

type bufferCloser struct {
	*bytes.Buffer
}

func (b bufferCloser) Close() error {
	return nil
}

func mapToBlob(m map[string]string, compress bool) ([]byte, ocispecs.Descriptor, error) {
	if !compress {
		return mapToBlobWithCompression(m, nil)
	}
	return mapToBlobWithCompression(m, func(w io.Writer) (io.WriteCloser, string, error) {
		return gzip.NewWriter(w), ocispecs.MediaTypeImageLayerGzip, nil
	})
}

func mapToBlobWithCompression(m map[string]string, compress func(io.Writer) (io.WriteCloser, string, error)) ([]byte, ocispecs.Descriptor, error) {
	buf := bytes.NewBuffer(nil)
	sha := digest.SHA256.Digester()

	var dest io.WriteCloser = bufferCloser{buf}
	mediaType := ocispecs.MediaTypeImageLayer
	if compress != nil {
		var err error
		dest, mediaType, err = compress(buf)
		if err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
	}
	tw := tar.NewWriter(io.MultiWriter(sha.Hash(), dest))

	for k, v := range m {
		if err := tw.WriteHeader(&tar.Header{
			Name: k,
			Size: int64(len(v)),
		}); err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
		if _, err := tw.Write([]byte(v)); err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, ocispecs.Descriptor{}, err
	}
	if err := dest.Close(); err != nil {
		return nil, ocispecs.Descriptor{}, err
	}

	return buf.Bytes(), ocispecs.Descriptor{
		Digest:    digest.FromBytes(buf.Bytes()),
		MediaType: mediaType,
		Size:      int64(buf.Len()),
		Annotations: map[string]string{
			labels.LabelUncompressed: sha.Digest().String(),
		},
	}, nil
}

func fileToBlob(file *os.File, compress bool) ([]byte, ocispecs.Descriptor, error) {
	buf := bytes.NewBuffer(nil)
	sha := digest.SHA256.Digester()

	var dest io.WriteCloser = bufferCloser{buf}
	if compress {
		dest = gzip.NewWriter(buf)
	}
	tw := tar.NewWriter(io.MultiWriter(sha.Hash(), dest))

	info, err := file.Stat()
	if err != nil {
		return nil, ocispecs.Descriptor{}, err
	}

	fi, err := tarheader.FileInfoHeaderNoLookups(info, "")
	if err != nil {
		return nil, ocispecs.Descriptor{}, err
	}
	fi.Format = tar.FormatPAX
	fi.ModTime = fi.ModTime.Truncate(time.Second)
	fi.AccessTime = time.Time{}
	fi.ChangeTime = time.Time{}

	if err := tw.WriteHeader(fi); err != nil {
		return nil, ocispecs.Descriptor{}, err
	}
	if _, err := io.Copy(tw, file); err != nil {
		return nil, ocispecs.Descriptor{}, err
	}

	if err := tw.Close(); err != nil {
		return nil, ocispecs.Descriptor{}, err
	}
	if err := dest.Close(); err != nil {
		return nil, ocispecs.Descriptor{}, err
	}

	mediaType := ocispecs.MediaTypeImageLayer
	if compress {
		mediaType = ocispecs.MediaTypeImageLayerGzip
	}
	return buf.Bytes(), ocispecs.Descriptor{
		Digest:    digest.FromBytes(buf.Bytes()),
		MediaType: mediaType,
		Size:      int64(buf.Len()),
		Annotations: map[string]string{
			labels.LabelUncompressed: sha.Digest().String(),
		},
	}, nil
}

func mapToSystemTarBlob(t *testing.T, m map[string]string) ([]byte, ocispecs.Descriptor, error) {
	tmpdir := t.TempDir()

	expected := map[string]string{}
	for k, v := range m {
		expected[k] = v
		if err := os.WriteFile(filepath.Join(tmpdir, k), []byte(v), 0600); err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
	}

	cmd := exec.Command("tar", "-C", tmpdir, "-c", ".")
	tarout, err := cmd.Output()
	if err != nil {
		return nil, ocispecs.Descriptor{}, err
	}

	tr := tar.NewReader(bytes.NewReader(tarout))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
		k := strings.TrimPrefix(filepath.Clean("/"+h.Name), "/")
		if k == "" {
			continue // ignore the root entry
		}
		v, ok := expected[k]
		if !ok {
			return nil, ocispecs.Descriptor{}, errors.Errorf("unexpected file %s", h.Name)
		}
		delete(expected, k)
		gotV, err := io.ReadAll(tr)
		if err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
		if string(gotV) != string(v) {
			return nil, ocispecs.Descriptor{}, errors.Errorf("unexpected contents of %s", h.Name)
		}
	}
	if len(expected) > 0 {
		return nil, ocispecs.Descriptor{}, errors.Errorf("expected file doesn't archived: %+v", expected)
	}

	return tarout, ocispecs.Descriptor{
		Digest:    digest.FromBytes(tarout),
		MediaType: ocispecs.MediaTypeImageLayer,
		Size:      int64(len(tarout)),
		Annotations: map[string]string{
			labels.LabelUncompressed: digest.FromBytes(tarout).String(),
		},
	}, nil
}

func isReadOnly(mnt mount.Mount) bool {
	var hasUpperdir bool
	for _, o := range mnt.Options {
		if o == "ro" {
			return true
		} else if strings.HasPrefix(o, "upperdir=") {
			hasUpperdir = true
		}
	}
	if overlay.IsOverlayMountType(mnt) {
		return !hasUpperdir
	}
	return false
}
