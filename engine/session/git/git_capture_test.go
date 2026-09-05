package git

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
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

func TestCaptureGitBuildsTwoRefBundleFromTemporaryObjects(t *testing.T) {
	skipIfNoGit(t)
	repo, home, remote := initCaptureRepo(t)
	base := gitCmd(t, home, repo, "rev-parse", "HEAD")
	commitFile(t, repo, home, "local-one.txt", "one\n", "local one")
	commitFile(t, repo, home, "local-two.txt", "two\n", "local two")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.ignored\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "base.txt"), []byte("dirty\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "safe.txt"), []byte("safe untracked\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "cache.ignored"), []byte("must not leave host\n"), 0o600))

	srv := captureGit(t, repo, &CaptureGitPolicy{Include: []string{".gitignore", "base.txt", "safe.txt"}})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	require.Equal(t, uint32(captureGitFormatVersion), meta.GetFormatVersion())
	require.Equal(t, base, meta.GetBaseSha())
	require.Equal(t, head, meta.GetHeadSha())
	require.Equal(t, "refs/heads/main", meta.GetRemoteRef())
	require.Equal(t, int32(2), meta.GetUntrackedFiles(), "ordinary nonignored files are selected")
	require.NotEmpty(t, meta.GetWorktreeSha())

	bundle := srv.payload(CAPTURE_CHUNK_BUNDLE)
	require.NotEmpty(t, bundle)
	require.True(t, bytes.HasPrefix(bundle, []byte("# v3 git bundle\n")))
	require.NotContains(t, string(bundle), "safe untracked", "bundle bytes are packed, not a patch stream")
	require.Empty(t, srv.payload(CAPTURE_CHUNK_UNKNOWN))

	bundlePath := filepath.Join(t.TempDir(), "capture.bundle")
	require.NoError(t, os.WriteFile(bundlePath, bundle, 0o600))
	gitCmd(t, home, repo, "bundle", "verify", bundlePath)
	heads := gitCmd(t, home, repo, "bundle", "list-heads", bundlePath)
	require.Contains(t, heads, head+" "+captureHeadRef)
	require.Contains(t, heads, meta.GetWorktreeSha()+" "+captureWorktreeRef)

	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", remote, clone)
	gitCmd(t, home, clone, "fetch", bundlePath,
		captureHeadRef+":"+captureHeadRef,
		captureWorktreeRef+":"+captureWorktreeRef)
	require.Equal(t, head, gitCmd(t, home, clone, "rev-parse", captureHeadRef))
	require.Equal(t, head, gitCmd(t, home, clone, "rev-parse", meta.GetWorktreeSha()+"^"))
	worktreeTree := gitCmd(t, home, clone, "rev-parse", meta.GetWorktreeSha()+"^{tree}")
	worktreeBlob := gitCmd(t, home, clone, "rev-parse", meta.GetWorktreeSha()+":safe.txt")
	gitCmd(t, home, clone, "checkout", "--detach", meta.GetWorktreeSha())
	require.FileExists(t, filepath.Join(clone, "safe.txt"))
	require.Equal(t, "dirty\n", string(mustReadFile(t, filepath.Join(clone, "base.txt"))))
	require.NoFileExists(t, filepath.Join(clone, "cache.ignored"))

	// S and every object created while staging it stay out of the user's object
	// database; only the temporary bundle carries them. The user's index and refs
	// are untouched as well.
	require.Error(t, gitErr(home, repo, "cat-file", "-e", meta.GetWorktreeSha()+"^{commit}"))
	require.Error(t, gitErr(home, repo, "cat-file", "-e", worktreeTree+"^{tree}"))
	require.Error(t, gitErr(home, repo, "cat-file", "-e", worktreeBlob+"^{blob}"))
	gitCmd(t, home, repo, "diff", "--cached", "--quiet")
	require.Empty(t, gitCmd(t, home, repo, "for-each-ref", "--format=%(refname)", "refs/dagger/checkpoint"))
}

