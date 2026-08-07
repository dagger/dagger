package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// These tests drive the CheckoutState and PackCheckout handlers against real
// local checkouts built with the host's git. They exercise the handlers'
// interaction with git directly rather than through the full session
// machinery, so PackCheckout is fed a fake server-streaming stream that just
// records what the handler sends.

// skipIfNoGit skips a test when git is not on PATH, matching how the handlers
// themselves degrade (they report NOT_FOUND) but keeping the suite green on
// machines without git.
func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping git handler tests")
	}
}

// gitEnv returns an isolated environment for invoking git: no system or user
// config leaks in (GIT_CONFIG_NOSYSTEM + a throwaway HOME), and a fixed author
// / committer identity so commits succeed on a bare CI account.
func gitEnv(home string) []string {
	return append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+home,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Dagger Test",
		"GIT_AUTHOR_EMAIL=test@dagger.io",
		"GIT_COMMITTER_NAME=Dagger Test",
		"GIT_COMMITTER_EMAIL=test@dagger.io",
	)
}

// gitCmd runs a git command with the isolated environment, failing the test on
// error. When dir is non-empty it is passed via -C; leave it empty for
// commands like clone/init that take the target as an argument.
func gitCmd(t *testing.T, home, dir string, args ...string) string {
	t.Helper()
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", full...)
	cmd.Env = gitEnv(home)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed: %s", strings.Join(full, " "), out)
	return strings.TrimSpace(string(out))
}

// initRepo creates a fresh repository in its own temp dir with its own HOME.
// A branch enables `git init -b <branch>`; an empty branch uses git's default.
func initRepo(t *testing.T, branch string) (repo, home string) {
	t.Helper()
	home = t.TempDir()
	repo = t.TempDir()
	if branch != "" {
		gitCmd(t, home, repo, "init", "-b", branch)
	} else {
		gitCmd(t, home, repo, "init")
	}
	return repo, home
}

// commitFile writes a file and commits it.
func commitFile(t *testing.T, repo, home, name, content, msg string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600))
	gitCmd(t, home, repo, "add", name)
	gitCmd(t, home, repo, "commit", "-m", msg)
}

// checkoutDigest calls CheckoutState and asserts a digest result, returning it.
func checkoutDigest(t *testing.T, path string) string {
	t.Helper()
	resp, err := GitAttachable{}.CheckoutState(context.Background(), &CheckoutStateRequest{CheckoutPath: path})
	require.NoError(t, err)
	if e := resp.GetError(); e != nil {
		t.Fatalf("unexpected error result: %s (%s)", e.GetType(), e.GetMessage())
	}
	digest := resp.GetStateDigest()
	require.NotEmpty(t, digest)
	return digest
}

func TestCheckoutStateNonRepo(t *testing.T) {
	skipIfNoGit(t)

	// A plain directory with no .git entry is the legitimate "not a git
	// checkout" state.
	dir := t.TempDir()
	resp, err := GitAttachable{}.CheckoutState(context.Background(), &CheckoutStateRequest{CheckoutPath: dir})
	require.NoError(t, err)
	require.Empty(t, resp.GetStateDigest())
	require.NotNil(t, resp.GetError())
	require.Equal(t, NOT_A_REPO, resp.GetError().GetType())
}

func TestCheckoutStateEmptyPath(t *testing.T) {
	skipIfNoGit(t)

	resp, err := GitAttachable{}.CheckoutState(context.Background(), &CheckoutStateRequest{CheckoutPath: ""})
	require.NoError(t, err)
	require.NotNil(t, resp.GetError())
	require.Equal(t, INVALID_REQUEST, resp.GetError().GetType())
}

