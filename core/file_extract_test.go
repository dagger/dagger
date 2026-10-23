package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	"github.com/stretchr/testify/require"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkcontainerd "github.com/dagger/dagger/engine/snapshots/containerd"
)

type testZipEntry struct {
	name    string
	body    string
	mode    fs.FileMode // zero: no Unix mode recorded
	symlink bool
	mtime   time.Time
}

func writeTestZip(t *testing.T, entries ...testZipEntry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: e.mtime}
		switch {
		case e.symlink:
			hdr.SetMode(fs.ModeSymlink | 0o777)
		case e.mode != 0:
			hdr.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(hdr)
		require.NoError(t, err)
		_, err = w.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	p := filepath.Join(t.TempDir(), "test.zip")
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o644))
	return p
}

type testTarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func writeTestTarGz(t *testing.T, entries ...testTarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Typeflag: typeflag, Linkname: e.linkname, Mode: 0o644, Size: int64(len(e.body))}
		if typeflag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	p := filepath.Join(t.TempDir(), "test.tar.gz")
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o644))
	return p
}

// extractTestArchive extracts archive into a fresh directory inside a
// parent that holds nothing else, returning both. It extracts with the
// engine's umask, 0, so modes are exactly what extraction chose.
func extractTestArchive(t *testing.T, archive string, strip int) (dest string, parent string, err error) {
	t.Helper()
	defer syscall.Umask(syscall.Umask(0))
	parent = t.TempDir()
	dest = filepath.Join(parent, "dest")
	require.NoError(t, os.Mkdir(dest, 0o755))
	err = extractArchive(context.Background(), archive, dest, strip)
	return dest, parent, err
}

// listTree lists dest's entries as "path mode [-> target]".
func listTree(t *testing.T, dest string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if p == dest {
			return nil
		}
		rel, err := filepath.Rel(dest, p)
		require.NoError(t, err)
		info, err := os.Lstat(p)
		require.NoError(t, err)
		line := rel + " " + info.Mode().String()
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			require.NoError(t, err)
			line += " -> " + target
		}
		out = append(out, line)
		return nil
	}))
	sort.Strings(out)
	return out
}

func requireOnlyDest(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Len(t, entries, 1, "nothing may be written beside the extraction directory")
	require.Equal(t, "dest", entries[0].Name())
}

func TestExtractZip(t *testing.T) {
	mtime := time.Date(2020, 1, 2, 3, 4, 6, 0, time.UTC)
	archive := writeTestZip(t,
		testZipEntry{name: "README", body: "readme"},
		testZipEntry{name: "bin/", mode: fs.ModeDir | 0o700, mtime: mtime},
		testZipEntry{name: "bin/tool", body: "#!/bin/sh\n", mode: 0o755, mtime: mtime},
		testZipEntry{name: "deep/er/file.txt", body: "deep"},
		testZipEntry{name: "link", body: "deep/er/file.txt", symlink: true, mtime: mtime},
		testZipEntry{name: "/abs/file", body: "abs"},
	)
	dest, parent, err := extractTestArchive(t, archive, 0)
	require.NoError(t, err)
	requireOnlyDest(t, parent)
	require.Equal(t, []string{
		"README -rw-r--r--",
		"abs drwxr-xr-x",
		"abs/file -rw-r--r--",
		"bin drwx------",
		"bin/tool -rwxr-xr-x",
		"deep drwxr-xr-x",
		"deep/er drwxr-xr-x",
		"deep/er/file.txt -rw-r--r--",
		"link Lrwxrwxrwx -> deep/er/file.txt",
	}, listTree(t, dest))
	b, err := os.ReadFile(filepath.Join(dest, "link"))
	require.NoError(t, err)
	require.Equal(t, "deep", string(b))
	for _, p := range []string{"bin", "bin/tool", "link"} {
		info, err := os.Lstat(filepath.Join(dest, p))
		require.NoError(t, err)
		require.True(t, info.ModTime().Equal(mtime), "%s: mtime %s", p, info.ModTime())
	}
}

func TestExtractZipStripComponents(t *testing.T) {
	archive := writeTestZip(t,
		testZipEntry{name: "top/", mode: fs.ModeDir | 0o755},
		testZipEntry{name: "top/a", body: "a"},
		testZipEntry{name: "top/sub/b", body: "b"},
		testZipEntry{name: "/top/c", body: "c"},
		testZipEntry{name: "loose", body: "dropped"},
	)
	dest, _, err := extractTestArchive(t, archive, 1)
	require.NoError(t, err)
	require.Equal(t, []string{
		"a -rw-r--r--",
		"c -rw-r--r--",
		"sub drwxr-xr-x",
		"sub/b -rw-r--r--",
	}, listTree(t, dest))
}