func TestCaptureGitVerifiesThinPackWithoutCountingPrerequisiteDeltaBases(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)

	baseContent := strings.Repeat("a long line whose prior version is an efficient delta base\n", 4096)
	commitFile(t, repo, home, "history.txt", baseContent, "add history")
	gitCmd(t, home, repo, "push", "origin", "main")

	localContent := strings.Replace(baseContent, "prior version", "local version", 1)
	commitFile(t, repo, home, "history.txt", localContent, "edit history locally")

	srv := captureGit(t, repo, &CaptureGitPolicy{})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	bundle := srv.payload(CAPTURE_CHUNK_BUNDLE)
	require.NotEmpty(t, bundle)

	packOffset := bytes.Index(bundle, []byte("\n\n")) + 2
	require.GreaterOrEqual(t, len(bundle)-packOffset, 12)
	incomingCount := binary.BigEndian.Uint32(bundle[packOffset+8 : packOffset+12])
	repairRepo := t.TempDir()
	gitCmd(t, home, repairRepo, "init", "--bare")
	sourceObjects := gitCmd(t, home, repo, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	_, err := runHostGitBytes(context.Background(), repairRepo,
		[]string{"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + sourceObjects},
		bytes.NewReader(bundle[packOffset:]), "index-pack", "--stdin", "--fix-thin")
	require.NoError(t, err)
	packs, err := filepath.Glob(filepath.Join(repairRepo, "objects", "pack", "*.pack"))
	require.NoError(t, err)
	require.Len(t, packs, 1)
	repairedPack := mustReadFile(t, packs[0])
	require.GreaterOrEqual(t, len(repairedPack), 12)
	repairedCount := binary.BigEndian.Uint32(repairedPack[8:12])
	require.Greater(t, repairedCount, incomingCount,
		"fixture must force Git to append at least one prerequisite delta base")
}

func TestCaptureGitVerifiesCopiedTreeAlreadyReachableFromPrerequisite(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "f1", "f1"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "f3", "f3"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f1", "f1", "data.txt"), []byte("shared content\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f3", "f3", "data.txt"), []byte("other content\n"), 0o600))
	gitCmd(t, home, repo, "add", ".")
	gitCmd(t, home, repo, "commit", "-m", "initialize trees")
	gitCmd(t, home, repo, "push", "origin", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "f3", "f4"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f3", "f4", "data.txt"), []byte("shared content\n"), 0o600))

	srv := captureGit(t, repo, &CaptureGitPolicy{Include: []string{"f3/f4/**"}})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
}

func TestCaptureGitStagesApprovedBytesWithoutCleanFilters(t *testing.T) {
	skipIfNoGit(t)
	repo, home, remote := initCaptureRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("base.txt filter=leak\n"), 0o600))
	gitCmd(t, home, repo, "add", ".gitattributes")
	gitCmd(t, home, repo, "commit", "-m", "configure attributes")
	gitCmd(t, home, repo, "push", "origin", "main")

	secret := "ignored-secret-must-not-enter-bundle\n"
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("secret.ignored\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "secret.ignored"), []byte(secret), 0o600))
	gitCmd(t, home, repo, "config", "filter.leak.clean", "cat secret.ignored")
	gitCmd(t, home, repo, "config", "filter.leak.required", "true")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "base.txt"), []byte("approved raw bytes\n"), 0o600))

	srv := captureGit(t, repo, &CaptureGitPolicy{Include: []string{"base.txt"}})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	bundlePath := filepath.Join(t.TempDir(), "capture.bundle")
	require.NoError(t, os.WriteFile(bundlePath, srv.payload(CAPTURE_CHUNK_BUNDLE), 0o600))
	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", remote, clone)
	gitCmd(t, home, clone, "fetch", bundlePath, captureWorktreeRef+":"+captureWorktreeRef)
	gitCmd(t, home, clone, "checkout", "--detach", meta.GetWorktreeSha())
	require.Equal(t, "approved raw bytes\n", string(mustReadFile(t, filepath.Join(clone, "base.txt"))))
	require.NotContains(t, string(srv.payload(CAPTURE_CHUNK_BUNDLE)), secret)
}

func TestCaptureGitPreservesFileToDirectoryReplacement(t *testing.T) {
	skipIfNoGit(t)
	repo, home, remote := initCaptureRepo(t)
	commitFile(t, repo, home, "replaced", "old file\n", "add replaceable file")
	gitCmd(t, home, repo, "push", "origin", "main")
	require.NoError(t, os.Remove(filepath.Join(repo, "replaced")))
	require.NoError(t, os.Mkdir(filepath.Join(repo, "replaced"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "replaced", "child.txt"), []byte("new child\n"), 0o600))

	srv := captureGit(t, repo, &CaptureGitPolicy{Include: []string{"replaced", "replaced/**"}})
	meta := srv.metadata(t)
	require.Nil(t, meta.GetError())
	bundlePath := filepath.Join(t.TempDir(), "capture.bundle")
	require.NoError(t, os.WriteFile(bundlePath, srv.payload(CAPTURE_CHUNK_BUNDLE), 0o600))
	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", remote, clone)
	gitCmd(t, home, clone, "fetch", bundlePath, captureWorktreeRef+":"+captureWorktreeRef)
	gitCmd(t, home, clone, "checkout", "--detach", meta.GetWorktreeSha())
	require.Equal(t, "new child\n", string(mustReadFile(t, filepath.Join(clone, "replaced", "child.txt"))))
}

