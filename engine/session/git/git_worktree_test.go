package git

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakePackWorktreeServer struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*PackWorktreeResponse
}

var _ Git_PackWorktreeServer = (*fakePackWorktreeServer)(nil)

func (s *fakePackWorktreeServer) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *fakePackWorktreeServer) Send(resp *PackWorktreeResponse) error {
	if chunk, ok := resp.Msg.(*PackWorktreeResponse_Chunk); ok {
		cp := append([]byte(nil), chunk.Chunk...)
		resp = &PackWorktreeResponse{Msg: &PackWorktreeResponse_Chunk{Chunk: cp}}
	}
	s.responses = append(s.responses, resp)
	return nil
}

func (s *fakePackWorktreeServer) metadata(t *testing.T) *PackWorktreeMetadata {
	t.Helper()
	require.NotEmpty(t, s.responses)
	meta := s.responses[0].GetMetadata()
	require.NotNil(t, meta, "first response must be metadata")
	return meta
}

func (s *fakePackWorktreeServer) patch() []byte {
	var patch []byte
	for _, resp := range s.responses {
		patch = append(patch, resp.GetChunk()...)
	}
	return patch
}

func packWorktree(t *testing.T, path, head string) *fakePackWorktreeServer {
	t.Helper()
	srv := &fakePackWorktreeServer{ctx: context.Background()}
	require.NoError(t, GitAttachable{}.PackWorktree(&PackWorktreeRequest{
		CheckoutPath:    path,
		ExpectedHeadSha: head,
	}, srv))
	return srv
}

func TestPackWorktreeGitVisibleDelta(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.ignored\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "modified.txt"), []byte("old\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("old staged\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "deleted.txt"), []byte("delete me\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "executable"), []byte("#!/bin/sh\nexit 0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{0, 1, 2, 3}, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "file-to-dir"), []byte("file\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "dir-to-file"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "dir-to-file", "child"), []byte("child\n"), 0o600))
	require.NoError(t, os.Symlink("modified.txt", filepath.Join(repo, "link")))
	gitCmd(t, home, repo, "add", ".")
	gitCmd(t, home, repo, "commit", "-m", "initial")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "modified.txt"), []byte("new\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("new staged\n"), 0o600))
	gitCmd(t, home, repo, "add", "staged.txt")
	require.NoError(t, os.Remove(filepath.Join(repo, "deleted.txt")))
	require.NoError(t, os.Chmod(filepath.Join(repo, "executable"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{0, 255, 1, 254, 2, 253}, 0o600))
	require.NoError(t, os.Remove(filepath.Join(repo, "file-to-dir")))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "file-to-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "file-to-dir", "child"), []byte("new child\n"), 0o600))
	require.NoError(t, os.RemoveAll(filepath.Join(repo, "dir-to-file")))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "dir-to-file"), []byte("new file\n"), 0o600))
	require.NoError(t, os.Remove(filepath.Join(repo, "link")))
	require.NoError(t, os.Symlink("staged.txt", filepath.Join(repo, "link")))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("ordinary\n"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "line\nbreak.txt"), []byte("nul safe path\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "skip.ignored"), []byte("ignored\n"), 0o600))

	nested := filepath.Join(repo, "nested")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	gitCmd(t, home, nested, "init")
	require.NoError(t, os.WriteFile(filepath.Join(nested, "inner.txt"), []byte("nested\n"), 0o600))

	srv := packWorktree(t, repo, head)
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	require.Equal(t, head, meta.GetHeadSha())
	require.Equal(t, []string{"nested"}, meta.GetNestedRepositories())
	patch := srv.patch()
	require.NotEmpty(t, patch)
	require.NotContains(t, string(patch), "skip.ignored")
	require.NotContains(t, string(patch), "inner.txt")

	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", "--no-local", repo, clone)
	patchPath := filepath.Join(t.TempDir(), "worktree.patch")
	require.NoError(t, os.WriteFile(patchPath, patch, 0o600))
	gitCmd(t, home, clone, "apply", "--binary", patchPath)

	got, err := os.ReadFile(filepath.Join(clone, "modified.txt"))
	require.NoError(t, err)
	require.Equal(t, "new\n", string(got))
	got, err = os.ReadFile(filepath.Join(clone, "staged.txt"))
	require.NoError(t, err)
	require.Equal(t, "new staged\n", string(got))
	_, err = os.Stat(filepath.Join(clone, "deleted.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	info, err := os.Stat(filepath.Join(clone, "executable"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&0o111)
	got, err = os.ReadFile(filepath.Join(clone, "binary.dat"))
	require.NoError(t, err)
	require.Equal(t, []byte{0, 255, 1, 254, 2, 253}, got)
	got, err = os.ReadFile(filepath.Join(clone, "file-to-dir", "child"))
	require.NoError(t, err)
	require.Equal(t, "new child\n", string(got))
	got, err = os.ReadFile(filepath.Join(clone, "dir-to-file"))
	require.NoError(t, err)
	require.Equal(t, "new file\n", string(got))
	target, err := os.Readlink(filepath.Join(clone, "link"))
	require.NoError(t, err)
	require.Equal(t, "staged.txt", target)
	got, err = os.ReadFile(filepath.Join(clone, "untracked.txt"))
	require.NoError(t, err)
	require.Equal(t, "ordinary\n", string(got))
	got, err = os.ReadFile(filepath.Join(clone, "line\nbreak.txt"))
	require.NoError(t, err)
	require.Equal(t, "nul safe path\n", string(got))
	require.NoFileExists(t, filepath.Join(clone, "skip.ignored"))
	require.NoDirExists(t, filepath.Join(clone, "nested"))
}

func TestPackWorktreeFallsBackForChangedSubmodule(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "initial")
	nested := filepath.Join(repo, "submodule")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	gitCmd(t, home, nested, "init", "-b", "main")
	commitFile(t, nested, home, "inner.txt", "old\n", "nested initial")
	nestedHead := gitCmd(t, home, nested, "rev-parse", "HEAD")
	gitCmd(t, home, repo, "update-index", "--add", "--cacheinfo", "160000", nestedHead, "submodule")
	gitCmd(t, home, repo, "commit", "-m", "add submodule")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(nested, "inner.txt"), []byte("dirty\n"), 0o600))
	srv := packWorktree(t, repo, head)
	require.Equal(t, WORKTREE_UNSUPPORTED, srv.metadata(t).GetError().GetType())
	require.Len(t, srv.responses, 1)
}

func TestPackWorktreeStreamsLargeBinaryAndRejectsMovedHead(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "initial")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	binary := make([]byte, 2*packCheckoutChunkSize)
	_, err := rand.Read(binary)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "large.bin"), binary, 0o600))
	srv := packWorktree(t, repo, head)
	require.Greater(t, len(srv.responses), 2, "large patch must be streamed across chunks")
	require.True(t, bytes.Contains(srv.patch(), []byte("large.bin")))

	mismatch := packWorktree(t, repo, "0000000000000000000000000000000000000000")
	require.Equal(t, HEAD_MISMATCH, mismatch.metadata(t).GetError().GetType())
	require.Len(t, mismatch.responses, 1)
}
