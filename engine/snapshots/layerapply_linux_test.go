package snapshots

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/continuity/sysx"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

var layerTime = time.Unix(1700000000, 123456789).UTC()

type tarEntry struct {
	hdr  tar.Header
	data string
}

func tarDir(name string, mode int64) tarEntry {
	return tarEntry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: mode}}
}

func tarFile(name, data string, mode int64) tarEntry {
	return tarEntry{hdr: tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(data))}, data: data}
}

func tarSymlink(name, target string) tarEntry {
	return tarEntry{hdr: tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target, Mode: 0777}}
}

func tarHardlink(name, target string) tarEntry {
	return tarEntry{hdr: tar.Header{Typeflag: tar.TypeLink, Name: name, Linkname: target}}
}

func tarOf(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := e.hdr
		if hdr.ModTime.IsZero() {
			hdr.ModTime = layerTime
		}
		hdr.Format = tar.FormatPAX
		require.NoError(t, tw.WriteHeader(&hdr))
		if e.data != "" {
			_, err := io.WriteString(tw, e.data)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// applyBoth extracts layer with containerd's archive.Apply into one copy of
// the starting tree and with applyLayerTar into another, and requires the same
// outcome: both fail, or both succeed with identical trees.
func applyBoth(t *testing.T, layer []byte, overlay bool, setup func(root, lower string)) (string, error) {
	t.Helper()
	ctx := context.Background()
	extract := func(dir string, apply func(root string, parents []string) error) (string, error) {
		root, lower := filepath.Join(dir, "root"), filepath.Join(dir, "lower")
		require.NoError(t, os.MkdirAll(root, 0755))
		require.NoError(t, os.MkdirAll(lower, 0755))
		if setup != nil {
			setup(root, lower)
		}
		var parents []string
		if overlay {
			parents = []string{lower}
		}
		return root, apply(root, parents)
	}
	want, wantErr := extract(t.TempDir(), func(root string, parents []string) error {
		var opts []archive.ApplyOpt
		if overlay {
			opts = append(opts, archive.WithConvertWhiteout(archive.OverlayConvertWhiteout))
			if len(parents) > 0 {
				opts = append(opts, archive.WithParents(parents))
			}
		}
		_, err := archive.Apply(ctx, root, bytes.NewReader(layer), opts...)
		return err
	})
	// Slow workers: a step that doesn't wait for them changes the outcome.
	slowWorkers(t)
	got, gotErr := extract(t.TempDir(), func(root string, parents []string) error {
		return applyLayerTar(ctx, layerTarget{root: root, parents: parents, overlay: overlay}, bytes.NewReader(layer))
	})
	if wantErr != nil {
		require.Error(t, gotErr, "containerd failed with %v", wantErr)
		return got, gotErr
	}
	require.NoError(t, gotErr)
	require.Equal(t, describeTree(t, want), describeTree(t, got))
	return got, nil
}

// slowWorkers makes each worker wait before writing a file, until the test
// ends.
func slowWorkers(t *testing.T) {
	setBeforeLayerFileWrite(t, func(string) error {
		time.Sleep(time.Millisecond)
		return nil
	})
}

func setBeforeLayerFileWrite(t *testing.T, f func(string) error) {
	beforeLayerFileWrite = f
	t.Cleanup(func() { beforeLayerFileWrite = func(string) error { return nil } })
}

// describeTree lists every entry under root with what extraction sets on it,
// and which entries share an inode.
func describeTree(t *testing.T, root string) []string {
	t.Helper()
	var lines []string
	inodes := map[uint64][]string{}
	require.NoError(t, filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		require.NoError(t, err)
		rel, err := filepath.Rel(root, path)
		require.NoError(t, err)
		st := fi.Sys().(*syscall.Stat_t)
		line := fmt.Sprintf("%s mode=%o uid=%d gid=%d", rel, st.Mode, st.Uid, st.Gid)
		// A directory made for an entry without its own takes the time
		// it was made; the times extraction sets are long past.
		if mtime := time.Unix(0, syscall.TimespecToNsec(st.Mtim)); rel != "." && time.Since(mtime) > time.Hour {
			line += fmt.Sprintf(" mtime=%d", mtime.UnixNano())
		}
		switch {
		case fi.Mode().IsRegular():
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			line += fmt.Sprintf(" data=%s", digest.FromBytes(data))
			inodes[st.Ino] = append(inodes[st.Ino], rel)
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			require.NoError(t, err)
			line += " link=" + target
			inodes[st.Ino] = append(inodes[st.Ino], rel)
		case fi.Mode()&(os.ModeDevice|os.ModeCharDevice) != 0:
			line += fmt.Sprintf(" rdev=%d", st.Rdev)
		}
		keys, err := sysx.LListxattr(path)
		if err == nil {
			sort.Strings(keys)
			for _, k := range keys {
				v, err := sysx.LGetxattr(path, k)
				require.NoError(t, err)
				line += fmt.Sprintf(" xattr:%s=%q", k, v)
			}
		}
		lines = append(lines, line)
		return nil
	}))
	for _, paths := range inodes {
		if len(paths) > 1 {
			sort.Strings(paths)
			lines = append(lines, "linked "+strings.Join(paths, " "))
		}
	}
	sort.Strings(lines)
	return lines
}