func TestCaptureGitRemoteHeadCleanOmitsBundleDirtyRestores(t *testing.T) {
	skipIfNoGit(t)
	repo, home, remote := initCaptureRepo(t)
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	clean := captureGit(t, repo, &CaptureGitPolicy{})
	require.Nil(t, clean.metadata(t).GetError())
	require.Equal(t, head, clean.metadata(t).GetBaseSha())
	require.Empty(t, clean.metadata(t).GetWorktreeSha())
	require.Zero(t, clean.metadata(t).GetBundleBytes())
	require.Empty(t, clean.payload(CAPTURE_CHUNK_BUNDLE))

	require.NoError(t, os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("approved dirt\n"), 0o600))
	dirty := captureGit(t, repo, &CaptureGitPolicy{Include: []string{"dirty.txt"}})
	meta := dirty.metadata(t)
	require.Nil(t, meta.GetError())
	require.Equal(t, head, meta.GetBaseSha())
	require.Equal(t, head, meta.GetHeadSha())
	require.NotEmpty(t, meta.GetWorktreeSha())
	bundle := dirty.payload(CAPTURE_CHUNK_BUNDLE)
	require.NotEmpty(t, bundle)

	bundlePath := filepath.Join(t.TempDir(), "dirty.bundle")
	require.NoError(t, os.WriteFile(bundlePath, bundle, 0o600))
	gitCmd(t, home, repo, "bundle", "verify", bundlePath)
	heads := gitCmd(t, home, repo, "bundle", "list-heads", bundlePath)
	require.Contains(t, heads, head+" "+captureHeadRef,
		"capture must restore Git's omitted L == R head advertisement")
	require.Contains(t, heads, meta.GetWorktreeSha()+" "+captureWorktreeRef)

	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", remote, clone)
	gitCmd(t, home, clone, "fetch", bundlePath,
		captureHeadRef+":"+captureHeadRef,
		captureWorktreeRef+":"+captureWorktreeRef)
	gitCmd(t, home, clone, "checkout", "--detach", meta.GetWorktreeSha())
	require.Equal(t, "approved dirt\n", string(mustReadFile(t, filepath.Join(clone, "dirty.txt"))))
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
}

// gitErr runs git for its exit status, for asserting that a lookup fails.
func gitErr(home, dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv(home)
	return cmd.Run()
}

func TestCaptureGitApprovalReportsCompleteSelectedDirtySet(t *testing.T) {
	skipIfNoGit(t)
	repo, _, _ := initCaptureRepo(t)
	secret := []byte("-----BEGIN PRIVATE KEY-----\ncanary-never-stream-on-preflight\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".env"), secret, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "safe.txt"), []byte("safe\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "base.txt"), []byte("tracked dirt\n"), 0o600))

	rejected := captureGit(t, repo, &CaptureGitPolicy{})
	meta := rejected.metadata(t)
	require.NotNil(t, meta.GetError())
	require.Len(t, rejected.responses, 1, "a rejected aggregate approval must not stream payload chunks")
	require.Equal(t, []string{".env", "base.txt", "safe.txt"}, []string{
		meta.ApprovalCandidates[0].GetPath(),
		meta.ApprovalCandidates[1].GetPath(),
		meta.ApprovalCandidates[2].GetPath(),
	})
	require.Equal(t, "credential-path", meta.ApprovalCandidates[0].GetClassification())
	require.Empty(t, meta.ApprovalCandidates[1].GetClassification())
	require.Empty(t, meta.ApprovalCandidates[2].GetClassification())
	for _, response := range rejected.responses {
		require.False(t, bytes.Contains(response.GetChunk().GetData(), secret))
	}

	approved := captureGit(t, repo, &CaptureGitPolicy{Include: []string{".env", "base.txt", "safe.txt"}})
	require.Nil(t, approved.metadata(t).GetError())
	require.NotEmpty(t, approved.payload(CAPTURE_CHUNK_BUNDLE))

	// Include selects untracked files; tracked changes are always captured.
	included := captureGit(t, repo, &CaptureGitPolicy{Include: []string{"*"}})
	require.Nil(t, included.metadata(t).GetError())
	require.Equal(t, int32(1), included.metadata(t).GetTrackedFiles())
	require.Equal(t, int32(2), included.metadata(t).GetUntrackedFiles())

	partial := captureGit(t, repo, &CaptureGitPolicy{Include: []string{"safe.txt"}})
	require.Nil(t, partial.metadata(t).GetError())
	require.Equal(t, int32(1), partial.metadata(t).GetTrackedFiles())
	require.Equal(t, int32(1), partial.metadata(t).GetUntrackedFiles())
}

