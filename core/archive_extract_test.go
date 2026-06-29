package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

func TestExtractArchiveFileTarFormats(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compress func(*testing.T, []byte) []byte
	}{
		{name: "tar"},
		{name: "tar.gz", compress: gzipBytes},
		{name: "tar.xz", compress: xzBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := tarBytes(t, []tarTestEntry{
				{name: "root/bin/tool", mode: 0o755, body: "hello"},
				{name: "root/docs/readme.txt", mode: 0o644, body: "readme"},
				{name: "root/link", typeflag: tar.TypeSymlink, linkname: "docs/readme.txt", mode: 0o777},
			})
			if tc.compress != nil {
				archive = tc.compress(t, archive)
			}

			src := writeTempFile(t, archive)
			dest := t.TempDir()

			require.NoError(t, extractArchiveFile(src, dest, 1))
			requireFileContents(t, filepath.Join(dest, "bin", "tool"), "hello")
			requireFileContents(t, filepath.Join(dest, "docs", "readme.txt"), "readme")

			info, err := os.Stat(filepath.Join(dest, "bin", "tool"))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o755), info.Mode().Perm())

			target, err := os.Readlink(filepath.Join(dest, "link"))
			require.NoError(t, err)
			require.Equal(t, "docs/readme.txt", target)
		})
	}
}

func TestExtractArchiveFileZip(t *testing.T) {
	src := writeTempFile(t, zipBytes(t, []zipTestEntry{
		{name: "root/bin/tool", mode: 0o755, body: "hello"},
		{name: "root/docs/readme.txt", mode: 0o644, body: "readme"},
		{name: "root/link", mode: os.ModeSymlink | 0o777, body: "docs/readme.txt"},
	}))
	dest := t.TempDir()

	require.NoError(t, extractArchiveFile(src, dest, 1))
	requireFileContents(t, filepath.Join(dest, "bin", "tool"), "hello")
	requireFileContents(t, filepath.Join(dest, "docs", "readme.txt"), "readme")

	info, err := os.Stat(filepath.Join(dest, "bin", "tool"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	target, err := os.Readlink(filepath.Join(dest, "link"))
	require.NoError(t, err)
	require.Equal(t, "docs/readme.txt", target)
}

func TestExtractArchiveFileStripComponentsSkipsEntries(t *testing.T) {
	src := writeTempFile(t, tarBytes(t, []tarTestEntry{
		{name: "top.txt", mode: 0o644, body: "skip"},
		{name: "root/keep.txt", mode: 0o644, body: "keep"},
	}))
	dest := t.TempDir()

	require.NoError(t, extractArchiveFile(src, dest, 1))
	require.NoFileExists(t, filepath.Join(dest, "top.txt"))
	requireFileContents(t, filepath.Join(dest, "keep.txt"), "keep")
}

func TestExtractArchiveFileRejectsUnsafeTarPath(t *testing.T) {
	src := writeTempFile(t, tarBytes(t, []tarTestEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "../outside", mode: 0o777},
	}))
	dest := t.TempDir()

	err := extractArchiveFile(src, dest, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes extraction root")
	require.NoFileExists(t, filepath.Join(dest, "link"))
}

func TestExtractArchiveFileRejectsUnsafeZipSymlink(t *testing.T) {
	src := writeTempFile(t, zipBytes(t, []zipTestEntry{
		{name: "root/link", mode: os.ModeSymlink | 0o777, body: "../outside"},
	}))
	dest := t.TempDir()

	err := extractArchiveFile(src, dest, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes extraction root")
	require.NoFileExists(t, filepath.Join(dest, "link"))
}

type tarTestEntry struct {
	name     string
	typeflag byte
	linkname string
	mode     int64
	body     string
}

func tarBytes(t *testing.T, entries []tarTestEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     entry.name,
			Typeflag: typeflag,
			Linkname: entry.linkname,
			Mode:     entry.mode,
			ModTime:  time.Unix(123, 0),
		}
		if typeflag == tar.TypeReg {
			hdr.Size = int64(len(entry.body))
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if hdr.Size > 0 {
			_, err := tw.Write([]byte(entry.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

type zipTestEntry struct {
	name string
	mode os.FileMode
	body string
}

func zipBytes(t *testing.T, entries []zipTestEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		hdr := &zip.FileHeader{
			Name:     entry.name,
			Method:   zip.Deflate,
			Modified: time.Unix(123, 0),
		}
		hdr.SetMode(entry.mode)
		w, err := zw.CreateHeader(hdr)
		require.NoError(t, err)
		_, err = io.WriteString(w, entry.body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func gzipBytes(t *testing.T, src []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write(src)
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func xzBytes(t *testing.T, src []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	xzw, err := xz.NewWriter(&buf)
	require.NoError(t, err)
	_, err = xzw.Write(src)
	require.NoError(t, err)
	require.NoError(t, xzw.Close())
	return buf.Bytes()
}

func writeTempFile(t *testing.T, contents []byte) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "archive-*")
	require.NoError(t, err)
	_, err = file.Write(contents)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	return file.Name()
}

func requireFileContents(t *testing.T, path, expected string) {
	t.Helper()

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, expected, string(contents))
}
