package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const testBundleRef = "refs/heads/staged"

type applyBundleFixture struct {
	repo   string
	home   string
	base   string
	target string
	bundle []byte
}

func newApplyBundleFixture(t *testing.T, conflict bool) applyBundleFixture {
	t.Helper()
	repo, home := initRepo(t, "main")
	gitCmd(t, home, repo, "config", "user.name", "Dagger Test")
	gitCmd(t, home, repo, "config", "user.email", "test@dagger.io")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("base\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("clean\n"), 0o600))
	gitCmd(t, home, repo, "add", ".")
	gitCmd(t, home, repo, "commit", "-m", "base")
	base := gitCmd(t, home, repo, "rev-parse", "HEAD")

	gitCmd(t, home, repo, "checkout", "-b", "staged")
	if conflict {
		require.NoError(t, os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("staged\n"), 0o600))
	} else {
		require.NoError(t, os.WriteFile(filepath.Join(repo, "folded.txt"), []byte("folded\n"), 0o600))
	}
	gitCmd(t, home, repo, "add", ".")
	gitCmd(t, home, repo, "commit", "-m", "staged one")
	if !conflict {
		commitFile(t, repo, home, "second.txt", "second\n", "staged two")
	}
	target := gitCmd(t, home, repo, "rev-parse", "HEAD")

	bundlePath := filepath.Join(t.TempDir(), "staged.bundle")
	gitCmd(t, home, repo, "bundle", "create", bundlePath, testBundleRef)
	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	gitCmd(t, home, repo, "checkout", "main")

	return applyBundleFixture{repo: repo, home: home, base: base, target: target, bundle: bundle}
}

func (f applyBundleFixture) apply(t *testing.T) *ApplyBundleResponse {
	t.Helper()
	resp, err := applyBundle(context.Background(), &ApplyBundleMetadata{
		CheckoutPath:    f.repo,
		TargetSha:       f.target,
		ExpectedBaseSha: f.base,
		BundleRef:       testBundleRef,
	}, f.bundle)
	require.NoError(t, err)
	require.NotNil(t, resp)
	return resp
}

func TestApplyBundleFastForwardsUnchangedHead(t *testing.T) {
	skipIfNoGit(t)
	f := newApplyBundleFixture(t, false)

	resp := f.apply(t)
	require.Nil(t, resp.GetError())
	require.Equal(t, f.target, resp.GetApplied().GetHeadSha())
	require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
}

func TestApplyBundleReplaysOntoAdvancedHead(t *testing.T) {
	skipIfNoGit(t)
	f := newApplyBundleFixture(t, false)

	commitFile(t, f.repo, f.home, "local.txt", "local\n", "local advance")
	localHead := gitCmd(t, f.home, f.repo, "rev-parse", "HEAD")

	// The staged stack folded this pre-existing untracked path in. Saving it
	// should retain the existing matching-path behavior even after replay.
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "folded.txt"), []byte("folded\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "unrelated.txt"), []byte("keep dirty\n"), 0o600))

	resp := f.apply(t)
	require.Nil(t, resp.GetError())
	got := resp.GetApplied().GetHeadSha()
	require.NotEmpty(t, got)
	require.NotEqual(t, f.target, got, "replayed commits must report their rewritten tip")
	require.Equal(t, got, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
	require.Equal(t, localHead, gitCmd(t, f.home, f.repo, "rev-parse", got+"~2"))
	require.Equal(t, "staged one\nstaged two", gitCmd(t, f.home, f.repo, "log", "--reverse", "--format=%s", localHead+".."+got))
	require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", "staged"), "replay must not rewrite other refs")
	require.Equal(t, "?? unrelated.txt", gitCmd(t, f.home, f.repo, "status", "--porcelain", "--untracked-files=all"))
}

func TestApplyBundleReplaysUnbornStackOntoCreatedHead(t *testing.T) {
	skipIfNoGit(t)

	stagedRepo, stagedHome := initRepo(t, "staged")
	commitFile(t, stagedRepo, stagedHome, "staged-root.txt", "root\n", "staged root")
	commitFile(t, stagedRepo, stagedHome, "staged-second.txt", "second\n", "staged second")
	target := gitCmd(t, stagedHome, stagedRepo, "rev-parse", "HEAD")
	bundlePath := filepath.Join(t.TempDir(), "staged.bundle")
	gitCmd(t, stagedHome, stagedRepo, "bundle", "create", bundlePath, testBundleRef)
	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	checkout, home := initRepo(t, "main")
	gitCmd(t, home, checkout, "config", "user.name", "Dagger Test")
	gitCmd(t, home, checkout, "config", "user.email", "test@dagger.io")
	commitFile(t, checkout, home, "local-root.txt", "local\n", "local root")
	localHead := gitCmd(t, home, checkout, "rev-parse", "HEAD")

	resp, err := applyBundle(context.Background(), &ApplyBundleMetadata{
		CheckoutPath: checkout,
		TargetSha:    target,
		BundleRef:    testBundleRef,
	}, bundle)
	require.NoError(t, err)
	require.Nil(t, resp.GetError())
	got := resp.GetApplied().GetHeadSha()
	require.NotEmpty(t, got)
	require.NotEqual(t, target, got)
	require.Equal(t, got, gitCmd(t, home, checkout, "rev-parse", "HEAD"))
	require.Equal(t, localHead, gitCmd(t, home, checkout, "rev-parse", got+"~2"))
	require.Equal(t, "staged root\nstaged second", gitCmd(t, home, checkout, "log", "--reverse", "--format=%s", localHead+".."+got))
}

func TestApplyBundleReplayConflictLeavesCheckoutUntouched(t *testing.T) {
	skipIfNoGit(t)
	f := newApplyBundleFixture(t, true)

	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "shared.txt"), []byte("local\n"), 0o600))
	gitCmd(t, f.home, f.repo, "add", "shared.txt")
	gitCmd(t, f.home, f.repo, "commit", "-m", "local advance")
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "dirty.txt"), []byte("staged local dirt\n"), 0o600))
	gitCmd(t, f.home, f.repo, "add", "dirty.txt")
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "untracked.txt"), []byte("untracked\n"), 0o600))

	headBefore := gitCmd(t, f.home, f.repo, "rev-parse", "HEAD")
	statusBefore := gitCmd(t, f.home, f.repo, "status", "--porcelain", "--untracked-files=all")
	indexBefore := gitCmd(t, f.home, f.repo, "diff", "--cached", "--binary")
	worktreeBefore := gitCmd(t, f.home, f.repo, "worktree", "list", "--porcelain")

	resp := f.apply(t)
	require.NotNil(t, resp.GetError())
	require.Equal(t, BUNDLE_APPLY_FAILED, resp.GetError().GetType())
	require.Contains(t, resp.GetError().GetMessage(), "replay staged commits")
	require.Equal(t, headBefore, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
	require.Equal(t, statusBefore, gitCmd(t, f.home, f.repo, "status", "--porcelain", "--untracked-files=all"))
	require.Equal(t, indexBefore, gitCmd(t, f.home, f.repo, "diff", "--cached", "--binary"))
	require.Equal(t, worktreeBefore, gitCmd(t, f.home, f.repo, "worktree", "list", "--porcelain"))
	require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", "staged"))
}
