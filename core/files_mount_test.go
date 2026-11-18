package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/util/layercopy"
	"github.com/stretchr/testify/require"
)

func filesMountTestLink(t *testing.T, ref bkcache.ImmutableRef) (how string, linked string, err error) {
	t.Helper()
	dst := t.TempDir()
	copier, err := layercopy.NewCopier(layercopy.Mount{Root: dst})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, copier.Close()) })
	how, err = linkMountedFile(t.Context(), copier, execMountedFile{Name: "linked", Path: "/d/f", Ref: ref})
	return how, filepath.Join(dst, "linked"), err
}

// A file is linked through a mount until its snapshot's topmost layer is known,
// then from that layer with no mount, even once no mount is left. A file the
// layer can't give goes through a mount as before. The linked file is the same
// each way.
func TestFilesMountLinkPaths(t *testing.T) {
	store := testutil.NewStore(t)
	check := func(t *testing.T, ref bkcache.ImmutableRef, wantHow string, wantMounts int64) {
		t.Helper()
		how, linked, err := filesMountTestLink(t, ref)
		require.NoError(t, err)
		require.Equal(t, wantHow, how)
		require.Equal(t, wantMounts, store.LocalMounts.Load())
		got, err := os.ReadFile(linked)
		require.NoError(t, err)
		require.Equal(t, "contents", string(got))
		info, err := os.Stat(linked)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o640), info.Mode())
	}
	release := func(t *testing.T) {
		t.Helper()
		require.NoError(t, store.Manager.(bkcache.ReadOnlyMountReleaser).ReleaseIdleSharedMounts())
	}

	plain, _ := store.BuildWith(t, nil, func(root string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "d"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "d/f"), []byte("contents"), 0o640))
	})
	check(t, plain, filesMountLayerUnknown, 1)
	check(t, plain, filesMountLinkedFromLayer, 1)
	release(t)
	check(t, plain, filesMountLinkedFromLayer, 1)

	viaSymlink, _ := store.BuildWith(t, nil, func(root string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "e"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "e/f"), []byte("contents"), 0o640))
		require.NoError(t, os.Symlink("e", filepath.Join(root, "d")))
	})
	check(t, viaSymlink, filesMountLayerUnknown, 2)
	check(t, viaSymlink, filesMountNotInLayer, 2)
	release(t)
	check(t, viaSymlink, filesMountNotInLayer, 3)
}

// A file deleted on top of a snapshot that holds it isn't linked from the
// snapshot below it, whichever way the link goes.
func TestFilesMountLinkDeletedFile(t *testing.T) {
	store := testutil.NewStore(t)
	parent, _ := store.BuildWith(t, nil, func(root string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "d"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "d/f"), []byte("contents"), 0o640))
	})
	child, _ := store.BuildWith(t, parent, func(root string) {
		require.NoError(t, os.Remove(filepath.Join(root, "d/f")))
	})
	for _, ref := range []bkcache.ImmutableRef{parent, child} {
		_, _, _ = filesMountTestLink(t, ref) // makes each layer known, if it can
	}
	how, linked, err := filesMountTestLink(t, parent)
	require.NoError(t, err)
	require.Equal(t, filesMountLinkedFromLayer, how)
	got, err := os.ReadFile(linked)
	require.NoError(t, err)
	require.Equal(t, "contents", string(got))

	how, linked, err = filesMountTestLink(t, child)
	require.Error(t, err)
	require.Equal(t, filesMountNotInLayer, how)
	_, statErr := os.Lstat(linked)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

// A linked file needs nothing of its snapshot: its bytes outlive the snapshot.
func TestFilesMountLinkOutlivesSnapshot(t *testing.T) {
	for _, known := range []bool{false, true} {
		store := testutil.NewStore(t)
		ref, owner := store.BuildWith(t, nil, func(root string) {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "d"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "d/f"), []byte("contents"), 0o640))
		})
		if known {
			_, _, err := filesMountTestLink(t, ref)
			require.NoError(t, err)
		}
		_, linked, err := filesMountTestLink(t, ref)
		require.NoError(t, err)
		require.NoError(t, store.Manager.RemoveLease(context.Background(), owner))
		require.NoError(t, store.Manager.(bkcache.ReadOnlyMountReleaser).ReleaseIdleSharedMounts())
		store.GC(t)
		_, err = store.Snapshots.Stat(context.Background(), ref.SnapshotID())
		require.Error(t, err, "the snapshot is gone")
		got, err := os.ReadFile(linked)
		require.NoError(t, err)
		require.Equal(t, "contents", string(got))
	}
}

// A snapshot removed before the link gives the same error whether or not its
// topmost layer was known.
func TestFilesMountLinkRemovedSnapshot(t *testing.T) {
	failure := func(t *testing.T, known bool) string {
		store := testutil.NewStore(t)
		ref, owner := store.BuildWith(t, nil, func(root string) {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "d"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "d/f"), []byte("contents"), 0o640))
		})
		if known {
			_, _, err := filesMountTestLink(t, ref)
			require.NoError(t, err)
		}
		require.NoError(t, store.Manager.RemoveLease(context.Background(), owner))
		require.NoError(t, store.Manager.(bkcache.ReadOnlyMountReleaser).ReleaseIdleSharedMounts())
		store.GC(t)
		_, _, err := filesMountTestLink(t, ref)
		require.Error(t, err)
		return strings.ReplaceAll(err.Error(), ref.SnapshotID(), "<snapshot>")
	}
	require.Equal(t, failure(t, false), failure(t, true))
}
