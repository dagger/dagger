package git

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeCaptureGitServer struct {
	grpc.ServerStream
	responses []*CaptureGitResponse
}

var _ Git_CaptureGitServer = (*fakeCaptureGitServer)(nil)

func (s *fakeCaptureGitServer) Context() context.Context { return context.Background() }
func (s *fakeCaptureGitServer) Send(resp *CaptureGitResponse) error {
	if chunk := resp.GetChunk(); chunk != nil {
		resp = &CaptureGitResponse{Msg: &CaptureGitResponse_Chunk{Chunk: &CaptureGitChunk{Kind: chunk.Kind, Data: append([]byte(nil), chunk.Data...)}}}
	}
	s.responses = append(s.responses, resp)
	return nil
}

func (s *fakeCaptureGitServer) metadata(t *testing.T) *CaptureGitMetadata {
	t.Helper()
	require.NotEmpty(t, s.responses)
	meta := s.responses[0].GetMetadata()
	require.NotNil(t, meta)
	return meta
}

func (s *fakeCaptureGitServer) payload(kind CaptureGitChunk_Kind) []byte {
	var result []byte
	for _, response := range s.responses {
		if chunk := response.GetChunk(); chunk != nil && chunk.Kind == kind {
			result = append(result, chunk.Data...)
		}
	}
	return result
}

func captureGit(t *testing.T, checkout string, policy *CaptureGitPolicy) *fakeCaptureGitServer {
	t.Helper()
	srv := new(fakeCaptureGitServer)
	require.NoError(t, GitAttachable{}.CaptureGit(&CaptureGitRequest{CheckoutPath: checkout, Policy: policy}, srv))
	return srv
}

func initCaptureRepo(t *testing.T) (repo, home, remote string) {
	t.Helper()
	home = t.TempDir()
	remote = filepath.Join(t.TempDir(), "remote.git")
	gitCmd(t, home, "", "init", "--bare", remote)
	repo = t.TempDir()
	gitCmd(t, home, repo, "init", "-b", "main")
	gitCmd(t, home, repo, "remote", "add", "origin", remote)
	commitFile(t, repo, home, "base.txt", "base\n", "base")
	gitCmd(t, home, repo, "push", "-u", "origin", "main")
	return repo, home, remote
}

func TestCaptureGitSeparatesPrerequisitesAndSafeWorktree(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	base := gitCmd(t, home, repo, "rev-parse", "HEAD")
	commitFile(t, repo, home, "local-one.txt", "one\n", "local one")
	commitFile(t, repo, home, "local-two.txt", "two\n", "local two")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.ignored\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "base.txt"), []byte("dirty\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "safe.txt"), []byte("safe untracked\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "cache.ignored"), []byte("must not leave host\n"), 0o600))

	srv := captureGit(t, repo, &CaptureGitPolicy{})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	require.Equal(t, uint32(captureGitFormatVersion), meta.GetFormatVersion())
	require.Equal(t, base, meta.GetBaseSha())
	require.Equal(t, head, meta.GetHeadSha())
	require.Equal(t, "refs/heads/main", meta.GetRemoteRef())
	require.Len(t, meta.GetCommits(), 2)
	require.Equal(t, []string{"local one", "local two"}, []string{meta.Commits[0].Message, meta.Commits[1].Message})
	require.Equal(t, int32(2), meta.GetUntrackedFiles(), "safe untracked files are auto-included")

	bundle := srv.payload(CAPTURE_CHUNK_PREREQUISITE_BUNDLE)
	patch := srv.payload(CAPTURE_CHUNK_WORKTREE_DELTA)
	require.NotEmpty(t, bundle)
	require.NotEmpty(t, patch)
	require.NotContains(t, string(patch), "cache.ignored")
	require.NotContains(t, string(bundle), "safe untracked")

	bundlePath := filepath.Join(t.TempDir(), "capture.bundle")
	require.NoError(t, os.WriteFile(bundlePath, bundle, 0o600))
	gitCmd(t, home, repo, "bundle", "verify", bundlePath)
	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", repo, clone)
	gitCmd(t, home, clone, "reset", "--hard", head)
	patchPath := filepath.Join(t.TempDir(), "capture.patch")
	require.NoError(t, os.WriteFile(patchPath, patch, 0o600))
	gitCmd(t, home, clone, "apply", "--binary", patchPath)
	require.FileExists(t, filepath.Join(clone, "safe.txt"))
	require.Equal(t, "dirty\n", string(mustReadFile(t, filepath.Join(clone, "base.txt"))))
	require.NoFileExists(t, filepath.Join(clone, "cache.ignored"))
}