// manyFiles is more files than there are workers, in several directories,
// small, at the worker limit and above it.
func manyFiles() []tarEntry {
	var entries []tarEntry
	for d := range 4 {
		dir := fmt.Sprintf("tree/d%d", d)
		entries = append(entries, tarDir(dir, 0755))
		for f := range 20 {
			entries = append(entries, tarFile(fmt.Sprintf("%s/f%d", dir, f), strings.Repeat(fmt.Sprintf("%d-%d ", d, f), f*7+1), 0644))
		}
	}
	entries = append(entries,
		tarFile("tree/at-limit", strings.Repeat("a", layerApplyBufferedMax), 0600),
		tarFile("tree/over-limit", strings.Repeat("b", layerApplyBufferedMax+1), 0600),
		tarFile("tree/empty", "", 0600),
	)
	return entries
}

func TestLayerApplyMatchesContainerd(t *testing.T) {
	owned := tarFile("owned/file", "owner and mode", 04755)
	owned.hdr.Uid, owned.hdr.Gid = 1000, 1001
	ownedDir := tarDir("owned", 0750)
	ownedDir.hdr.Uid, ownedDir.hdr.Gid = 1002, 1003
	xattrFile := tarFile("xattrs/file", "with attributes", 0644)
	xattrFile.hdr.PAXRecords = map[string]string{"SCHILY.xattr.user.layer": "value"}
	oldTimes := tarFile("times/old", "old", 0644)
	oldTimes.hdr.ModTime = time.Unix(-5, 0)
	fifo := tarEntry{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "dev/fifo", Mode: 0640}}
	char := tarEntry{hdr: tar.Header{Typeflag: tar.TypeChar, Name: "dev/null", Mode: 0666, Devmajor: 1, Devminor: 3}}

	for _, tc := range []struct {
		name    string
		entries []tarEntry
		setup   func(root, lower string)
		// needsCaps: the test container may lack the capability to make
		// device nodes; then both extractions must fail alike.
		needsCaps bool
	}{
		{name: "many files", entries: manyFiles()},
		{name: "owners, modes, times and xattrs", entries: []tarEntry{ownedDir, owned, xattrFile, oldTimes}},
		{name: "devices", entries: []tarEntry{fifo, char}, needsCaps: true},
		{name: "a directory after its files", entries: []tarEntry{
			tarFile("late/a", "a", 0644), tarFile("late/sub/b", "b", 0644), tarDir("late/sub", 0555), tarDir("late", 0700),
		}},
		{name: "implicit parents copy the lower directory", entries: []tarEntry{
			tarFile("from-lower/deep/file", "in a lower directory", 0644),
		}, setup: func(_, lower string) {
			dir := filepath.Join(lower, "from-lower")
			require.NoError(t, os.MkdirAll(dir, 0700))
			require.NoError(t, os.Chown(dir, 1234, 5678))
			if err := unix.Lsetxattr(dir, "user.lower", []byte("x"), 0); err != unix.ENOTSUP {
				require.NoError(t, err)
			}
			require.NoError(t, os.Chtimes(dir, layerTime, layerTime))
		}},
		{name: "entries under symlinks stay under the root", entries: []tarEntry{
			tarDir("real", 0755),
			tarSymlink("rel", "real"),
			tarFile("rel/through-relative", "x", 0644),
			tarSymlink("abs", "/real"),
			tarFile("abs/through-absolute", "y", 0644),
			tarSymlink("escape", "../../../.."),
			tarFile("escape/through-escape", "z", 0644),
			tarFile("../dotdot", "w", 0644),
			tarFile("/absolute-name", "v", 0644),
			tarFile("real/../../up-and-back", "u", 0644),
		}},
		{name: "a symlink replaces a directory", entries: []tarEntry{
			tarFile("swap/file", "before", 0644),
			tarSymlink("swap", "real"),
			tarDir("real", 0755),
			tarFile("swap/file", "after", 0644),
		}},
		{name: "hard links to files still being written", entries: append(manyFiles(),
			tarHardlink("tree/link-small", "tree/d3/f19"),
			tarHardlink("tree/link-large", "tree/over-limit"),
			tarSymlink("tree/symlink", "d0/f0"),
			tarHardlink("tree/link-to-symlink", "tree/symlink"),
		)},
		{name: "a later entry replaces an earlier one", entries: []tarEntry{
			tarFile("dup", "first", 0644),
			tarFile("dup", "second", 0600),
			tarFile("file-then-dir", "file", 0644),
			tarDir("file-then-dir", 0755),
			tarFile("file-then-dir/inner", "inner", 0644),
			tarDir("dir-then-file", 0755),
			tarFile("dir-then-file/inner", "inner", 0644),
			tarFile("dir-then-file", "file", 0644),
		}},
		{name: "existing files are replaced", entries: []tarEntry{
			tarFile("present/file", "new", 0644),
			tarDir("present/dir", 0700),
		}, setup: func(root, _ string) {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "present/dir"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "present/dir/kept"), []byte("kept"), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(root, "present/file"), []byte("old"), 0644))
			require.NoError(t, os.Chtimes(filepath.Join(root, "present/dir/kept"), layerTime, layerTime))
		}},
	} {
		for _, overlay := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s overlay=%v", tc.name, overlay), func(t *testing.T) {
				_, err := applyBoth(t, tarOf(t, tc.entries...), overlay, tc.setup)
				if tc.needsCaps && err != nil {
					t.Skipf("both extractions failed alike here: %v", err)
				}
				require.NoError(t, err)
			})
		}
	}
}