func TestCheckoutStateDigestStableAndChanges(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")

	// A repository at rest reports the same digest every time it is read.
	first := checkoutDigest(t, repo)
	require.Equal(t, first, checkoutDigest(t, repo), "digest must be stable across reads")

	// Moving HEAD changes the digest.
	commitFile(t, repo, home, "b.txt", "two", "second")
	afterCommit := checkoutDigest(t, repo)
	require.NotEqual(t, first, afterCommit, "adding a commit must change the digest")

	// Creating a ref (a tag) changes the digest.
	gitCmd(t, home, repo, "tag", "v1")
	afterTag := checkoutDigest(t, repo)
	require.NotEqual(t, afterCommit, afterTag, "creating a tag must change the digest")

	// Losing the symbolic HEAD (detach) changes the digest.
	gitCmd(t, home, repo, "checkout", "--detach")
	afterDetach := checkoutDigest(t, repo)
	require.NotEqual(t, afterTag, afterDetach, "detaching HEAD must change the digest")
}

func TestCheckoutStateWorktree(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")

	// A linked worktree's .git is a pointer file, not a directory; host git
	// resolves it transparently.
	worktree := filepath.Join(t.TempDir(), "wt")
	gitCmd(t, home, repo, "worktree", "add", "-b", "feature", worktree)

	info, err := os.Lstat(filepath.Join(worktree, ".git"))
	require.NoError(t, err)
	require.False(t, info.IsDir(), "worktree .git should be a pointer file")

	before := checkoutDigest(t, worktree)

	// Committing in the worktree moves its branch, changing the digest.
	commitFile(t, worktree, home, "c.txt", "three", "in worktree")
	require.NotEqual(t, before, checkoutDigest(t, worktree), "worktree commit must change the digest")
}

func TestCheckoutStateUnbornRepo(t *testing.T) {
	skipIfNoGit(t)

	// A freshly initialized repository with no commits (unborn HEAD) is still
	// a readable repository and yields a digest.
	repo, _ := initRepo(t, "main")
	require.NotEmpty(t, checkoutDigest(t, repo))
}

// fakePackCheckoutServer is a stand-in for the generated Git_PackCheckoutServer
// server-streaming stream. It embeds grpc.ServerStream (so it satisfies the
// interface's method set) and overrides only Send and Context, which are the
// sole methods the PackCheckout handler touches. Mirrors the fake-stream
// pattern in engine/client/filesync_test.go.
type fakePackCheckoutServer struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*PackCheckoutResponse
}

var _ Git_PackCheckoutServer = (*fakePackCheckoutServer)(nil)

func (s *fakePackCheckoutServer) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *fakePackCheckoutServer) Send(resp *PackCheckoutResponse) error {
	// The handler streams every chunk out of a single reused read buffer,
	// exactly as a real gRPC stream tolerates (it serializes each message on
	// Send). Copy the chunk so our recorded slice does not get clobbered by
	// the next read.
	if chunk, ok := resp.Msg.(*PackCheckoutResponse_Chunk); ok {
		cp := make([]byte, len(chunk.Chunk))
		copy(cp, chunk.Chunk)
		resp = &PackCheckoutResponse{Msg: &PackCheckoutResponse_Chunk{Chunk: cp}}
	}
	s.responses = append(s.responses, resp)
	return nil
}

// metadata returns the first (metadata) message, asserting it is one.
func (s *fakePackCheckoutServer) metadata(t *testing.T) *PackCheckoutMetadata {
	t.Helper()
	require.NotEmpty(t, s.responses, "expected at least a metadata message")
	meta := s.responses[0].GetMetadata()
	require.NotNil(t, meta, "first message must be metadata")
	return meta
}

// chunkCount counts the streamed bundle chunk messages.
func (s *fakePackCheckoutServer) chunkCount() int {
	n := 0
	for _, resp := range s.responses {
		if resp.GetChunk() != nil {
			n++
		}
	}
	return n
}

// bundleBytes concatenates all streamed chunks into the bundle's bytes.
func (s *fakePackCheckoutServer) bundleBytes() []byte {
	var buf []byte
	for _, resp := range s.responses {
		if c := resp.GetChunk(); c != nil {
			buf = append(buf, c...)
		}
	}
	return buf
}

// writeBundle writes the streamed bundle bytes to a temp file and returns its
// path.
func (s *fakePackCheckoutServer) writeBundle(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checkout.bundle")
	require.NoError(t, os.WriteFile(path, s.bundleBytes(), 0o600))
	return path
}

