package contenthash

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/dagger/dagger/buildkit/cache"
	"github.com/dagger/dagger/buildkit/cache/metadata"
	"github.com/dagger/dagger/buildkit/session"
	"github.com/dagger/dagger/buildkit/snapshot"
	containerdsnapshot "github.com/dagger/dagger/buildkit/snapshot/containerd"
	"github.com/dagger/dagger/buildkit/util/leaseutil"
	"github.com/dagger/dagger/buildkit/util/winlayers"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	bolt "go.etcd.io/bbolt"
)

const (
	dgstFileData0       = digest.Digest("sha256:cd8e75bca50f2d695f220d0cb0997d8ead387e4f926e8669a92d7f104cc9885b")
	dgstDirD0           = digest.Digest("sha256:d47454417d2c554067fbefe5f5719edc49f3cfe969c36b62e34a187a4da0cc9a")
	dgstDirD0FileByFile = digest.Digest("sha256:231c3293e329de47fec9e79056686477891fd1f244ed7b1c1fa668489a1f0d50")
	dgstDirD0Modified   = digest.Digest("sha256:555ffa3028630d97ba37832b749eda85ab676fd64ffb629fbf0f4ec8c1e3bff1")
	dgstDoubleStar      = digest.Digest("sha256:853b46abef38d02c9e29fdd1557c6002903b262541e60064bc84518d4d3a6f11")
)

func TestChecksumSymlinkNoParentScan(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD aa dir",
		"ADD aa/bb dir",
		"ADD aa/bb/cc dir",
		"ADD aa/bb/cc/dd file data0",
		"ADD aa/ln symlink /aa",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "aa/ln/bb/cc/dd", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	// The above checksum request should have only checksummed aa/bb/cc, and so
	// any parent directories should need a scan but non-existent (or existent)
	// children should not.
	root := cc.tree.Root()

	for _, path := range []string{
		// Paths not within the scanned /aa/bb/cc/.
		"/", "/aa", "/aa/bb", "/aa/bb/ff", "/non-exist",
	} {
		needs1, err := cc.needsScan(root, path, false)
		require.NoErrorf(t, err, "needsScan(%q, followTrailing=false)", path)
		require.Truef(t, needs1, "needsScan(%q, followTrailing=false)", path)

		needs2, err := cc.needsScan(root, path, true)
		require.NoErrorf(t, err, "needsScan(%q, followTrailing=true)", path)
		require.Truef(t, needs2, "needsScan(%q, followTrailing=true)", path)
	}

	for _, path := range []string{
		// Paths within the scanned /aa/bb/cc, even if they don't exist.
		"/aa/bb/cc", "/aa/bb/cc/non-exist", "/aa/bb/cc/dd/ee/ff", "/aa/bb/cc/non-exist/xx/yy/zz",
	} {
		needs1, err := cc.needsScan(root, path, false)
		require.NoErrorf(t, err, "needsScan(%q, followTrailing=false)", path)
		require.Falsef(t, needs1, "needsScan(%q, followTrailing=false)", path)

		needs2, err := cc.needsScan(root, path, true)
		require.NoErrorf(t, err, "needsScan(%q, followTrailing=true)", path)
		require.Falsef(t, needs2, "needsScan(%q, followTrailing=true)", path)
	}

	// /aa was not scanned, but during the walk we went through /aa/ln and so
	// we know the contents of the link. However, if we want to scan it with
	// followTrailing=true, we will need a scan because we didn't scan /aa.
	path := "/aa/ln"
	needs1, err := cc.needsScan(root, path, false)
	require.NoErrorf(t, err, "needsScan(%q, followTrailing=false)", path)
	require.Falsef(t, needs1, "needsScan(%q, followTrailing=false)", path)

	needs2, err := cc.needsScan(root, path, true)
	require.NoErrorf(t, err, "needsScan(%q, followTrailing=true)", path)
	require.Truef(t, needs2, "needsScan(%q, followTrailing=true)", path)
}