func TestLayerApplyWhiteouts(t *testing.T) {
	for _, overlay := range []bool{false, true} {
		// What a whiteout hides is in the lower directory under an overlay,
		// and already in the directory otherwise.
		setup := func(root, lower string) {
			dir := root
			if overlay {
				dir = lower
			}
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "opaque"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "opaque/old"), []byte("old"), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "gone"), []byte("gone"), 0644))
		}
		entries := append(manyFiles(), tarFile(".wh.gone", "", 0644))
		if !overlay {
			// Without an overlay a whiteout removes its target, here a
			// file of this layer a worker may still be writing.
			entries = append(entries, tarFile("tree/d0/.wh.f3", "", 0644))
		}
		t.Run(fmt.Sprintf("whiteouts overlay=%v", overlay), func(t *testing.T) {
			root, err := applyBoth(t, tarOf(t, entries...), overlay, setup)
			if overlay && err != nil {
				// An overlay whiteout is a 0:0 character device, which the
				// test container may not be allowed to make.
				t.Skipf("both extractions failed alike here: %v", err)
			}
			require.NoError(t, err)
			fi, err := os.Lstat(filepath.Join(root, "gone"))
			if overlay {
				require.NoError(t, err)
				require.Equal(t, os.ModeDevice|os.ModeCharDevice, fi.Mode().Type())
			} else {
				require.True(t, os.IsNotExist(err))
				_, err = os.Lstat(filepath.Join(root, "tree/d0/f3"))
				require.True(t, os.IsNotExist(err))
			}
		})
		t.Run(fmt.Sprintf("opaque directory overlay=%v", overlay), func(t *testing.T) {
			layer := tarOf(t, append(manyFiles(), tarFile("opaque/new", "new", 0644), tarFile("opaque/.wh..wh..opq", "", 0644))...)
			root, err := applyBoth(t, layer, overlay, setup)
			if overlay {
				// An overlay's opaque directory is a trusted xattr, which
				// needs CAP_SYS_ADMIN.
				if err != nil {
					t.Skipf("both extractions failed alike here: %v", err)
				}
				return
			}
			require.NoError(t, err)
			_, err = os.Lstat(filepath.Join(root, "opaque/old"))
			require.True(t, os.IsNotExist(err))
		})
	}
}

func TestLayerApplyFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		layer func(t *testing.T) []byte
	}{
		{name: "a file under a file still being written", layer: func(t *testing.T) []byte {
			return tarOf(t, append(manyFiles(), tarFile("tree/d1/f2/under", "x", 0644))...)
		}},
		{name: "a hard link to a missing file", layer: func(t *testing.T) []byte {
			return tarOf(t, tarHardlink("link", "missing"))
		}},
		{name: "a hard link outside the root", layer: func(t *testing.T) []byte {
			return tarOf(t, tarHardlink("link", "../../../etc/passwd"))
		}},
		{name: "a hard link to a later entry", layer: func(t *testing.T) []byte {
			return tarOf(t, tarHardlink("early", "later"), tarFile("later", "later", 0644))
		}},
		{name: "a whiteout outside its directory", layer: func(t *testing.T) []byte {
			return tarOf(t, tarFile("dir/.wh...", "", 0644))
		}},
		{name: "truncated in a large file", layer: func(t *testing.T) []byte {
			full := tarOf(t, manyFiles()...)
			return full[:len(full)-layerApplyBufferedMax/2]
		}},
		{name: "truncated in a small file", layer: func(t *testing.T) []byte {
			// A PAX header and its records, a's header, then half its data.
			full := tarOf(t, tarFile("a", strings.Repeat("a", 4096), 0644), tarFile("b", "b", 0644))
			return full[:3*512+2048]
		}},
	} {
		for _, overlay := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s overlay=%v", tc.name, overlay), func(t *testing.T) {
				_, err := applyBoth(t, tc.layer(t), overlay, nil)
				require.Error(t, err)
			})
		}
	}

	t.Run("a worker fails", func(t *testing.T) {
		failed := errors.New("worker failed")
		var after atomic.Int64
		var returned atomic.Bool
		setBeforeLayerFileWrite(t, func(path string) error {
			if returned.Load() {
				after.Add(1)
			}
			if strings.HasSuffix(path, "tree/d1/f5") {
				return failed
			}
			time.Sleep(time.Millisecond)
			return nil
		})
		err := applyLayerTar(context.Background(), layerTarget{root: t.TempDir()}, bytes.NewReader(tarOf(t, manyFiles()...)))
		returned.Store(true)
		require.ErrorIs(t, err, failed)
		time.Sleep(50 * time.Millisecond)
		require.Zero(t, after.Load(), "a worker wrote after the extraction returned")
	})

	t.Run("canceled while files are queued", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var after atomic.Int64
		var returned atomic.Bool
		setBeforeLayerFileWrite(t, func(path string) error {
			if returned.Load() {
				after.Add(1)
			}
			cancel()
			time.Sleep(time.Millisecond)
			return nil
		})
		root := t.TempDir()
		err := applyLayerTar(ctx, layerTarget{root: root}, bytes.NewReader(tarOf(t, manyFiles()...)))
		returned.Store(true)
		require.ErrorIs(t, err, context.Canceled)
		time.Sleep(50 * time.Millisecond)
		require.Zero(t, after.Load(), "a worker wrote after the extraction returned")
	})

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := applyLayerTar(ctx, layerTarget{root: t.TempDir()}, bytes.NewReader(tarOf(t, manyFiles()...)))
		require.ErrorIs(t, err, context.Canceled)
	})
}