// As tar --strip-components, stripping counts a "." element.
func TestExtractStripComponentsCountsDot(t *testing.T) {
	archive := writeTestZip(t,
		testZipEntry{name: "./LICENSE", body: "license"},
		testZipEntry{name: "./top/f", body: "f"},
	)
	dest, _, err := extractTestArchive(t, archive, 1)
	require.NoError(t, err)
	require.Equal(t, []string{
		"LICENSE -rw-r--r--",
		"top drwxr-xr-x",
		"top/f -rw-r--r--",
	}, listTree(t, dest))
}

// A recorded mode of 000 is kept; only a missing mode gets the default.
func TestExtractZipZeroMode(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "locked", Method: zip.Deflate}
	hdr.SetMode(0)
	_, err := zw.CreateHeader(hdr)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	archive := filepath.Join(t.TempDir(), "zero.zip")
	require.NoError(t, os.WriteFile(archive, buf.Bytes(), 0o644))
	dest, _, err := extractTestArchive(t, archive, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"locked ----------"}, listTree(t, dest))
}

// cancelingReaderAt cancels once more than limit bytes have been read.
type cancelingReaderAt struct {
	r      io.ReaderAt
	limit  int64
	read   atomic.Int64
	cancel context.CancelFunc
}

func (c *cancelingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	if c.read.Add(int64(n)) > c.limit {
		c.cancel()
	}
	return n, err
}

// Canceling stops an extraction in the middle of an entry.
func TestExtractZipCanceled(t *testing.T) {
	const size = 256 << 20
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "zeros", Method: zip.Deflate})
	require.NoError(t, err)
	chunk := make([]byte, 1<<20)
	for range size / len(chunk) {
		_, err := w.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	dest := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The central directory is read first; cancel a little into the entry.
	r := &cancelingReaderAt{r: bytes.NewReader(buf.Bytes()), limit: 64 << 10, cancel: cancel}
	err = extractZip(ctx, r, int64(buf.Len()), dest, 0)
	require.ErrorIs(t, err, context.Canceled)
	info, err := os.Stat(filepath.Join(dest, "zeros"))
	require.NoError(t, err)
	require.Less(t, info.Size(), int64(size))
}

func TestExtractTarCanceled(t *testing.T) {
	archive := writeTestTarGz(t, testTarEntry{name: "f", body: "f"})
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	require.NoError(t, os.Mkdir(dest, 0o755))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := extractArchive(ctx, archive, dest, 0)
	require.ErrorIs(t, err, context.Canceled)
	entries, err := os.ReadDir(dest)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// releaseRecorder records whether the mutable refs its New returns are
// released.
type releaseRecorder struct {
	bkcache.Accessor
	released bool
}

func (r *releaseRecorder) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	ref, err := r.Accessor.New(ctx, parent, opts...)
	if err != nil {
		return nil, err
	}
	return &recordedMutableRef{MutableRef: ref, recorder: r}, nil
}

type recordedMutableRef struct {
	bkcache.MutableRef
	recorder *releaseRecorder
}

func (r *recordedMutableRef) Release(ctx context.Context) error {
	r.recorder.released = true
	return r.MutableRef.Release(ctx)
}

// A failed extraction releases the snapshot it was writing.
func TestExtractToSnapshotReleasesOnError(t *testing.T) {
	snapshotter, err := native.NewSnapshotter(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshotter.Close()) })
	sm, err := bkcache.NewSnapshotManager(bkcache.SnapshotManagerOpt{
		Snapshotter:   bkcontainerd.NewSnapshotter("native", snapshotter, "extract-test"),
		MountPoolRoot: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	recorder := &releaseRecorder{Accessor: sm}
	archive := writeTestZip(t, testZipEntry{name: "../evil", body: "evil"})
	_, err = extractToSnapshot(context.Background(), recorder, archive, "test.zip", 0)
	require.ErrorContains(t, err, "leaves the extraction directory")
	require.True(t, recorder.released)
}

func TestExtractZipDuplicateEntries(t *testing.T) {
	archive := writeTestZip(t,
		testZipEntry{name: "f", body: "first"},
		testZipEntry{name: "f", body: "second"},
	)
	dest, _, err := extractTestArchive(t, archive, 0)
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(dest, "f"))
	require.NoError(t, err)
	require.Equal(t, "second", string(b))
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../evil", "a/../../evil", "a/../../../evil"} {
		t.Run(name, func(t *testing.T) {
			archive := writeTestZip(t, testZipEntry{name: name, body: "evil"})
			_, parent, err := extractTestArchive(t, archive, 0)
			require.ErrorContains(t, err, "leaves the extraction directory")
			requireOnlyDest(t, parent)
		})
	}
}

