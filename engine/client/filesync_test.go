package client

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestGlobHostPathCancellation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "match.txt"), []byte("x"), 0o600))

	opts := engine.LocalImportOpts{Path: root, GlobPattern: "**"}
	ctx := metadata.NewIncomingContext(context.Background(), opts.ToGRPCMD())
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	stream := &canceledDiffCopyStream{ctx: ctx}

	err := (FilesyncSource{}).DiffCopy(stream)
	require.ErrorIs(t, err, context.Canceled)
}

type canceledDiffCopyStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *canceledDiffCopyStream) Context() context.Context     { return s.ctx }
func (*canceledDiffCopyStream) Send(*fstypes.Packet) error     { return nil }
func (*canceledDiffCopyStream) Recv() (*fstypes.Packet, error) { return nil, io.EOF }
func (*canceledDiffCopyStream) SendMsg(any) error              { return nil }

var _ filesync.FileSync_DiffCopyServer = (*canceledDiffCopyStream)(nil)

// TestSearchHostPathNestedGitRepo covers the tree that host-side search walks:
// a nested git repository (a subdirectory with its own .git) must not become a
// hole in the search results, and a glob that happens to match nothing must
// come back empty rather than blowing up the whole search.
func TestSearchHostPathNestedGitRepo(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; host search falls back to grep")
	}

	root := t.TempDir()

	// outer repo
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outer.txt"), []byte("needle\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("needle\n"), 0o600))

	// nested repo: a subdirectory that is itself a git repository
	nested := filepath.Join(root, "nested")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "inner.txt"), []byte("needle\n"), 0o600))

	paths := func(t *testing.T, results []engine.LocalSearchResult) []string {
		t.Helper()
		var out []string
		for _, r := range results {
			out = append(out, r.FilePath)
		}
		return out
	}

	t.Run("nested repo files are searched", func(t *testing.T) {
		results, err := searchHostPath(context.Background(), root, &engine.LocalSearchOpts{
			Pattern: "needle",
		})
		require.NoError(t, err)
		require.Contains(t, paths(t, results), filepath.Join("nested", "inner.txt"))
	})

	t.Run("nested repo files are searched when honoring ignores", func(t *testing.T) {
		results, err := searchHostPath(context.Background(), root, &engine.LocalSearchOpts{
			Pattern:     "needle",
			SkipIgnored: true,
		})
		require.NoError(t, err)
		found := paths(t, results)
		require.Contains(t, found, filepath.Join("nested", "inner.txt"))
		require.Contains(t, found, "outer.txt")
		// the outer repo's own .gitignore is still honored
		require.NotContains(t, found, "ignored.txt")
	})

	t.Run("glob scoped to a nested repo file", func(t *testing.T) {
		results, err := searchHostPath(context.Background(), root, &engine.LocalSearchOpts{
			Pattern:     "needle",
			SkipIgnored: true,
			Globs:       []string{"nested/inner.txt"},
		})
		require.NoError(t, err)
		require.Equal(t, []string{filepath.Join("nested", "inner.txt")}, paths(t, results))
	})

	t.Run("glob matching nothing is empty, not an error", func(t *testing.T) {
		// ripgrep exits 2 with "No files were searched" here. That must not
		// fail the search: results from other trees (workspace overlays) get
		// merged with these, and may contain the file being looked for.
		results, err := searchHostPath(context.Background(), root, &engine.LocalSearchOpts{
			Pattern: "needle",
			Globs:   []string{"nested/does-not-exist.txt"},
		})
		require.NoError(t, err)
		require.Empty(t, results)
	})
}

// TestSearchWithRipgrepNoFilesSearched pins the fix for the case above without
// needing a real ripgrep: a stub that reproduces ripgrep's exit-2
// "No files were searched" diagnostic must be reported as an empty result,
// while any other stderr is still a real error.
func TestSearchWithRipgrepNoFilesSearched(t *testing.T) {
	stubRg := func(t *testing.T, script string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "rg")
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755))
		return path
	}

	t.Run("no files searched is empty", func(t *testing.T) {
		rg := stubRg(t, `echo "rg: No files were searched, which means ripgrep probably applied a filter you didn't expect." >&2
exit 2
`)
		results, err := searchWithRipgrep(context.Background(), t.TempDir(), rg, &engine.LocalSearchOpts{
			Pattern: "needle",
			Globs:   []string{"nope"},
		})
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("other failures still error", func(t *testing.T) {
		rg := stubRg(t, `echo "rg: something actually went wrong" >&2
exit 2
`)
		_, err := searchWithRipgrep(context.Background(), t.TempDir(), rg, &engine.LocalSearchOpts{
			Pattern: "needle",
		})
		require.ErrorContains(t, err, "something actually went wrong")
	})
}

// fileSendStream feeds a file stream export its chunks.
type fileSendStream struct {
	grpc.ServerStream
	ctx    context.Context
	chunks [][]byte
}

func (s *fileSendStream) Context() context.Context { return s.ctx }
func (s *fileSendStream) SendMsg(any) error        { return nil }
func (s *fileSendStream) RecvMsg(m any) error {
	if len(s.chunks) == 0 {
		return io.EOF
	}
	m.(*filesync.BytesMessage).Data = s.chunks[0]
	s.chunks = s.chunks[1:]
	return nil
}
func (s *fileSendStream) Send(*filesync.BytesMessage) error { return nil }
func (s *fileSendStream) Recv() (*filesync.BytesMessage, error) {
	msg := &filesync.BytesMessage{}
	if err := s.RecvMsg(msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func exportFileStream(t *testing.T, opts engine.LocalExportOpts, chunks ...string) {
	t.Helper()
	fs, err := NewFilesyncer()
	require.NoError(t, err)
	stream := &fileSendStream{ctx: metadata.NewIncomingContext(t.Context(), opts.ToGRPCMD())}
	for _, c := range chunks {
		stream.chunks = append(stream.chunks, []byte(c))
	}
	require.NoError(t, fs.AsTarget().DiffCopy(stream))
}

// A file stream export with ReplaceAtomically renames a complete file into
// place: a reader holding the old file keeps reading it whole, and no
// temporary file is left behind. Without it the file is rewritten in place.
func TestFileStreamExportReplacesAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "credentials.json")
	require.NoError(t, os.WriteFile(dest, []byte(`{"old":"token"}`), 0o600))
	held, err := os.Open(dest)
	require.NoError(t, err)
	defer held.Close()

	exportFileStream(t, engine.LocalExportOpts{Path: dest, IsFileStream: true, FileMode: 0o600, ReplaceAtomically: true},
		`{"new":`, `"token"}`)
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, `{"new":"token"}`, string(got))
	old, err := io.ReadAll(held)
	require.NoError(t, err)
	require.Equal(t, `{"old":"token"}`, string(old), "the old file was replaced, not rewritten")
	info, err := os.Stat(dest)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file left behind")

	inPlace, err := os.Open(dest)
	require.NoError(t, err)
	defer inPlace.Close()
	exportFileStream(t, engine.LocalExportOpts{Path: dest, IsFileStream: true, FileMode: 0o600}, `{"third":1}`)
	rewritten, err := io.ReadAll(inPlace)
	require.NoError(t, err)
	require.Equal(t, `{"third":1}`, string(rewritten), "an ordinary export rewrites the file in place")
}