// applyBlob stores data gzipped as a layer blob and applies it through
// NewLayerApplier into a new directory.
func applyBlob(t *testing.T, data []byte, opts ...diff.ApplyOpt) (string, ocispecs.Descriptor, error) {
	t.Helper()
	store, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, err = zw.Write(data)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return applyStored(t, store, gz.Bytes(), opts...)
}

func applyStored(t *testing.T, store content.Store, blob []byte, opts ...diff.ApplyOpt) (string, ocispecs.Descriptor, error) {
	t.Helper()
	ctx := context.Background()
	desc := ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayerGzip, Digest: digest.FromBytes(blob), Size: int64(len(blob))}
	require.NoError(t, content.WriteBlob(ctx, store, desc.Digest.String(), bytes.NewReader(blob), desc))
	root := t.TempDir()
	applied, err := NewLayerApplier(store).Apply(ctx, desc, []mount.Mount{{Type: "bind", Source: root, Options: []string{"rbind", "rw"}}}, opts...)
	return root, applied, err
}

// The applier reads the blob from the content store, decompresses it, and
// returns the uncompressed digest, as containerd's applier does.
func TestLayerApplierApply(t *testing.T) {
	layer := tarOf(t, manyFiles()...)
	var progress []int64
	root, applied, err := applyBlob(t, layer, diff.WithProgress(func(_ ocispecs.Descriptor, n int64) { progress = append(progress, n) }))
	require.NoError(t, err)
	require.Equal(t, digest.FromBytes(layer), applied.Digest)
	require.Equal(t, int64(len(layer)), applied.Size)
	require.NotEmpty(t, progress)
	require.Positive(t, progress[len(progress)-1])
	data, err := os.ReadFile(filepath.Join(root, "tree/over-limit"))
	require.NoError(t, err)
	require.Len(t, data, layerApplyBufferedMax+1)

	t.Run("trailing data", func(t *testing.T) {
		// The digest covers the whole stream, as the image's diffID does.
		stream := append(append([]byte{}, layer...), bytes.Repeat([]byte("trailing"), layerReadAheadChunk/4)...)
		_, applied, err := applyBlob(t, stream)
		require.NoError(t, err)
		require.Equal(t, digest.FromBytes(stream), applied.Digest)
		require.Equal(t, int64(len(stream)), applied.Size)
	})

	t.Run("a stream that fails", func(t *testing.T) {
		store, err := local.NewStore(t.TempDir())
		require.NoError(t, err)
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		_, err = zw.Write(layer)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		// Cut in the last chunk the read-ahead reads, past every entry
		// but the end of the archive.
		_, _, err = applyStored(t, store, gz.Bytes()[:gz.Len()-8])
		require.Error(t, err)
	})

	t.Run("an extraction that fails early", func(t *testing.T) {
		// The read-ahead is still reading when the extraction fails.
		_, _, err := applyBlob(t, tarOf(t, append([]tarEntry{tarHardlink("link", "missing")}, manyFiles()...)...))
		require.Error(t, err)
	})
}

// Applying a small layer allocates far less than a read-ahead chunk: the
// chunks are reused across layers, as an engine applies thousands of small
// uncompressed ones, the parts its image ships.
func TestLayerApplyReusesReadAheadChunks(t *testing.T) {
	ctx := context.Background()
	layer := tarOf(t, tarDir("pkg", 0o755), tarFile("pkg/archive.a", strings.Repeat("x", 64<<10), 0o644))
	store, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	desc := ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayer, Digest: digest.FromBytes(layer), Size: int64(len(layer))}
	require.NoError(t, content.WriteBlob(ctx, store, desc.Digest.String(), bytes.NewReader(layer), desc))
	apply := func() {
		root := t.TempDir()
		applied, err := NewLayerApplier(store).Apply(ctx, desc, []mount.Mount{{Type: "bind", Source: root, Options: []string{"rbind", "rw"}}})
		require.NoError(t, err)
		require.Equal(t, desc.Digest, applied.Digest)
	}
	apply()

	const applies = 30
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range applies {
		apply()
	}
	runtime.ReadMemStats(&after)
	perApply := (after.TotalAlloc - before.TotalAlloc) / applies
	// A chunk made per layer is a whole chunk per apply, on top of the
	// apply's other allocations. Reused, it is a fraction: the race
	// detector's pool drops a quarter of what is put back, so a run with it
	// makes a new chunk for about a quarter of the applies.
	require.Less(t, perApply, uint64(layerReadAheadChunk), "each apply allocated %d bytes", perApply)
}