func TestCaptureGitTrackedChangesNeedNoApproval(t *testing.T) {
	skipIfNoGit(t)
	repo, _, _ := initCaptureRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "base.txt"), []byte("tracked changes"), 0o600))
	captured := captureGit(t, repo, &CaptureGitPolicy{})
	require.Nil(t, captured.metadata(t).GetError())
	require.Equal(t, int32(1), captured.metadata(t).TrackedFiles)
	require.NotEmpty(t, captured.metadata(t).WorktreeSha)
}

func TestCaptureGitLocalFallbackStillRequiresApproval(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	gitCmd(t, home, repo, "remote", "remove", "origin")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "secret.txt"), []byte("private"), 0o600))
	rejected := captureGit(t, repo, &CaptureGitPolicy{})
	require.Equal(t, CAPTURE_REJECTED, rejected.metadata(t).GetError().GetType())
	require.Len(t, rejected.responses, 1)
	approved := captureGit(t, repo, &CaptureGitPolicy{Include: []string{"secret.txt"}})
	require.Nil(t, approved.metadata(t).GetError())
	require.Empty(t, approved.metadata(t).RemoteUrl)
	require.NotEmpty(t, approved.payload(CAPTURE_CHUNK_BUNDLE))
}

func TestCaptureGitApprovalTokenRejectsChangedReviewedBytes(t *testing.T) {
	skipIfNoGit(t)
	repo, _, _ := initCaptureRepo(t)
	const size = 64
	require.NoError(t, os.WriteFile(filepath.Join(repo, "reviewed.txt"), []byte(strings.Repeat("a", size)), 0o600))

	first := captureGit(t, repo, &CaptureGitPolicy{})
	firstMeta := first.metadata(t)
	require.Equal(t, CAPTURE_REJECTED, firstMeta.GetError().GetType())
	require.Len(t, firstMeta.GetApprovalCandidates(), 1)
	token := firstMeta.ApprovalCandidates[0].GetApprovalToken()
	require.NotEmpty(t, token)

	secret := "-----BEGIN PRIVATE KEY-----" + strings.Repeat("x", size-len("-----BEGIN PRIVATE KEY-----"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "reviewed.txt"), []byte(secret), 0o600))
	retried := captureGit(t, repo, &CaptureGitPolicy{ApprovalTokens: []string{token}})
	retriedMeta := retried.metadata(t)
	require.Equal(t, CAPTURE_REJECTED, retriedMeta.GetError().GetType())
	require.Len(t, retried.responses, 1, "changed reviewed bytes must not be streamed")
	require.Len(t, retriedMeta.GetApprovalCandidates(), 1)
	require.Equal(t, "credential-content", retriedMeta.ApprovalCandidates[0].GetClassification())
	require.NotEqual(t, token, retriedMeta.ApprovalCandidates[0].GetApprovalToken())
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
	require.Empty(t, srv.metadata(t).GetApprovalCandidates())
	require.NotEmpty(t, srv.payload(CAPTURE_CHUNK_BUNDLE))

	// The same bytes uncommitted still need approval: that is the state a
	// checkpoint carries off the host without anyone having committed it.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "loose-key.pem"), []byte(secret), 0o600))
	rejected := captureGit(t, repo, &CaptureGitPolicy{})
	meta := rejected.metadata(t)
	require.NotNil(t, meta.GetError())
	require.Len(t, meta.GetApprovalCandidates(), 1)
	require.Equal(t, "loose-key.pem", meta.ApprovalCandidates[0].GetPath())
	require.Equal(t, "credential-content", meta.ApprovalCandidates[0].GetClassification())
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
		require.Nil(t, srv.metadata(t).GetError())
		require.Empty(t, srv.metadata(t).RemoteUrl)
		require.Equal(t, srv.metadata(t).HeadSha, srv.metadata(t).BaseSha)
		require.NotEmpty(t, srv.metadata(t).CheckoutStateDigest)
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

func TestCaptureGitPreservesMergeInLocalHistory(t *testing.T) {
	skipIfNoGit(t)
	repo, home, _ := initCaptureRepo(t)
	gitCmd(t, home, repo, "checkout", "-b", "side")
	commitFile(t, repo, home, "side", "side", "side")
	gitCmd(t, home, repo, "checkout", "main")
	commitFile(t, repo, home, "main", "main", "main")
	gitCmd(t, home, repo, "merge", "--no-ff", "side", "-m", "merge")

	srv := captureGit(t, repo, &CaptureGitPolicy{})
	require.Nil(t, srv.metadata(t).GetError())
	require.Equal(t, gitCmd(t, home, repo, "rev-parse", "HEAD"), srv.metadata(t).HeadSha)
	require.NotEmpty(t, srv.payload(CAPTURE_CHUNK_BUNDLE))
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