func TestCaptureGitChoosesClosestAdvertisedAncestorAcrossRemotes(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	commitFile(t, repo, home, "near.txt", "near\n", "near")
	near := gitCmd(t, home, repo, "rev-parse", "HEAD")
	other := filepath.Join(t.TempDir(), "other.git")
	gitCmd(t, home, "", "init", "--bare", other)
	gitCmd(t, home, repo, "remote", "add", "other", other)
	gitCmd(t, home, repo, "push", "other", "main")
	commitFile(t, repo, home, "tip.txt", "tip\n", "tip")

	srv := captureGit(t, repo, &CaptureGitPolicy{})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	require.Equal(t, near, meta.GetBaseSha(), "preferred origin must not win with an older ancestor")
	require.Equal(t, "refs/heads/main", meta.GetRemoteRef())
	require.Equal(t, other, meta.GetRemoteUrl())
	require.Len(t, meta.GetCommits(), 1)
}

func TestCaptureGitSelectsBaseWithoutReachingForUnknownAdvertisedRefs(t *testing.T) {
	skipIfNoGit(t)
	repo, home, remote := initCaptureRepo(t)
	base := gitCmd(t, home, repo, "rev-parse", "HEAD")

	// Publish refs this checkout has never fetched. Base selection has to answer
	// from what the checkout already has: asking git about an advertised commit
	// it does not have is a network round trip, and a remote can advertise tens
	// of thousands of refs.
	publisher := filepath.Join(t.TempDir(), "publisher")
	gitCmd(t, home, "", "clone", remote, publisher)
	var unfetched []string
	for i := range 8 {
		branch := fmt.Sprintf("published-%02d", i)
		gitCmd(t, home, publisher, "checkout", "-q", "-b", branch, base)
		commitFile(t, publisher, home, branch+".txt", branch+"\n", branch)
		unfetched = append(unfetched, gitCmd(t, home, publisher, "rev-parse", "HEAD"))
		gitCmd(t, home, publisher, "push", "-q", "origin", branch)
	}

	commitFile(t, repo, home, "local.txt", "local\n", "local")

	srv := captureGit(t, repo, &CaptureGitPolicy{})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	require.Equal(t, base, meta.GetBaseSha())
	require.Equal(t, "refs/heads/main", meta.GetRemoteRef())
	require.Len(t, meta.GetCommits(), 1)

	for _, sha := range unfetched {
		require.Error(t, gitErr(home, repo, "cat-file", "-e", sha+"^{commit}"),
			"capture pulled an advertised commit into the checkout it was reading")
	}
	require.Error(t, gitErr(home, repo, "config", "--get", "extensions.partialClone"),
		"capture converted the checkout it was reading into a partial clone")
}

func TestCaptureGitSelectsBaseFromAdvertisedAnnotatedTag(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	commitFile(t, repo, home, "tagged.txt", "tagged\n", "tagged")
	tagged := gitCmd(t, home, repo, "rev-parse", "HEAD")
	gitCmd(t, home, repo, "tag", "-a", "v1", "-m", "v1")
	gitCmd(t, home, repo, "push", "origin", "v1")
	commitFile(t, repo, home, "tip.txt", "tip\n", "tip")

	srv := captureGit(t, repo, &CaptureGitPolicy{})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	// The tag names a nearer ancestor than the branch origin still advertises,
	// and it advertises the tag object rather than the commit it points at, so
	// selection has to peel it for history questions while recording the object
	// a later advertisement is compared against.
	require.Equal(t, tagged, meta.GetBaseSha())
	require.Equal(t, "refs/tags/v1", meta.GetRemoteRef())
	require.Len(t, meta.GetCommits(), 1)
}

// gitErr runs git for its exit status, for asserting that a lookup fails.
func gitErr(home, dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv(home)
	return cmd.Run()
}