func TestLayerApplyTarget(t *testing.T) {
	target, ok := layerApplyTarget([]mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"index=off", "workdir=/w", "upperdir=/u", "lowerdir=/l1:/l2"}}})
	require.True(t, ok)
	require.Equal(t, layerTarget{root: "/u", parents: []string{"/l1", "/l2"}, overlay: true}, target)

	target, ok = layerApplyTarget([]mount.Mount{{Type: "bind", Source: "/s", Options: []string{"rbind", "rw"}}})
	require.True(t, ok)
	require.Equal(t, layerTarget{root: "/s"}, target)

	for _, mounts := range [][]mount.Mount{
		{{Type: "bind", Source: "/s", Options: []string{"rbind", "ro"}}},
		{{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/l1:/l2"}}},
		{{Type: "bind", Source: "/a"}, {Type: "bind", Source: "/b"}},
		{{Type: "tmpfs", Source: "tmpfs"}},
	} {
		_, ok := layerApplyTarget(mounts)
		require.False(t, ok, "%v", mounts)
	}
}

// A path's resolution is remembered only until a symlink appears along it:
// here the symlink replacing a directory points outside the root, which
// containerd keeps under the root.
func TestLayerApplySymlinkReplacingADirectory(t *testing.T) {
	outside := t.TempDir()
	for _, overlay := range []bool{false, true} {
		t.Run(fmt.Sprintf("overlay=%v", overlay), func(t *testing.T) {
			root, err := applyBoth(t, tarOf(t,
				tarFile("swap/sub/file", "before", 0644),
				tarSymlink("swap", outside),
				tarFile("swap/sub/file", "through the symlink", 0644),
				tarFile("swap/other", "through the symlink", 0644),
			), overlay, nil)
			require.NoError(t, err)
			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			require.Empty(t, entries)
			data, err := os.ReadFile(filepath.Join(root, outside, "sub/file"))
			require.NoError(t, err)
			require.Equal(t, "through the symlink", string(data))
		})
	}
}

// A file takes its directory's default ACL when it is created, so a
// directory header after a file under it must not reach the file first.
func TestLayerApplyDirectoryAfterItsFiles(t *testing.T) {
	// A default ACL: owner rwx, user 1234 r, group r-x, mask r-x, other r-x.
	acl := []byte{2, 0, 0, 0}
	for _, e := range []struct {
		tag, perm uint16
		id        uint32
	}{{0x01, 7, 0xffffffff}, {0x02, 4, 1234}, {0x04, 5, 0xffffffff}, {0x10, 5, 0xffffffff}, {0x20, 5, 0xffffffff}} {
		acl = binary.LittleEndian.AppendUint16(acl, e.tag)
		acl = binary.LittleEndian.AppendUint16(acl, e.perm)
		acl = binary.LittleEndian.AppendUint32(acl, e.id)
	}
	dir := tarDir("d", 0755)
	dir.hdr.PAXRecords = map[string]string{"SCHILY.xattr.system.posix_acl_default": string(acl)}
	// The header comes while d/f is still queued: the queue holds a few
	// files, and the workers are slowed.
	entries := []tarEntry{tarFile("d/f", "file", 0640), dir, tarFile("d/later", "later", 0640)}
	entries = append(entries, manyFiles()...)
	for _, overlay := range []bool{false, true} {
		t.Run(fmt.Sprintf("overlay=%v", overlay), func(t *testing.T) {
			root, err := applyBoth(t, tarOf(t, entries...), overlay, nil)
			require.NoError(t, err)
			if _, err := sysx.LGetxattr(filepath.Join(root, "d/later"), "system.posix_acl_access"); err != nil {
				t.Skipf("no ACLs on this file system, so nothing to tell apart: %v", err)
			}
		})
	}
}
