package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	var tarBuf bytes.Buffer
	require.NoError(t, tar.NewWriter(&tarBuf).Close())
	tarPath := filepath.Join(t.TempDir(), "empty.tar")
	require.NoError(t, os.WriteFile(tarPath, tarBuf.Bytes(), 0o644))
	for name, archive := range map[string]string{"zip": writeTestZip(t), "tar": tarPath} {
		t.Run(name, func(t *testing.T) {
			dest, _, err := extractTestArchive(t, archive, 0)
			require.NoError(t, err)
			require.Empty(t, listTree(t, dest))
		})
	}
}

func TestExtractTarGz(t *testing.T) {
	archive := writeTestTarGz(t,
		testTarEntry{name: "top/", typeflag: tar.TypeDir},
		testTarEntry{name: "top/a", body: "a"},
		testTarEntry{name: "top/sub/b", body: "b"},
		testTarEntry{name: "top/hard", typeflag: tar.TypeLink, linkname: "top/a"},
		testTarEntry{name: "top/soft", typeflag: tar.TypeSymlink, linkname: "a"},
		testTarEntry{name: "loose", body: "dropped"},
	)
	t.Run("whole", func(t *testing.T) {
		dest, _, err := extractTestArchive(t, archive, 0)
		require.NoError(t, err)
		require.Equal(t, []string{
			"loose -rw-r--r--",
			"top drwxr-xr-x",
			"top/a -rw-r--r--",
			"top/hard -rw-r--r--",
			"top/soft Lrwxrwxrwx -> a",
			"top/sub drwxr-xr-x",
			"top/sub/b -rw-r--r--",
		}, listTree(t, dest))
	})
	t.Run("strip", func(t *testing.T) {
		dest, _, err := extractTestArchive(t, archive, 1)
		require.NoError(t, err)
		require.Equal(t, []string{
			"a -rw-r--r--",
			"hard -rw-r--r--",
			"soft Lrwxrwxrwx -> a",
			"sub drwxr-xr-x",
			"sub/b -rw-r--r--",
		}, listTree(t, dest))
		a, err := os.Stat(filepath.Join(dest, "a"))
		require.NoError(t, err)
		hard, err := os.Stat(filepath.Join(dest, "hard"))
		require.NoError(t, err)
		require.True(t, os.SameFile(a, hard), "the hard link's target is stripped too")
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
