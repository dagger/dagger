package contenthash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	ctdmount "github.com/containerd/containerd/v2/core/mount"
	cache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"gotest.tools/v3/assert"
)

// Checksumming a file must not walk its parent directory: for a file at the
// root of a container filesystem that is a walk of the whole filesystem.
// Recording only the file must not change any digest, including the digests
// of directories checksummed later from the same cache context.
func TestChecksumFileDoesNotWalkParent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "stamp", "stamp")
	writeFile(t, root, "work/b001/b001.a", "archive")
	for i := range 100 {
		writeFile(t, root, fmt.Sprintf("usr/lib/f%d", i), "lib")
	}
	assert.NilError(t, os.Symlink("stamp", filepath.Join(root, "link")))
	mnt := bindMountable{root}

	// Reference digests from a context that walked the whole tree first.
	full := newTestCacheContext(t)
	want := map[string]digest.Digest{}
	for _, p := range []string{"/", "/stamp", "/link", "/work", "/work/b001/b001.a", "/usr/lib/f7"} {
		dgst, err := full.Checksum(ctx, mnt, p, ChecksumOpts{FollowLinks: true})
		assert.NilError(t, err)
		want[p] = dgst
	}
	linkNoFollow, err := full.Checksum(ctx, mnt, "/link", ChecksumOpts{})
	assert.NilError(t, err)

	scanCounterEnable = true
	t.Cleanup(func() { scanCounterEnable = false })

	cc := newTestCacheContext(t)
	for _, p := range []string{"/stamp", "/link", "/work/b001/b001.a"} {
		scanCounter.Store(0)
		dgst, err := cc.Checksum(ctx, mnt, p, ChecksumOpts{FollowLinks: true})
		assert.NilError(t, err)
		assert.Equal(t, dgst, want[p], p)
		assert.Assert(t, scanCounter.Load() <= 1, "%s: scanned %d entries", p, scanCounter.Load())
	}
	dgst, err := cc.Checksum(ctx, mnt, "/link", ChecksumOpts{})
	assert.NilError(t, err)
	assert.Equal(t, dgst, linkNoFollow)

	// Directories and siblings checksummed after the file-only records still
	// get the digests of a full walk.
	for _, p := range []string{"/work", "/usr/lib/f7", "/"} {
		dgst, err := cc.Checksum(ctx, mnt, p, ChecksumOpts{FollowLinks: true})
		assert.NilError(t, err)
		assert.Equal(t, dgst, want[p], p)
	}

	_, err = newTestCacheContext(t).Checksum(ctx, mnt, "/missing", ChecksumOpts{FollowLinks: true})
	assert.Assert(t, errors.Is(err, errNotFound), "got %v", err)
	_, err = newTestCacheContext(t).Checksum(ctx, mnt, "/stamp/missing", ChecksumOpts{FollowLinks: true})
	assert.Assert(t, err != nil)
}

// Hashing a file of an immutable snapshot reads it through the snapshot's
// shared read-only mount, which the snapshot's next reader reuses.
func TestChecksumUsesSharedMount(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	// A linger long enough that the mount stays while the test runs.
	store.WithSharedMounts(t, time.Hour, 0)
	ref, _ := store.Build(t, nil, "work/b001/_pkg_.a", "archive")

	dgst, err := Checksum(ctx, ref, "/work/b001/_pkg_.a", ChecksumOpts{FollowLinks: true})
	assert.NilError(t, err)
	assert.Equal(t, store.LocalMounts.Load(), int64(1), "hashing mounts the shared mount")

	shared, ok := ref.(cache.SharedMounter)
	assert.Assert(t, ok)
	_, _, release, err := shared.MountShared(ctx)
	assert.NilError(t, err)
	release()
	assert.Equal(t, store.LocalMounts.Load(), int64(1), "the next reader reuses it")

	want, err := newTestCacheContext(t).Checksum(ctx, bindMountable{testutil.Root(t, ref)}, "/work/b001/_pkg_.a", ChecksumOpts{FollowLinks: true})
	assert.NilError(t, err)
	assert.Equal(t, dgst, want)
}

func writeFile(t *testing.T, root, p, contents string) {
	t.Helper()
	fp := filepath.Join(root, p)
	assert.NilError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
	assert.NilError(t, os.WriteFile(fp, []byte(contents), 0o644))
}

func newTestCacheContext(t *testing.T) *cacheContext {
	t.Helper()
	cc, err := newCacheContext(testRefMetadata{})
	assert.NilError(t, err)
	return cc
}

// testRefMetadata stores nothing; the cache context only reads and writes its
// serialized tree through it.
type testRefMetadata struct {
	cache.RefMetadata
}

func (testRefMetadata) GetExternal(string) ([]byte, error) {
	return nil, errors.New("not found")
}

func (testRefMetadata) SetExternal(string, []byte) error {
	return nil
}

// bindMountable mounts a host directory. A writable bind mount is used as is,
// so no mount is performed.
type bindMountable struct {
	dir string
}

func (m bindMountable) Mount(context.Context, bool) (cache.MountableRef, error) {
	return bindMounts(m), nil
}

type bindMounts bindMountable

func (m bindMounts) Mount() ([]ctdmount.Mount, func() error, error) {
	return []ctdmount.Mount{{Type: "bind", Source: m.dir, Options: []string{"rbind"}}}, func() error { return nil }, nil
}