// A zip can't write through a symlink it makes: symlinks are made last.
func TestExtractZipSymlinkSlip(t *testing.T) {
	outside := t.TempDir()
	archive := writeTestZip(t,
		testZipEntry{name: "escape", body: outside, symlink: true},
		testZipEntry{name: "escape/evil", body: "evil"},
	)
	_, parent, err := extractTestArchive(t, archive, 0)
	require.Error(t, err)
	requireOnlyDest(t, parent)
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// A symlink entry's target is read into memory, so only as much of it as a
// target can be long.
func TestExtractZipSymlinkTargetBounded(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "link", Method: zip.Deflate}
	hdr.SetMode(fs.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	require.NoError(t, err)
	chunk := bytes.Repeat([]byte("a"), 1<<20)
	for range 256 {
		_, err := w.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	archive := filepath.Join(t.TempDir(), "bomb.zip")
	require.NoError(t, os.WriteFile(archive, buf.Bytes(), 0o644))

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err = extractTestArchive(t, archive, 0)
	runtime.ReadMemStats(&after)
	require.Error(t, err)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(32<<20))
}

func TestExtractZipUnsupportedType(t *testing.T) {
	archive := writeTestZip(t, testZipEntry{name: "fifo", mode: fs.ModeNamedPipe | 0o644})
	_, _, err := extractTestArchive(t, archive, 0)
	require.ErrorContains(t, err, "unsupported file type")
}

func TestExtractEmpty(t *testing.T) {
	dest, _, err := extractTestArchive(t, writeTestZip(t), 0)
	require.NoError(t, err)
	require.Empty(t, listTree(t, dest))

	var tarBuf bytes.Buffer
	require.NoError(t, tar.NewWriter(&tarBuf).Close())
	tarPath := filepath.Join(t.TempDir(), "empty.tar")
	require.NoError(t, os.WriteFile(tarPath, tarBuf.Bytes(), 0o644))
	entries, err := stripTestTar(t, tarPath, 0)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// stripTestTar runs the tar at archive, uncompressed or gzipped, through
// stripTarComponents and lists the entries it writes as "name", "name =
// body", "name => hard link target" or "name -> symlink target". The
// unpacking itself needs a mount namespace, so the integration tests
// cover it.
func stripTestTar(t *testing.T, archive string, strip int) ([]string, error) {
	t.Helper()
	f, err := os.Open(archive)
	require.NoError(t, err)
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(archive, ".gz") {
		gr, err := gzip.NewReader(f)
		require.NoError(t, err)
		r = gr
	}
	var out bytes.Buffer
	if err := stripTarComponents(r, &out, strip); err != nil {
		return nil, err
	}
	var entries []string
	tr := tar.NewReader(&out)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return entries, nil
		}
		require.NoError(t, err)
		switch hdr.Typeflag {
		case tar.TypeReg:
			body, err := io.ReadAll(tr)
			require.NoError(t, err)
			entries = append(entries, hdr.Name+" = "+string(body))
		case tar.TypeLink:
			entries = append(entries, hdr.Name+" => "+hdr.Linkname)
		case tar.TypeSymlink:
			entries = append(entries, hdr.Name+" -> "+hdr.Linkname)
		default:
			entries = append(entries, hdr.Name)
		}
	}
}

func TestStripTarComponents(t *testing.T) {
	archive := writeTestTarGz(t,
		testTarEntry{name: "top/", typeflag: tar.TypeDir},
		testTarEntry{name: "top/a", body: "a"},
		testTarEntry{name: "top/sub/b", body: "b"},
		testTarEntry{name: "top/hard", typeflag: tar.TypeLink, linkname: "top/a"},
		testTarEntry{name: "top/soft", typeflag: tar.TypeSymlink, linkname: "a"},
		testTarEntry{name: "./top/dot", body: "dot"},
		testTarEntry{name: "loose", body: "dropped"},
	)
	t.Run("whole", func(t *testing.T) {
		entries, err := stripTestTar(t, archive, 0)
		require.NoError(t, err)
		require.Equal(t, []string{
			"top/",
			"top/a = a",
			"top/sub/b = b",
			"top/hard => top/a",
			"top/soft -> a",
			"top/dot = dot",
			"loose = dropped",
		}, entries)
	})
	t.Run("strip", func(t *testing.T) {
		entries, err := stripTestTar(t, archive, 1)
		require.NoError(t, err)
		require.Equal(t, []string{
			"a = a",
			"sub/b = b",
			"hard => a",
			"soft -> a",
			"top/dot = dot",
		}, entries, "the hard link's target is stripped too, and \".\" counts as an element")
	})
}

func TestExtractTarRejectsTraversal(t *testing.T) {
	for _, e := range []testTarEntry{
		{name: "../evil", body: "evil"},
		{name: "hard", typeflag: tar.TypeLink, linkname: "../evil"},
	} {
		t.Run(e.name, func(t *testing.T) {
			archive := writeTestTarGz(t, e)
			_, parent, err := extractTestArchive(t, archive, 0)
			require.ErrorContains(t, err, "leaves the extraction directory")
			requireOnlyDest(t, parent)
		})
	}
}

func TestExtractNotAnArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(p, []byte(strings.Repeat("not an archive\n", 100)), 0o644))
	_, _, err := extractTestArchive(t, p, 0)
	require.ErrorContains(t, err, "not a zip or tar archive")
}