// https://github.com/dagger/dagger/buildkit/issues/5042
func TestNeedScanChecksumRegression(t *testing.T) {
	// This test cannot be run in parallel because we use scanCounter.
	scanCounterEnable = true
	defer func() {
		scanCounterEnable = false
	}()

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD aa dir",
		"ADD aa/bb dir",
		"ADD aa/bb/cc file data0",
		"ADD aa/ln symlink /aa",
		"ADD aa/root symlink /",
		"ADD bb symlink aa/bb",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	// Checksumming /aa/bb while following links will result in /aa being scanned.
	_, err = cc.Checksum(context.TODO(), ref, "/bb", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	root := cc.tree.Root()
	for _, test := range []struct {
		path                            string
		followTrailing, expectNeedsScan bool
	}{
		// Any path under /aa will not result in a re-scan.
		{"/aa", true, false},
		{"/aa/ln", true, false},
		{"/aa/ln", false, false},
		{"/aa/non-exist", true, false},
		{"/aa/bb/non-exist", true, false},
		{"/aa/bb/cc", true, false},
		{"/aa/bb/cc/non-exist", true, false},
		// followTrailing=false on a symlink to /.
		{"/aa/root", false, false},
		// /bb itself was scanned during the lookup in Checksum.
		{"/bb", true, false},
		{"/bb", false, false},
		// A path outside /aa will need a scan.
		{"/non-exist", true, true},
		{"/non-exist", false, true},
		{"/aa/root", true, true},
		{"/", true, true},
	} {
		needs, err := cc.needsScan(root, test.path, test.followTrailing)
		require.NoErrorf(t, err, "needsScan(%q, followTrailing=%v)", test.path, test.followTrailing)
		require.Equalf(t, test.expectNeedsScan, needs, "needsScan(%q, followTrailing=%v)", test.path, test.followTrailing)
	}

	// Make sure trying to checksum a subpath results in no further scans.
	initialScanCounter := scanCounter.Load()
	_, err = cc.Checksum(context.TODO(), ref, "/bb/cc", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, initialScanCounter, scanCounter.Load())
	_, err = cc.Checksum(context.TODO(), ref, "/bb/non-existent", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	require.Equal(t, initialScanCounter, scanCounter.Load())

	// Looking up a non-existent path in / will checksum the whole tree. See
	// <https://github.com/dagger/dagger/buildkit/issues/5042> for more information.
	// This means that needsScan will return true for any path.
	_, err = cc.Checksum(context.TODO(), ref, "/non-existent", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	fullScanCounter := scanCounter.Load()
	require.NotEqual(t, fullScanCounter, initialScanCounter)

	root = cc.tree.Root()
	for _, path := range []string{
		"/", "/non-exist", "/ff", "/aa/root", "/non-exist/child", "/different-non-exist",
	} {
		needs1, err := cc.needsScan(root, path, false)
		require.NoErrorf(t, err, "needsScan(%q, followTrailing=false)", path)
		require.Falsef(t, needs1, "needsScan(%q, followTrailing=false)", path)

		needs2, err := cc.needsScan(root, path, true)
		require.NoErrorf(t, err, "needsScan(%q, followTrailing=true)", path)
		require.Falsef(t, needs2, "needsScan(%q, followTrailing=true)", path)
	}

	// Looking up any more paths should not result in any more scans because we
	// already know / was scanned.
	_, err = cc.Checksum(context.TODO(), ref, "/non-existent", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	require.Equal(t, fullScanCounter, scanCounter.Load())
	_, err = cc.Checksum(context.TODO(), ref, "/different/non/existent", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	require.Equal(t, fullScanCounter, scanCounter.Load())
	_, err = cc.Checksum(context.TODO(), ref, "/aa/root/aa/non-exist", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	require.Equal(t, fullScanCounter, scanCounter.Load())
	_, err = cc.Checksum(context.TODO(), ref, "/aa/root/bb/cc", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, fullScanCounter, scanCounter.Load())
}

func TestChecksumNonLexicalSymlinks(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD target dir",
		"ADD target/file file data0",
		"ADD link1 dir",
		"ADD link1/target_file symlink ../target/file",
		"ADD link1/target_file_abs symlink /target/file",
		"ADD link1/target_dir symlink ../target",
		"ADD link1/target_dir_abs symlink /target",
		"ADD link2 dir",
		"ADD link2/link1_rel symlink ../link1",
		"ADD link2/link1_abs symlink /link1",
		"ADD link3 dir",
		"ADD link3/target symlink ../link2/link1_rel/target_dir",
		"ADD link3/target_file symlink ../link2/link1_rel/target_file",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	// When following links, all of these paths should be resolved identically.
	for _, path := range []string{
		"target/file",
		"link1/target_file",
		"link1/target_dir/file",
		"link2/link1_rel/target_file",
		"link2/link1_rel/target_file_abs",
		"link2/link1_rel/target_dir/file",
		"link2/link1_rel/target_dir_abs/file",
		"link2/link1_abs/target_file",
		"link2/link1_abs/target_file_abs",
		"link2/link1_abs/target_dir/file",
		"link2/link1_abs/target_dir_abs/file",
		"link3/target_file",
		"link3/target/file",
	} {
		dgst, err := cc.Checksum(context.TODO(), ref, path, ChecksumOpts{FollowLinks: true}, nil)
		require.NoErrorf(t, err, "Checksum(%q)", path)
		require.Equalf(t, dgstFileData0, dgst, "Checksum(%q)", path)
	}

	// FollowLinks only affects final component resolution, so make sure that
	// the resolution still works with symlink path components.
	for _, path := range []string{
		"target/file",
		"link1/target_dir/file",
		"link2/link1_rel/target_dir/file",
		"link2/link1_rel/target_dir_abs/file",
		"link2/link1_abs/target_dir/file",
		"link2/link1_abs/target_dir_abs/file",
		"link3/target/file",
	} {
		dgst, err := cc.Checksum(context.TODO(), ref, path, ChecksumOpts{FollowLinks: false}, nil)
		require.NoErrorf(t, err, "Checksum(%q)", path)
		require.Equalf(t, dgstFileData0, dgst, "Checksum(%q)", path)
	}

	dgstLink1TargetFile, err := cc.Checksum(context.TODO(), ref, "link1/target_file", ChecksumOpts{FollowLinks: false}, nil)
	require.NoError(t, err)
	require.NotEqual(t, dgstFileData0, dgstLink1TargetFile)

	dgstLink1TargetFileAbs, err := cc.Checksum(context.TODO(), ref, "link1/target_file_abs", ChecksumOpts{FollowLinks: false}, nil)
	require.NoError(t, err)
	require.NotEqual(t, dgstFileData0, dgstLink1TargetFileAbs)

	dgstLink3TargetFile, err := cc.Checksum(context.TODO(), ref, "link3/target_file", ChecksumOpts{FollowLinks: false}, nil)
	require.NoError(t, err)
	require.NotEqual(t, dgstFileData0, dgstLink3TargetFile)

	// For the final component, we should get the digest of the expected links.
	for _, test := range []struct {
		path         string
		expectedDgst digest.Digest
	}{
		{"link1/target_file", dgstLink1TargetFile},
		{"link2/link1_rel/target_file", dgstLink1TargetFile},
		{"link2/link1_rel/target_file_abs", dgstLink1TargetFileAbs},
		{"link2/link1_abs/target_file", dgstLink1TargetFile},
		{"link2/link1_abs/target_file_abs", dgstLink1TargetFileAbs},
		{"link3/target_file", dgstLink3TargetFile},
	} {
		dgst, err := cc.Checksum(context.TODO(), ref, test.path, ChecksumOpts{FollowLinks: false}, nil)
		require.NoErrorf(t, err, "Checksum(%q)", test.path)
		require.NotEqualf(t, dgstFileData0, dgst, "Checksum(%q)", test.path)
		require.Equalf(t, test.expectedDgst, dgst, "Checksum(%q)", test.path)
	}
}

func TestChecksumHardlinks(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD abc dir",
		"ADD abc/foo file data0",
		"ADD ln file >/abc/foo",
		"ADD ln2 file >/abc/foo",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "abc/foo", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "ln", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "ln2", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	// validate same results with handleChange
	ref2 := createRef(t, cm, nil)

	cc2, err := newCacheContext(ref2)
	require.NoError(t, err)

	err = emit(cc2.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgst, err = cc2.Checksum(context.TODO(), ref, "abc/foo", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = cc2.Checksum(context.TODO(), ref, "ln", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = cc2.Checksum(context.TODO(), ref, "ln2", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	// modify two of the links

	ch = []string{
		"ADD abc/foo file data1",
		"ADD ln file >/abc/foo",
	}

	cc2.linkMap = map[string][][]byte{}

	err = emit(cc2.HandleChange, changeStream(ch))
	require.NoError(t, err)

	data1Expected := "sha256:c2b5e234f5f38fc5864da7def04782f82501a40d46192e4207d5b3f0c3c4732b"

	dgst, err = cc2.Checksum(context.TODO(), ref, "abc/foo", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, data1Expected, string(dgst))

	dgst, err = cc2.Checksum(context.TODO(), ref, "ln", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, data1Expected, string(dgst))

	dgst, err = cc2.Checksum(context.TODO(), ref, "ln2", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)
}

func TestChecksumWildcardOrFilter(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD bar file data1",
		"ADD foo file data0",
		"ADD fox file data2",
		"ADD x dir",
		"ADD x/d0 dir",
		"ADD x/d0/abc file data0",
		"ADD x/d0/def symlink abc",
		"ADD x/d0/ghi symlink nosuchfile",
		"ADD y1 symlink foo",
		"ADD y2 symlink fox",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "f*o", ChecksumOpts{Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, digest.FromBytes(append([]byte("foo"), []byte(dgstFileData0)...)), dgst)

	expFoos := digest.Digest("sha256:7f51c821895cfc116d3f64231dfb438e87a237ecbbe027cd96b7ee5e763cc569")

	dgst, err = cc.Checksum(context.TODO(), ref, "f*", ChecksumOpts{Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, expFoos, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "x/d?", ChecksumOpts{Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDirD0FileByFile, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "x/d?/def", ChecksumOpts{FollowLinks: true, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	expFoos2 := digest.Digest("sha256:8afc09c7018d65d5eb318a9ef55cb704dec1f06d288181d913fc27a571aa042d")

	dgst, err = cc.Checksum(context.TODO(), ref, "y*", ChecksumOpts{FollowLinks: true, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, expFoos2, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestChecksumWildcardWithBadMountable(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ref := createRef(t, cm, nil)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	_, err = cc.Checksum(context.TODO(), newBadMountable(), "*", ChecksumOpts{Wildcard: true}, nil)
	require.Error(t, err)
}

func TestSymlinksNoFollow(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD target file data0",
		"ADD sym symlink target",
		"ADD sym2 symlink target2",
		"ADD foo dir",
		"ADD foo/ghi symlink target",
		"ADD y1 symlink foo/ghi",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	expectedSym := digest.Digest("sha256:a2ba571981f48ec34eb79c9a3ab091b6491e825c2f7e9914ea86e8e958be7fae")

	dgst, err := cc.Checksum(context.TODO(), ref, "sym", ChecksumOpts{Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, expectedSym, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "sym2", ChecksumOpts{Wildcard: true}, nil)
	require.NoError(t, err)
	require.NotEqual(t, expectedSym, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "foo/ghi", ChecksumOpts{Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, expectedSym, dgst)

	_, err = cc.Checksum(context.TODO(), ref, "foo/ghi", ChecksumOpts{FollowLinks: true, Wildcard: true}, nil) // same because broken symlink
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	_, err = cc.Checksum(context.TODO(), ref, "y1", ChecksumOpts{FollowLinks: true, Wildcard: true}, nil)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	dgst, err = cc.Checksum(context.TODO(), ref, "sym", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, expectedSym, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "foo/ghi", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, expectedSym, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestChecksumBasicFile(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref := createRef(t, cm, ch)

	// for the digest values, the actual values are not important in development
	// phase but consistency is

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	_, err = cc.Checksum(context.TODO(), ref, "nosuch", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstFileData0, dgst)

	// second file returns different hash
	dgst, err = cc.Checksum(context.TODO(), ref, "bar", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, digest.Digest("sha256:c2b5e234f5f38fc5864da7def04782f82501a40d46192e4207d5b3f0c3c4732b"), dgst)

	// same file inside a directory
	dgst, err = cc.Checksum(context.TODO(), ref, "d0/abc", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstFileData0, dgst)

	// repeat because codepath is different
	dgst, err = cc.Checksum(context.TODO(), ref, "d0/abc", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstFileData0, dgst)

	// symlink to the same file is followed, returns same hash
	dgst, err = cc.Checksum(context.TODO(), ref, "d0/def", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstFileData0, dgst)

	_, err = cc.Checksum(context.TODO(), ref, "d0/ghi", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	dgst, err = cc.Checksum(context.TODO(), ref, "/", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, digest.Digest("sha256:427c9cf9ae98c0f81fb57a3076b965c7c149b6b0a85625ad4e884236649a42c6"), dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "d0", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	// this is same directory as previous d0
	ch = []string{
		"ADD abc file data0",
		"ADD def symlink abc",
		"ADD ghi symlink nosuchfile",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref)
	require.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "/", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	// test that removing broken symlink changes hash even though symlink itself can't be checksummed
	ch = []string{
		"ADD abc file data0",
		"ADD def symlink abc",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref)
	require.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "/", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstDirD0Modified, dgst)
	require.NotEqual(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	// test multiple scans, get checksum of nested file first

	ch = []string{
		"ADD abc dir",
		"ADD abc/aa dir",
		"ADD abc/aa/foo file data2",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref)
	require.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "abc/aa/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, digest.Digest("sha256:1c67653c3cf95b12a0014e2c4cd1d776b474b3218aee54155d6ae27b9b999c54"), dgst)
	require.NotEqual(t, dgstDirD0, dgst)

	// this will force rescan
	dgst, err = cc.Checksum(context.TODO(), ref, "d0", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestChecksumIncludeExclude(t *testing.T) {
	t.Parallel()

	t.Run("wildcard_false", func(t *testing.T) { testChecksumIncludeExclude(t, false) })
	t.Run("wildcard_true", func(t *testing.T) { testChecksumIncludeExclude(t, true) })
}

func testChecksumIncludeExclude(t *testing.T, wildcard bool) {
	t.Parallel()

	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD d0 dir",
		"ADD d0/abc file abc",
		"ADD d1 dir",
		"ADD d1/def file def",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	opts := func(opts ChecksumOpts) ChecksumOpts {
		opts.Wildcard = wildcard
		return opts
	}

	dgstFoo, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"foo"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstFoo)

	dgstFooBar, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"foo", "bar"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstFooBar)

	require.NotEqual(t, dgstFoo, dgstFooBar)

	dgstD0, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d0"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD0)
	dgstD1, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d1"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD1)

	dgstD0Star, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d0/*"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD0Star)
	dgstD0AStar, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d0/a*"}}), nil)
	require.NoError(t, err)
	require.Equal(t, dgstD0Star, dgstD0AStar)
	dgstD1Star, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d1/*"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD1Star)

	// Nothing matches pattern, but d2's metadata should be captured in the
	// checksum if d2 exists
	dgstD2Foo, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d2/foo"}}), nil)
	require.NoError(t, err)
	require.Equal(t, digest.FromBytes([]byte{}), dgstD2Foo)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	// add some files
	ch = []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD baz file data2",
		"ADD d0 dir",
		"ADD d0/abc file abc",
		"ADD d0/xyz file xyz",
		"ADD d1 dir",
		"ADD d1/def file def",
		"ADD d2 dir",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref)
	require.NoError(t, err)

	dgstFoo2, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"foo"}}), nil)
	require.NoError(t, err)
	dgstFooBar2, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"foo", "bar"}}), nil)
	require.NoError(t, err)

	require.Equal(t, dgstFoo, dgstFoo2)
	require.Equal(t, dgstFooBar, dgstFooBar2)

	dgstD02, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d0"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, dgstD0, dgstD02)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD02)

	dgstD12, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d1"}}), nil)
	require.NoError(t, err)
	require.Equal(t, dgstD1, dgstD12)

	dgstD0Star2, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d0/*"}}), nil)
	require.NoError(t, err)
	require.NotEqual(t, dgstD0Star, dgstD0Star2)

	dgstD0AStar2, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d0/a*"}}), nil)
	require.NoError(t, err)
	// new file does not match the include pattern, so the digest should stay the same
	require.Equal(t, dgstD0AStar, dgstD0AStar2)
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD0AStar2)

	dgstStarStarABC, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"**/abc"}}), nil)
	require.NoError(t, err)
	require.Equal(t, dgstD0AStar, dgstStarStarABC)

	dgstD1Star2, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d1/*"}}), nil)
	require.NoError(t, err)
	require.Equal(t, dgstD1Star, dgstD1Star2)

	dgstD0StarExclude, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d0/*"}, ExcludePatterns: []string{"d0/xyz"}}), nil)
	require.NoError(t, err)
	require.Equal(t, dgstD0Star, dgstD0StarExclude)

	dgstD2Foo2, err := cc.Checksum(context.TODO(), ref, "", opts(ChecksumOpts{IncludePatterns: []string{"d2/foo"}}), nil)
	require.NoError(t, err)
	require.Equal(t, dgstD2Foo, dgstD2Foo2)

	dgstD2Foo3, err := cc.Checksum(context.TODO(), ref, "d2", opts(ChecksumOpts{IncludePatterns: []string{"foo"}}), nil)
	require.NoError(t, err)
	require.Equal(t, dgstD2Foo, dgstD2Foo3)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestChecksumIncludeDoubleStar(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD prefix dir",
		"ADD prefix/a dir",
		"ADD prefix/a/b dir",
		"ADD prefix/a/b/c dir",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "prefix/a", ChecksumOpts{IncludePatterns: []string{"**/foo/**"}}, nil)
	require.NoError(t, err)
	// Nothing included
	require.Equal(t, digest.FromBytes([]byte{}), dgst)

	// Same, with Wildcard = true
	dgst, err = cc.Checksum(context.TODO(), ref, "prefix/a", ChecksumOpts{IncludePatterns: []string{"**/foo/**"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, digest.FromBytes([]byte{}), dgst)

	ch = []string{
		"ADD prefix dir",
		"ADD prefix/a dir",
		"ADD prefix/a/b dir",
		"ADD prefix/a/b/c dir",
		"ADD prefix/a/b/c/foo dir",
		"ADD prefix/a/b/c/foo/bar file abc",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref)
	require.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "prefix/a", ChecksumOpts{IncludePatterns: []string{"**/foo/**", "**/report"}}, nil)
	require.NoError(t, err)
	// Now there is a file included
	require.Equal(t, dgstDoubleStar, dgst)

	// Same, with Wildcard = true
	dgst, err = cc.Checksum(context.TODO(), ref, "prefix/a", ChecksumOpts{IncludePatterns: []string{"**/foo/**", "**/report"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDoubleStar, dgst)

	// **/... pattern (https://github.com/moby/moby/issues/41433)
	dgst, err = cc.Checksum(context.TODO(), ref, "prefix/a", ChecksumOpts{IncludePatterns: []string{"**/foo", "**/report"}}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDoubleStar, dgst)

	// Same, with Wildcard = true
	dgst, err = cc.Checksum(context.TODO(), ref, "prefix/a", ChecksumOpts{IncludePatterns: []string{"**/foo", "**/report"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDoubleStar, dgst)
}

func TestChecksumIncludeSymlink(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD data dir",
		"ADD data/d0 dir",
		"ADD data/d0/d1 dir",
		"ADD data/d0/d1/d2 dir",
		"ADD mnt dir",
		"ADD mnt/data symlink ../data",
		"ADD data/d0/d1/d2/foo file abc",
		"ADD data/symlink-to-d0 symlink d0",
	}

	ref := createRef(t, cm, ch)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	dgstD0, err := cc.Checksum(context.TODO(), ref, "data/d0", ChecksumOpts{IncludePatterns: []string{"**/foo"}}, nil)
	require.NoError(t, err)
	// File should be included
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD0)

	dgstMntD0, err := cc.Checksum(context.TODO(), ref, "mnt/data/d0", ChecksumOpts{IncludePatterns: []string{"**/foo"}}, nil)
	require.NoError(t, err)
	// File should be included despite symlink
	require.Equal(t, dgstD0, dgstMntD0)

	dgstD2, err := cc.Checksum(context.TODO(), ref, "data/d0/d1/d2", ChecksumOpts{IncludePatterns: []string{"**/foo"}}, nil)
	require.NoError(t, err)
	// File should be included
	require.NotEqual(t, digest.FromBytes([]byte{}), dgstD2)

	dgstMntD2, err := cc.Checksum(context.TODO(), ref, "mnt/data/d0/d1/d2", ChecksumOpts{IncludePatterns: []string{"**/foo"}}, nil)
	require.NoError(t, err)
	// File should be included despite symlink
	require.Equal(t, dgstD2, dgstMntD2)

	// Same, with Wildcard = true
	dgstMntD0Wildcard, err := cc.Checksum(context.TODO(), ref, "mnt/data/d0", ChecksumOpts{IncludePatterns: []string{"**/foo"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstD0, dgstMntD0Wildcard)

	dgstMntD0Wildcard2, err := cc.Checksum(context.TODO(), ref, "mnt/data/d*", ChecksumOpts{IncludePatterns: []string{"**/foo"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstD0, dgstMntD0Wildcard2)

	dgstMntD2Wildcard, err := cc.Checksum(context.TODO(), ref, "mnt/data/d0/d1/d2", ChecksumOpts{IncludePatterns: []string{"**/foo"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstD2, dgstMntD2Wildcard)

	dgstMntD2Wildcard2, err := cc.Checksum(context.TODO(), ref, "mnt/data/d0/d1/d*", ChecksumOpts{IncludePatterns: []string{"**/foo"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstD2, dgstMntD2Wildcard2)

	dgstMntInnerWildcard, err := cc.Checksum(context.TODO(), ref, "mnt/data/d0/d*/d2", ChecksumOpts{IncludePatterns: []string{"**/foo"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstD2, dgstMntInnerWildcard)

	dgstMntInnerWildcard2, err := cc.Checksum(context.TODO(), ref, "mnt/data/symlink-to-d0/d*/d2", ChecksumOpts{IncludePatterns: []string{"**/foo"}, Wildcard: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstD2, dgstMntInnerWildcard2)
}

func TestHandleChange(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref := createRef(t, cm, nil)

	// for the digest values, the actual values are not important in development
	// phase but consistency is

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgstFoo, err := cc.Checksum(context.TODO(), ref, "foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstFileData0, dgstFoo)

	// symlink to the same file is followed, returns same hash
	dgst, err := cc.Checksum(context.TODO(), ref, "d0/def", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstFoo, dgst)

	// symlink to the same file is followed, returns same hash
	dgst, err = cc.Checksum(context.TODO(), ref, "d0", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, dgstDirD0, dgst)

	ch = []string{
		"DEL d0/ghi file",
	}

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "d0", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDirD0Modified, dgst)

	ch = []string{
		"DEL d0 dir",
	}

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	_, err = cc.Checksum(context.TODO(), ref, "d0", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	_, err = cc.Checksum(context.TODO(), ref, "d0/abc", ChecksumOpts{FollowLinks: true}, nil)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, errNotFound))

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestHandleRecursiveDir(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD d0 dir",
		"ADD d0/foo dir",
		"ADD d0/foo/bar dir",
		"ADD d0/foo/bar/foo file data0",
		"ADD d0/foo/bar/bar file data1",
		"ADD d1 dir",
		"ADD d1/foo file data0",
	}

	ref := createRef(t, cm, nil)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "d0/foo/bar", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	ch = []string{
		"DEL d0 dir",
		"DEL d0/foo dir", // the differ can produce a record for subdir as well
		"ADD d1/bar file data1",
	}

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgst2, err := cc.Checksum(context.TODO(), ref, "d1", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgst2, dgst)

	_, err = cc.Checksum(context.TODO(), ref, "", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
}

func TestChecksumUnorderedFiles(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD d0 dir",
		"ADD d0/foo dir",
		"ADD d0/foo/bar file data0",
		"ADD d0/foo-subdir dir",
		"ADD d0/foo.subdir file data1",
	}

	ref := createRef(t, cm, nil)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "d0", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.Equal(t, digest.Digest("sha256:14276c302c940a80f82ca5477bf766c98a24702d6a9948ee71bb277cdad3ae05"), dgst)

	// check regression from earier version that didn't track some files
	ch = []string{
		"ADD d0 dir",
		"ADD d0/foo dir",
		"ADD d0/foo/bar file data0",
	}

	ref = createRef(t, cm, nil)

	cc, err = newCacheContext(ref)
	require.NoError(t, err)

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgst2, err := cc.Checksum(context.TODO(), ref, "d0", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	require.NotEqual(t, dgst, dgst2)
}

func TestSymlinkInPathScan(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD d0 dir",
		"ADD d0/sub dir",
		"ADD d0/sub/foo file data0",
		"ADD d0/def symlink sub",
	}
	ref := createRef(t, cm, ch)

	dgst, err := Checksum(context.TODO(), ref, "d0/def/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = Checksum(context.TODO(), ref, "d0/def/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestSymlinkNeedsScan(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD c0 dir",
		"ADD c0/sub dir",
		"ADD c0/sub/foo file data0",
		"ADD d0 dir",
		"ADD d0/d1 dir",
		"ADD d0/d1/def symlink ../../c0/sub",
	}
	ref := createRef(t, cm, ch)

	// scan the d0 path containing the symlink that doesn't get followed
	_, err = Checksum(context.TODO(), ref, "d0/d1", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)

	dgst, err := Checksum(context.TODO(), ref, "d0/d1/def/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestSymlinkAbsDirSuffix(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD c0 dir",
		"ADD c0/sub dir",
		"ADD c0/sub/foo file data0",
		"ADD link symlink /c0/sub/",
	}
	ref := createRef(t, cm, ch)

	dgst, err := Checksum(context.TODO(), ref, "link/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestSymlinkThroughParent(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD lib dir",
		"ADD lib/sub dir",
		"ADD lib/sub/foo file data0",
		"ADD lib/sub/link symlink ../../lib2",
		"ADD lib2 dir",
		"ADD lib2/sub dir",
		"ADD lib2/sub/foo file data0",
		"ADD link1 symlink /lib",
		"ADD link2 symlink /lib/",
		"ADD link3 symlink /lib/.",
		"ADD link4 symlink /lib/../lib",
		"ADD link5 symlink ../lib",
	}
	ref := createRef(t, cm, ch)

	dgst, err := Checksum(context.TODO(), ref, "link1/sub/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = Checksum(context.TODO(), ref, "link2/sub/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = Checksum(context.TODO(), ref, "link3/sub/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = Checksum(context.TODO(), ref, "link4/sub/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = Checksum(context.TODO(), ref, "link5/sub/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = Checksum(context.TODO(), ref, "link1/sub/link/sub/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestSymlinkInPathHandleChange(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD d1 dir",
		"ADD d1/sub dir",
		"ADD d1/sub/foo file data0",
		"ADD d1/sub/bar symlink /link",
		"ADD d1/sub/baz symlink ../../../link",
		"ADD d1/sub/bay symlink ../../../../link/.", // weird link
		"ADD d1/def symlink sub",
		"ADD sub dir",
		"ADD sub/d0 dir",
		"ADD sub/d0/abc file data0",
		"ADD sub/d0/def symlink abc",
		"ADD sub/d0/ghi symlink nosuchfile",
		"ADD link symlink sub/d0",
	}

	ref := createRef(t, cm, nil)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "d1/def/foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "d1/def/bar/abc", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	dgstFileData0, err := cc.Checksum(context.TODO(), ref, "sub/d0", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDirD0, dgstFileData0)

	dgstFileData0, err = cc.Checksum(context.TODO(), ref, "d1/def/baz", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDirD0, dgstFileData0)

	dgstFileData0, err = cc.Checksum(context.TODO(), ref, "d1/def/bay", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDirD0, dgstFileData0)

	dgstFileData0, err = cc.Checksum(context.TODO(), ref, "link", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstDirD0, dgstFileData0)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestPersistence(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref := createRef(t, cm, ch)
	id := ref.ID()

	dgst, err := Checksum(context.TODO(), ref, "foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	ref, err = cm.Get(context.TODO(), id, nil)
	require.NoError(t, err)

	dgst, err = Checksum(context.TODO(), ref, "foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond) // saving happens on the background

	// we can't close snapshotter and open it twice (especially, its internal bbolt store)
	cleanup()
	getDefaultManager().lru.Purge()
	cm, cleanup = setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ref, err = cm.Get(context.TODO(), id, nil)
	require.NoError(t, err)

	dgst, err = Checksum(context.TODO(), ref, "foo", ChecksumOpts{FollowLinks: true}, nil)
	require.NoError(t, err)
	require.Equal(t, dgstFileData0, dgst)
}

func TestChecksumUpdateDirectory(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, snapshotter.Close())
	})

	cm, cleanup := setupCacheManager(t, tmpdir, "native", snapshotter)
	t.Cleanup(cleanup)

	ch := []string{
		"ADD d0 dir",
		"ADD d0/foo dir",
		"ADD d0/foo/bar file data0",
		"ADD d0/foo/subdir1 dir",
		"ADD d0/foo/subdir1/baz file data1",
		"ADD d0/foo/subdir2 dir",
	}

	ref := createRef(t, cm, nil)

	cc, err := newCacheContext(ref)
	require.NoError(t, err)

	err = emit(cc.HandleChange, changeStream(ch))
	require.NoError(t, err)

	fooDgst1, err := cc.Checksum(context.TODO(), ref, "d0/foo", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, digest.Digest("sha256:e76717544f71725bd759a981554ca17c286b3d222598f46a671b983fd2b8172d"), fooDgst1)

	barDgst1, err := cc.Checksum(context.TODO(), ref, "d0/foo/bar", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, digest.Digest("sha256:cd8e75bca50f2d695f220d0cb0997d8ead387e4f926e8669a92d7f104cc9885b"), barDgst1)

	// change d0/foo's permissions
	updateFooCh := parseChange("CHG d0/foo dir")
	fi, ok := updateFooCh.fi.(*fsutil.StatInfo)
	require.True(t, ok)
	prevMode := fi.Stat.Mode
	fi.Stat.Mode = uint32(os.ModeDir) | 0700
	require.NotEqual(t, prevMode, fi.Stat.Mode) // sanity check we actually changed something

	err = emit(cc.HandleChange, []*change{updateFooCh})
	require.NoError(t, err)

	// d0/foo should have a different digest now
	fooDgst2, err := cc.Checksum(context.TODO(), ref, "d0/foo", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.NotEqual(t, fooDgst1, fooDgst2)
	require.Equal(t, digest.Digest("sha256:3a729f6ba0d3d74c6ade7d118b08b46e37e447afdad7fc6e258dbba12fa80141"), fooDgst2)

	// but files under the dir should be the same as before
	barDgst2, err := cc.Checksum(context.TODO(), ref, "d0/foo/bar", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.Equal(t, barDgst1, barDgst2)

	// replace d0/foo with a file
	err = emit(cc.HandleChange, changeStream([]string{
		"CHG d0/foo file data2",
	}))
	require.NoError(t, err)

	// d0/foo should again have a different digest now
	fooDgst3, err := cc.Checksum(context.TODO(), ref, "d0/foo", ChecksumOpts{}, nil)
	require.NoError(t, err)
	require.NotEqual(t, fooDgst1, fooDgst3)
	require.NotEqual(t, fooDgst2, fooDgst3)
	require.Equal(t, digest.Digest("sha256:1c67653c3cf95b12a0014e2c4cd1d776b474b3218aee54155d6ae27b9b999c54"), fooDgst3)

	// files under the old dir should not exist anymore
	_, err = cc.Checksum(context.TODO(), ref, "d0/foo/bar", ChecksumOpts{}, nil)
	require.ErrorContains(t, err, "not found")
}

func createRef(t *testing.T, cm cache.Manager, files []string) cache.ImmutableRef {
	if runtime.GOOS == "windows" && len(files) > 0 {
		// lm.Mount() will fail
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	mref, err := cm.New(context.TODO(), nil, nil, cache.CachePolicyRetain)
	require.NoError(t, err)

	mounts, err := mref.Mount(context.TODO(), false, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mounts)

	mp, err := lm.Mount()
	require.NoError(t, err)

	err = writeChanges(mp, changeStream(files))
	lm.Unmount()
	require.NoError(t, err)

	ref, err := mref.Commit(context.TODO())
	require.NoError(t, err)

	return ref
}

func setupCacheManager(t *testing.T, tmpdir string, snapshotterName string, snapshotter snapshots.Snapshotter) (cache.Manager, func()) {
	store, err := local.NewStore(tmpdir)
	require.NoError(t, err)

	db, err := bolt.Open(filepath.Join(tmpdir, "containerdmeta.db"), 0644, nil)
	require.NoError(t, err)

	mdb := ctdmetadata.NewDB(db, store, map[string]snapshots.Snapshotter{
		snapshotterName: snapshotter,
	})

	md, err := metadata.NewStore(filepath.Join(tmpdir, "metadata.db"))
	require.NoError(t, err)
	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), "buildkit")
	c := mdb.ContentStore()
	applier := winlayers.NewFileSystemApplierWithWindows(c, apply.NewFileSystemApplier(c))
	differ := winlayers.NewWalkingDiffWithWindows(c, walking.NewWalkingDiff(c))

	cm, err := cache.NewManager(cache.ManagerOpt{
		Snapshotter:    snapshot.FromContainerdSnapshotter(snapshotterName, containerdsnapshot.NSSnapshotter("buildkit", mdb.Snapshotter(snapshotterName)), nil),
		MetadataStore:  md,
		LeaseManager:   lm,
		ContentStore:   c,
		Applier:        applier,
		Differ:         differ,
		GarbageCollect: mdb.GarbageCollect,
		MountPoolRoot:  filepath.Join(tmpdir, "cachemounts"),
	})
	require.NoError(t, err)

	return cm, func() {
		db.Close()
		md.Close()
		cm.Close()
	}
}

type badMountable struct{}

func (bm *badMountable) Mount(ctx context.Context, readonly bool, _ session.Group) (snapshot.Mountable, error) {
	return nil, errors.New("tried to mount bad mountable")
}

// newBadMountable returns a cache.Mountable that will fail to mount, for use in APIs
// that require a Mountable, but which should never actually try to access the filesystem.
func newBadMountable() cache.Mountable {
	return &badMountable{}
}

// these test helpers are from tonistiigi/fsutil

type change struct {
	kind fsutil.ChangeKind
	path string
	fi   os.FileInfo
	data string
}

func changeStream(dt []string) (changes []*change) {
	for _, s := range dt {
		changes = append(changes, parseChange(s))
	}
	return
}

func parseChange(str string) *change {
	f := strings.Fields(str)
	errStr := fmt.Sprintf("invalid change %q", str)
	if len(f) < 3 {
		panic(errStr)
	}
	c := &change{}
	switch f[0] {
	case "ADD":
		c.kind = fsutil.ChangeKindAdd
	case "CHG":
		c.kind = fsutil.ChangeKindModify
	case "DEL":
		c.kind = fsutil.ChangeKindDelete
	default:
		panic(errStr)
	}
	c.path = f[1]
	st := &fstypes.Stat{}
	switch f[2] {
	case "file":
		if len(f) > 3 {
			if f[3][0] == '>' {
				st.Linkname = f[3][1:]
			} else {
				c.data = f[3]
				st.Size_ = int64(len(f[3]))
			}
		}
		st.Mode |= 0644
	case "dir":
		st.Mode |= uint32(os.ModeDir)
		st.Mode |= 0755
	case "symlink":
		if len(f) < 4 {
			panic(errStr)
		}
		st.Mode |= uint32(os.ModeSymlink)
		st.Linkname = f[3]
		st.Mode |= 0777
	}

	c.fi = &fsutil.StatInfo{Stat: st}
	return c
}

func emit(fn fsutil.HandleChangeFn, inp []*change) error {
	for _, c := range inp {
		stat, ok := c.fi.Sys().(*fstypes.Stat)
		if !ok {
			return errors.Errorf("invalid non-stat change %s", c.fi.Name())
		}
		fi := c.fi
		if c.kind != fsutil.ChangeKindDelete {
			h, err := NewFromStat(stat)
			if err != nil {
				return err
			}
			if _, err := io.Copy(h, strings.NewReader(c.data)); err != nil {
				return err
			}
			fi = &withHash{FileInfo: c.fi, digest: digest.NewDigest(digest.SHA256, h)}
		}
		if err := fn(c.kind, c.path, fi, nil); err != nil {
			return err
		}
	}
	return nil
}

type withHash struct {
	digest digest.Digest
	os.FileInfo
}

func (wh *withHash) Digest() digest.Digest {
	return wh.digest
}

func writeChanges(root string, inp []*change) error {
	for _, c := range inp {
		if c.kind == fsutil.ChangeKindAdd {
			p := filepath.Join(root, c.path)
			stat, ok := c.fi.Sys().(*fstypes.Stat)
			if !ok {
				return errors.Errorf("invalid non-stat change %s", p)
			}
			if c.fi.IsDir() {
				// The snapshot root ('/') is always created with 0755.
				// We use the same permission mode here.
				if err := os.Mkdir(p, 0755); err != nil {
					return errors.WithStack(err)
				}
			} else if c.fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Symlink(stat.Linkname, p); err != nil {
					return errors.WithStack(err)
				}
			} else if len(stat.Linkname) > 0 {
				link := filepath.Join(root, stat.Linkname)
				if !filepath.IsAbs(link) {
					link = filepath.Join(filepath.Dir(p), stat.Linkname)
				}
				if err := os.Link(link, p); err != nil {
					return errors.WithStack(err)
				}
			} else {
				f, err := os.Create(p)
				if err != nil {
					return errors.WithStack(err)
				}
				if len(c.data) > 0 {
					if _, err := f.Write([]byte(c.data)); err != nil {
						return errors.WithStack(err)
					}
				}
				f.Close()
			}
		}
	}
	return nil
}
