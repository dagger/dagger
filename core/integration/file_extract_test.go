package core

// These tests cover File.extract: extracting zip and tar archives into
// directories.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
)

type extractTestEntry struct {
	name    string
	body    []byte
	mode    fs.FileMode // zero: no Unix mode recorded
	symlink bool
	mtime   time.Time
}

func extractTestZip(t *testctx.T, entries ...extractTestEntry) []byte {
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
		_, err = w.Write(e.body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// hostArchive writes contents to name in a new host directory and returns
// that directory.
func hostArchive(t *testctx.T, name string, contents []byte) string {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), contents, 0o644))
	return dir
}

func extractHostArchive(c *dagger.Client, dir, name string, opts ...core.FileExtractOpts) *core.Directory {
	return core.NewQuery(c).Host().Directory(dir).File(name).Extract(opts...)
}

type extractedEntry struct {
	perm, mtime, kind string
}

// listExtracted stats every path under the directory: its permissions,
// modification time and type, and the raw listing, which also shows each
// symlink's target.
func listExtracted(ctx context.Context, t *testctx.T, c *dagger.Client, dir *core.Directory) (map[string]extractedEntry, string) {
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedDirectory("/x", dir).
		WithWorkdir("/x").
		WithExec([]string{"sh", "-c", `find . -mindepth 1 | sort | xargs stat -c '%n %a %Y %F|%N'`}).
		Stdout(ctx)
	require.NoError(t, err)
	entries := map[string]extractedEntry{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		stat, _, _ := strings.Cut(line, "|")
		f := strings.SplitN(stat, " ", 4)
		require.Len(t, f, 4, line)
		entries[f[0]] = extractedEntry{perm: f[1], mtime: f[2], kind: f[3]}
	}
	return entries, out
}

func (FileSuite) TestExtractZip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mtime := time.Date(2020, 1, 2, 3, 4, 6, 0, time.UTC)
	dir := hostArchive(t, "a.zip", extractTestZip(t,
		extractTestEntry{name: "README", body: []byte("readme")},
		extractTestEntry{name: "bin/", mode: fs.ModeDir | 0o700, mtime: mtime},
		extractTestEntry{name: "bin/tool", body: []byte("#!/bin/sh\necho tool\n"), mode: 0o755, mtime: mtime},
		extractTestEntry{name: "deep/er/file.txt", body: []byte("deep")},
		extractTestEntry{name: "link", body: []byte("deep/er/file.txt"), symlink: true, mtime: mtime},
	))
	extracted := extractHostArchive(c, dir, "a.zip")

	entries, listing := listExtracted(ctx, t, c, extracted)
	got := map[string]string{}
	for p, e := range entries {
		got[p] = e.perm + " " + e.kind
	}
	require.Equal(t, map[string]string{
		"./README":           "644 regular file",
		"./bin":              "700 directory",
		"./bin/tool":         "755 regular file",
		"./deep":             "755 directory",
		"./deep/er":          "755 directory",
		"./deep/er/file.txt": "644 regular file",
		"./link":             "777 symbolic link",
	}, got, listing)
	for _, p := range []string{"./bin", "./bin/tool", "./link"} {
		require.Equal(t, fmt.Sprint(mtime.Unix()), entries[p].mtime, "%s keeps the archive's modification time", p)
	}
	require.Contains(t, listing, "'./link' -> 'deep/er/file.txt'")

	contents, err := extracted.File("link").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "deep", contents)
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedDirectory("/x", extracted).
		WithExec([]string{"/x/bin/tool"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "tool\n", out)
}

func (FileSuite) TestExtractTarGz(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range []struct {
		name, body string
		mode       int64
		typeflag   byte
		linkname   string
	}{
		{"pkg-1.0/", "", 0o755, tar.TypeDir, ""},
		{"pkg-1.0/run.sh", "#!/bin/sh\necho run\n", 0o750, tar.TypeReg, ""},
		{"pkg-1.0/doc/notes.txt", "notes", 0o640, tar.TypeReg, ""},
		{"pkg-1.0/run-again", "", 0o750, tar.TypeLink, "pkg-1.0/run.sh"},
		{"pkg-1.0/latest", "", 0o777, tar.TypeSymlink, "run.sh"},
	} {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: e.typeflag, Linkname: e.linkname}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	dir := hostArchive(t, "pkg.tar.gz", buf.Bytes())

	entries, err := extractHostArchive(c, dir, "pkg.tar.gz").Glob(ctx, "**")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"pkg-1.0/", "pkg-1.0/run.sh", "pkg-1.0/doc/", "pkg-1.0/doc/notes.txt", "pkg-1.0/run-again", "pkg-1.0/latest"}, entries)

	stripped := extractHostArchive(c, dir, "pkg.tar.gz", core.FileExtractOpts{StripComponents: 1})
	extracted, listing := listExtracted(ctx, t, c, stripped)
	require.Len(t, extracted, 5, listing)
	require.Equal(t, extractedEntry{perm: "750", mtime: extracted["./run.sh"].mtime, kind: "regular file"}, extracted["./run.sh"])
	require.Equal(t, "640", extracted["./doc/notes.txt"].perm)
	require.Equal(t, "directory", extracted["./doc"].kind)
	require.Equal(t, "symbolic link", extracted["./latest"].kind)
	require.Contains(t, listing, "'./latest' -> 'run.sh'")

	// The hard link's target is stripped too: it links to run.sh.
	inodes, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedDirectory("/x", stripped).
		WithExec([]string{"stat", "-c", "%i %h", "/x/run.sh", "/x/run-again"}).
		Stdout(ctx)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(inodes), "\n")
	require.Len(t, lines, 2, inodes)
	require.Equal(t, lines[0], lines[1], inodes)
	require.True(t, strings.HasSuffix(lines[0], " 2"), inodes)
}

