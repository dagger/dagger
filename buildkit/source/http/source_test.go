package http

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/snapshot"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/source"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/testutil/httpserver"
	"github.com/moby/buildkit/util/winlayers"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestHTTPSource(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	hs, err := newHTTPSource(t)
	require.NoError(t, err)

	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: []byte("content1"),
	}
	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/foo": resp,
	})
	defer server.Close()

	id := &HTTPIdentifier{URL: server.URL + "/foo"}

	h, err := hs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	k, p, _, _, err := h.CacheKey(ctx, nil, 0)
	require.NoError(t, err)

	expectedContent1 := "sha256:0b1a154faa3003c1fbe7fda9c8a42d55fde2df2a2c405c32038f8ac7ed6b044a"
	expectedPin1 := "sha256:d0b425e00e15a0d36b9b361f02bab63563aed6cb4665083905386c55d5b679fa"

	require.Equal(t, expectedContent1, k)
	require.Equal(t, expectedPin1, p)
	require.Equal(t, server.Stats("/foo").AllRequests, 1)
	require.Equal(t, server.Stats("/foo").CachedRequests, 0)

	ref, err := h.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if ref != nil {
			ref.Release(context.TODO())
			ref = nil
		}
	}()

	dt, err := readFile(ctx, ref, "foo")
	require.NoError(t, err)
	require.Equal(t, dt, []byte("content1"))

	ref.Release(context.TODO())
	ref = nil

	// repeat, should use the etag
	h, err = hs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	k, p, _, _, err = h.CacheKey(ctx, nil, 0)
	require.NoError(t, err)

	require.Equal(t, expectedContent1, k)
	require.Equal(t, expectedPin1, p)
	require.Equal(t, server.Stats("/foo").AllRequests, 2)
	require.Equal(t, server.Stats("/foo").CachedRequests, 1)

	ref, err = h.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if ref != nil {
			ref.Release(context.TODO())
			ref = nil
		}
	}()

	dt, err = readFile(ctx, ref, "foo")
	require.NoError(t, err)
	require.Equal(t, dt, []byte("content1"))

	ref.Release(context.TODO())
	ref = nil

	resp2 := httpserver.Response{
		Etag:    identity.NewID(),
		Content: []byte("content2"),
	}

	expectedContent2 := "sha256:888722f299c02bfae173a747a0345bb2291cf6a076c36d8eb6fab442a8adddfa"
	expectedPin2 := "sha256:dab741b6289e7dccc1ed42330cae1accc2b755ce8079c2cd5d4b5366c9f769a6"

	// update etag, downloads again
	server.SetRoute("/foo", resp2)

	h, err = hs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	k, p, _, _, err = h.CacheKey(ctx, nil, 0)
	require.NoError(t, err)

	require.Equal(t, expectedContent2, k)
	require.Equal(t, expectedPin2, p)
	require.Equal(t, server.Stats("/foo").AllRequests, 4)
	require.Equal(t, server.Stats("/foo").CachedRequests, 1)

	ref, err = h.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if ref != nil {
			ref.Release(context.TODO())
			ref = nil
		}
	}()

	dt, err = readFile(ctx, ref, "foo")
	require.NoError(t, err)
	require.Equal(t, dt, []byte("content2"))

	ref.Release(context.TODO())
	ref = nil
}

func TestHTTPDefaultName(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	hs, err := newHTTPSource(t)
	require.NoError(t, err)

	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: []byte("content1"),
	}
	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/": resp,
	})
	defer server.Close()

	id := &HTTPIdentifier{URL: server.URL}

	h, err := hs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	k, p, _, _, err := h.CacheKey(ctx, nil, 0)
	require.NoError(t, err)

	require.Equal(t, "sha256:146f16ec8810a62a57ce314aba391f95f7eaaf41b8b1ebaf2ab65fd63b1ad437", k)
	require.Equal(t, "sha256:d0b425e00e15a0d36b9b361f02bab63563aed6cb4665083905386c55d5b679fa", p)
	require.Equal(t, server.Stats("/").AllRequests, 1)
	require.Equal(t, server.Stats("/").CachedRequests, 0)

	ref, err := h.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if ref != nil {
			ref.Release(context.TODO())
			ref = nil
		}
	}()

	dt, err := readFile(ctx, ref, "download")
	require.NoError(t, err)
	require.Equal(t, dt, []byte("content1"))

	ref.Release(context.TODO())
	ref = nil
}