func packCheckout(t *testing.T, path string) *fakePackCheckoutServer {
	t.Helper()
	srv := &fakePackCheckoutServer{ctx: context.Background()}
	require.NoError(t, GitAttachable{}.PackCheckout(&PackCheckoutRequest{CheckoutPath: path}, srv))
	return srv
}

func TestPackCheckoutPlainRepo(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	srv := packCheckout(t, repo)
	meta := srv.metadata(t)
	require.Equal(t, head, meta.GetHeadSha())
	require.Equal(t, "refs/heads/main", meta.GetHeadRef())
	require.Positive(t, srv.chunkCount(), "expected bundle chunks")

	bundlePath := srv.writeBundle(t)

	// The bundle is a valid git bundle: verify it from within a fresh repo.
	verifyRepo := filepath.Join(t.TempDir(), "verify")
	gitCmd(t, home, "", "init", verifyRepo)
	gitCmd(t, home, verifyRepo, "bundle", "verify", bundlePath)

	// Cloning the bundle reproduces a repo whose HEAD is the packed commit.
	cloneDir := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", bundlePath, cloneDir)
	require.Equal(t, meta.GetHeadSha(), gitCmd(t, home, cloneDir, "rev-parse", "HEAD"))
}

func TestPackCheckoutWorktree(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")

	worktree := filepath.Join(t.TempDir(), "wt")
	gitCmd(t, home, repo, "worktree", "add", "-b", "feature", worktree)
	commitFile(t, worktree, home, "c.txt", "three", "in worktree")
	wtHead := gitCmd(t, home, worktree, "rev-parse", "HEAD")

	srv := packCheckout(t, worktree)
	meta := srv.metadata(t)
	require.Equal(t, wtHead, meta.GetHeadSha())
	require.Equal(t, "refs/heads/feature", meta.GetHeadRef())
	require.Positive(t, srv.chunkCount())

	bundlePath := srv.writeBundle(t)
	cloneDir := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", bundlePath, cloneDir)
	require.Equal(t, wtHead, gitCmd(t, home, cloneDir, "rev-parse", "HEAD"))
}

func TestPackCheckoutDetachedHead(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")
	gitCmd(t, home, repo, "checkout", "--detach")

	srv := packCheckout(t, repo)
	meta := srv.metadata(t)
	require.Equal(t, head, meta.GetHeadSha())
	require.Empty(t, meta.GetHeadRef(), "detached HEAD has no symbolic ref")
	require.Positive(t, srv.chunkCount())

	// A bundle from a detached HEAD does not clone cleanly (no branch to check
	// out), so fetch HEAD into a fresh repo and confirm it resolves.
	bundlePath := srv.writeBundle(t)
	fetchDir := filepath.Join(t.TempDir(), "fetch")
	gitCmd(t, home, "", "init", fetchDir)
	gitCmd(t, home, fetchDir, "fetch", bundlePath, "HEAD")
	require.Equal(t, head, gitCmd(t, home, fetchDir, "rev-parse", "FETCH_HEAD"))
}

func TestPackCheckoutUnbornRepo(t *testing.T) {
	skipIfNoGit(t)

	// No commits yet: metadata names the unborn branch, carries no HEAD, and
	// no bundle chunks follow.
	repo, _ := initRepo(t, "main")

	srv := packCheckout(t, repo)
	meta := srv.metadata(t)
	require.Empty(t, meta.GetHeadSha())
	require.Equal(t, "refs/heads/main", meta.GetHeadRef())
	require.Zero(t, srv.chunkCount(), "an unborn repo has nothing to pack")
	require.Len(t, srv.responses, 1, "only the metadata message is sent")
}

func TestPackCheckoutNonRepo(t *testing.T) {
	skipIfNoGit(t)

	// A plain directory reports NOT_A_REPO as a single metadata message.
	dir := t.TempDir()
	srv := packCheckout(t, dir)
	require.Len(t, srv.responses, 1)
	meta := srv.metadata(t)
	require.NotNil(t, meta.GetError())
	require.Equal(t, NOT_A_REPO, meta.GetError().GetType())
}