func (FileSuite) TestExtractStripComponents(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dir := hostArchive(t, "a.zip", extractTestZip(t,
		extractTestEntry{name: "mod@v1/", mode: fs.ModeDir | 0o755},
		extractTestEntry{name: "mod@v1/go.mod", body: []byte("module mod\n")},
		extractTestEntry{name: "mod@v1/sub/x.go", body: []byte("package sub\n")},
		extractTestEntry{name: "toplevel", body: []byte("dropped")},
	))
	entries, err := extractHostArchive(c, dir, "a.zip", core.FileExtractOpts{StripComponents: 1}).Glob(ctx, "**")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"go.mod", "sub/", "sub/x.go"}, entries)

	_, err = extractHostArchive(c, dir, "a.zip", core.FileExtractOpts{StripComponents: -1}).Sync(ctx)
	require.ErrorContains(t, err, "stripComponents must not be negative")
}

func (FileSuite) TestExtractRejectsEscapes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name    string
		entries []extractTestEntry
		err     string
	}{
		{"parent path", []extractTestEntry{{name: "../evil", body: []byte("evil")}}, "path leaves the extraction directory"},
		{"nested parent path", []extractTestEntry{{name: "a/../../evil", body: []byte("evil")}}, "path leaves the extraction directory"},
		{"through own symlink", []extractTestEntry{
			{name: "escape", body: []byte("/etc"), symlink: true},
			{name: "escape/evil", body: []byte("evil")},
		}, "file exists"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			dir := hostArchive(t, "evil.zip", extractTestZip(t, tc.entries...))
			_, err := extractHostArchive(c, dir, "evil.zip").Sync(ctx)
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func (FileSuite) TestExtractEmpty(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	var tarBuf bytes.Buffer
	require.NoError(t, tar.NewWriter(&tarBuf).Close())
	for name, contents := range map[string][]byte{
		"empty.zip": extractTestZip(t),
		"empty.tar": tarBuf.Bytes(),
	} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			entries, err := extractHostArchive(c, hostArchive(t, name, contents), name).Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}

	dir := hostArchive(t, "notes.txt", []byte(strings.Repeat("not an archive\n", 100)))
	_, err := extractHostArchive(c, dir, "notes.txt").Sync(ctx)
	require.ErrorContains(t, err, "not a zip or tar archive")
}

func (FileSuite) TestExtractLarge(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	big := make([]byte, 64<<20)
	_, err := rand.Read(big)
	require.NoError(t, err)
	entries := []extractTestEntry{{name: "big.bin", body: big}}
	const smallFiles = 5000
	for i := range smallFiles {
		entries = append(entries, extractTestEntry{
			name: fmt.Sprintf("src/d%02d/f%04d.go", i%50, i),
			body: []byte(fmt.Sprintf("package d%02d\n\nconst X%d = %d\n", i%50, i, i)),
		})
	}
	dir := hostArchive(t, "large.zip", extractTestZip(t, entries...))
	extracted := extractHostArchive(c, dir, "large.zip")

	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedDirectory("/x", extracted).
		WithExec([]string{"sh", "-c", "sha256sum /x/big.bin | cut -d' ' -f1; find /x/src -type f | wc -l; cat /x/src/d07/f4207.go"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x\n%d\npackage d07\n\nconst X4207 = 4207\n", sha256.Sum256(big), smallFiles), out)
}

// Archives with the same contents extract once: the second extraction is
// the first one's directory, as its files' change times show.
func (FileSuite) TestExtractCaching(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	archive := extractTestZip(t, extractTestEntry{name: "f", body: []byte(rand.Text())})
	first := extractHostArchive(c, hostArchive(t, "a.zip", archive), "a.zip")
	same := extractHostArchive(c, hostArchive(t, "a.zip", archive), "a.zip")
	other := extractHostArchive(c, hostArchive(t, "a.zip", extractTestZip(t, extractTestEntry{name: "f", body: []byte(rand.Text())})), "a.zip")
	for _, d := range []*core.Directory{first, same, other} {
		_, err := d.Sync(ctx)
		require.NoError(t, err)
	}
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedDirectory("/first", first).
		WithMountedDirectory("/same", same).
		WithMountedDirectory("/other", other).
		WithExec([]string{"stat", "-c", "%z", "/first/f", "/same/f", "/other/f"}).
		Stdout(ctx)
	require.NoError(t, err)
	ctimes := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, ctimes, 3)
	require.Equal(t, ctimes[0], ctimes[1], "the same archive is extracted once")
	require.NotEqual(t, ctimes[0], ctimes[2], "a different archive is extracted on its own")
}