func TestHTTPInvalidURL(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	hs, err := newHTTPSource(t)
	require.NoError(t, err)

	server := httpserver.NewTestServer(map[string]httpserver.Response{})
	defer server.Close()

	id := &HTTPIdentifier{URL: server.URL + "/foo"}

	h, err := hs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	_, _, _, _, err = h.CacheKey(ctx, nil, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid response")
}

func TestHTTPChecksum(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	hs, err := newHTTPSource(t)
	require.NoError(t, err)

	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: []byte("content-correct"),
	}
	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/foo": resp,
	})
	defer server.Close()

	id := &HTTPIdentifier{URL: server.URL + "/foo", Checksum: digest.FromBytes([]byte("content-different"))}

	h, err := hs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	k, p, _, _, err := h.CacheKey(ctx, nil, 0)
	require.NoError(t, err)

	expectedContentDifferent := "sha256:f25996f463dca69cffb580f8273ffacdda43332b5f0a8bea2ead33900616d44b"
	expectedContentCorrect := "sha256:c6a440110a7757b9e1e47b52e413cba96c62377c37a474714b6b3c4f8b74e536"
	expectedPinDifferent := "sha256:ab0d5a7aa55c1c95d59c302eb12c55368940e6f0a257646afd455cabe248edc4"
	expectedPinCorrect := "sha256:f5fa14774044d2ec428ffe7efbfaa0a439db7bc8127d6b71aea21e1cd558d0f0"

	require.Equal(t, expectedContentDifferent, k)
	require.Equal(t, expectedPinDifferent, p)
	require.Equal(t, server.Stats("/foo").AllRequests, 0)
	require.Equal(t, server.Stats("/foo").CachedRequests, 0)

	_, err = h.Snapshot(ctx, nil)
	require.Error(t, err)

	require.Equal(t, expectedContentDifferent, k)
	require.Equal(t, expectedPinDifferent, p)
	require.Equal(t, server.Stats("/foo").AllRequests, 1)
	require.Equal(t, server.Stats("/foo").CachedRequests, 0)

	id = &HTTPIdentifier{URL: server.URL + "/foo", Checksum: digest.FromBytes([]byte("content-correct"))}

	h, err = hs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	k, p, _, _, err = h.CacheKey(ctx, nil, 0)
	require.NoError(t, err)

	require.Equal(t, expectedContentCorrect, k)
	require.Equal(t, expectedPinCorrect, p)
	require.Equal(t, server.Stats("/foo").AllRequests, 1)
	require.Equal(t, server.Stats("/foo").CachedRequests, 0)

	ref, err := h.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if ref != nil {
			ref.Release(context.TODO())
			ref = nil
		}
	}()

	dt, err := readFile(ctx, ref, "foo")
	require.NoError(t, err)
	require.Equal(t, dt, []byte("content-correct"))

	require.Equal(t, expectedContentCorrect, k)
	require.Equal(t, expectedPinCorrect, p)
	require.Equal(t, server.Stats("/foo").AllRequests, 2)
	require.Equal(t, server.Stats("/foo").CachedRequests, 0)

	ref.Release(context.TODO())
	ref = nil
}

func readFile(ctx context.Context, ref cache.ImmutableRef, fp string) ([]byte, error) {
	mount, err := ref.Mount(ctx, true, nil)
	if err != nil {
		return nil, err
	}

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	if err != nil {
		return nil, err
	}

	defer lm.Unmount()

	dt, err := os.ReadFile(filepath.Join(dir, fp))
	if err != nil {
		return nil, err
	}

	return dt, nil
}

func newHTTPSource(t *testing.T) (source.Source, error) {
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

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
		"native": snapshotter,
	})

	md, err := metadata.NewStore(filepath.Join(tmpdir, "metadata.db"))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, md.Close())
	})

	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), "buildkit")
	c := mdb.ContentStore()
	applier := winlayers.NewFileSystemApplierWithWindows(c, apply.NewFileSystemApplier(c))
	differ := winlayers.NewWalkingDiffWithWindows(c, walking.NewWalkingDiff(c))

	cm, err := cache.NewManager(cache.ManagerOpt{
		Snapshotter:    snapshot.FromContainerdSnapshotter("native", containerdsnapshot.NSSnapshotter("buildkit", mdb.Snapshotter("native")), nil),
		MetadataStore:  md,
		LeaseManager:   lm,
		ContentStore:   c,
		Applier:        applier,
		Differ:         differ,
		GarbageCollect: mdb.GarbageCollect,
		MountPoolRoot:  filepath.Join(tmpdir, "cachemounts"),
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, cm.Close())
	})

	return NewSource(Opt{
		CacheAccessor: cm,
	})
}