func TestCaptureGitSuspiciousPreflightReturnsNoBytesUntilExactApproval(t *testing.T) {
	skipIfNoGit(t)
	repo, _, _ := initCaptureRepo(t)
	secret := []byte("-----BEGIN PRIVATE KEY-----\ncanary-never-stream-on-preflight\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".env"), secret, 0o600))

	rejected := captureGit(t, repo, &CaptureGitPolicy{})
	meta := rejected.metadata(t)
	require.NotNil(t, meta.GetError())
	require.Len(t, rejected.responses, 1, "a rejected preflight must not stream payload chunks")
	require.Len(t, meta.GetSuspiciousCandidates(), 1)
	require.Equal(t, ".env", meta.SuspiciousCandidates[0].GetPath())
	require.Equal(t, "credential-path", meta.SuspiciousCandidates[0].GetClassification())
	for _, response := range rejected.responses {
		require.False(t, bytes.Contains(response.GetChunk().GetData(), secret))
	}

	approved := captureGit(t, repo, &CaptureGitPolicy{ApproveSuspicious: []string{".env"}})
	require.Nil(t, approved.metadata(t).GetError())
	require.NotEmpty(t, approved.payload(CAPTURE_CHUNK_WORKTREE_DELTA))
}

func TestCaptureGitPreflightIgnoresCommittedContent(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	secret := "-----BEGIN PRIVATE KEY-----\ncanary-committed-on-purpose\n"
	commitFile(t, repo, home, "committed-key.pem", secret, "commit a key on purpose")

	// Committing is already a decision to record content in history, so the
	// preflight has nothing left to ask about it.
	srv := captureGit(t, repo, &CaptureGitPolicy{})
	require.Nil(t, srv.metadata(t).GetError())
	require.Empty(t, srv.metadata(t).GetSuspiciousCandidates())
	require.NotEmpty(t, srv.payload(CAPTURE_CHUNK_PREREQUISITE_BUNDLE))

	// The same bytes uncommitted still need approval: that is the state a
	// checkpoint carries off the host without anyone having committed it.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "loose-key.pem"), []byte(secret), 0o600))
	rejected := captureGit(t, repo, &CaptureGitPolicy{})
	meta := rejected.metadata(t)
	require.NotNil(t, meta.GetError())
	require.Len(t, meta.GetSuspiciousCandidates(), 1)
	require.Equal(t, "loose-key.pem", meta.SuspiciousCandidates[0].GetPath())
	require.Equal(t, "credential-content", meta.SuspiciousCandidates[0].GetClassification())
}

func TestCaptureGitRejectsUnsupportedAndUnboundedState(t *testing.T) {
	skipIfNoGit(t)
	t.Run("non repository", func(t *testing.T) {
		srv := captureGit(t, t.TempDir(), &CaptureGitPolicy{})
		require.Equal(t, NOT_A_REPO, srv.metadata(t).GetError().GetType())
	})
	t.Run("no remote ancestor", func(t *testing.T) {
		repo, _ := initRepo(t, "main")
		commitFile(t, repo, t.TempDir(), "a", "a", "a")
		srv := captureGit(t, repo, &CaptureGitPolicy{})
		require.Contains(t, srv.metadata(t).GetError().GetMessage(), "remote-backed ancestor")
	})
	t.Run("nested repository", func(t *testing.T) {
		repo, home, _ := initCaptureRepo(t)
		nested := filepath.Join(repo, "nested")
		require.NoError(t, os.Mkdir(nested, 0o700))
		gitCmd(t, home, nested, "init")
		srv := captureGit(t, repo, &CaptureGitPolicy{})
		require.Contains(t, srv.metadata(t).GetError().GetMessage(), "nested repository")
		require.Len(t, srv.responses, 1)
	})
	t.Run("committed bounds", func(t *testing.T) {
		repo, home, _ := initCaptureRepo(t)
		commitFile(t, repo, home, "large.txt", strings.Repeat("x", 32), "large committed file")
		srv := captureGit(t, repo, &CaptureGitPolicy{MaxTrackedFileBytes: 16})
		require.Contains(t, srv.metadata(t).GetError().GetMessage(), "committed content exceeds")
		require.Len(t, srv.responses, 1)
	})
	t.Run("untracked bounds", func(t *testing.T) {
		repo, _, _ := initCaptureRepo(t)
		data := make([]byte, 32)
		_, err := rand.Read(data)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(repo, "large.bin"), data, 0o600))
		srv := captureGit(t, repo, &CaptureGitPolicy{MaxUntrackedFileBytes: 16})
		require.Contains(t, srv.metadata(t).GetError().GetMessage(), "per-file bound")
		require.Len(t, srv.responses, 1)
	})
}

func TestCaptureGitRejectsMergeInLocalHistory(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	gitCmd(t, home, repo, "checkout", "-b", "side")
	commitFile(t, repo, home, "side", "side", "side")
	gitCmd(t, home, repo, "checkout", "main")
	commitFile(t, repo, home, "main", "main", "main")
	gitCmd(t, home, repo, "merge", "--no-ff", "side", "-m", "merge")

	srv := captureGit(t, repo, &CaptureGitPolicy{})
	require.Contains(t, srv.metadata(t).GetError().GetMessage(), "linear local history")
	require.Len(t, srv.responses, 1)
}

func TestSanitizeRemoteURL(t *testing.T) {
	require.Equal(t, "https://example.com/org/repo.git", sanitizeRemoteURL("https://user:password@example.com/org/repo.git?token=nope#fragment"))
	require.Equal(t, "ssh://git@example.com/org/repo.git", sanitizeRemoteURL("ssh://git:password@example.com/org/repo.git"))
	require.Equal(t, "git@example.com:org/repo.git", sanitizeRemoteURL("git@example.com:org/repo.git"))
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
