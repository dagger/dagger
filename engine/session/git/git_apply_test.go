package git

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type applyBundleFixture struct {
	repo, home, source, base, target, bundle string
	size                                     int64
}

func newApplyBundleFixture(t *testing.T, objectFormat ...string) applyBundleFixture {
	t.Helper()
	skipIfNoGit(t)
	repo, home := initRepo(t, "main")
	if len(objectFormat) > 0 {
		repo = t.TempDir()
		gitCmd(t, home, repo, "init", "-b", "main", "--object-format="+objectFormat[0])
	}
	commitFile(t, repo, home, "base.txt", "base\n", "base")
	commitFile(t, repo, home, "unrelated.txt", "original\n", "unrelated")
	base := gitCmd(t, home, repo, "rev-parse", "HEAD")
	source := t.TempDir()
	gitCmd(t, home, "", "clone", "--no-local", repo, source)
	commitFile(t, source, home, "incoming.txt", "incoming\n", "incoming")
	target := gitCmd(t, home, source, "rev-parse", "HEAD")
	bundle := filepath.Join(t.TempDir(), "export.bundle")
	gitCmd(t, home, source, "bundle", "create", "--version=3", bundle, "HEAD", "^"+base)
	st, err := os.Stat(bundle)
	require.NoError(t, err)
	return applyBundleFixture{repo: repo, home: home, source: source, base: base, target: target, bundle: bundle, size: st.Size()}
}

func (f applyBundleFixture) metadata(t *testing.T) *ApplyBundleMetadata {
	t.Helper()
	return &ApplyBundleMetadata{CheckoutPath: f.repo, TargetSha: f.target, ExpectedStateDigest: checkoutDigest(t, f.repo), BundleRef: "HEAD"}
}

func TestApplyBundleFastForward(t *testing.T) {
	f := newApplyBundleFixture(t)
	// Nonoverlapping staged and unstaged edits must survive unchanged.
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "unrelated.txt"), []byte("staged\n"), 0o644))
	gitCmd(t, f.home, f.repo, "add", "unrelated.txt")
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "unrelated.txt"), []byte("unstaged\n"), 0o644))
	status := gitCmd(t, f.home, f.repo, "status", "--porcelain=v1")
	index := gitCmd(t, f.home, f.repo, "show", ":unrelated.txt")
	resp := applyBundle(t.Context(), f.metadata(t), f.bundle, f.size)
	require.Nil(t, resp.Error)
	require.Equal(t, f.target, resp.HeadSha)
	require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
	require.Equal(t, "refs/heads/main", gitCmd(t, f.home, f.repo, "symbolic-ref", "HEAD"))
	require.Equal(t, status, gitCmd(t, f.home, f.repo, "status", "--porcelain=v1"))
	require.Equal(t, index, gitCmd(t, f.home, f.repo, "show", ":unrelated.txt"))
	require.Contains(t, gitCmd(t, f.home, f.repo, "reflog", "-1"), "dagger export")
	require.Empty(t, gitCmd(t, f.home, f.repo, "for-each-ref", "refs/dagger/checkpoints"))
	// Retry without a bundle still checks its lease but makes no new commit.
	resp = applyBundle(t.Context(), f.metadata(t), "", 0)
	require.Nil(t, resp.Error)
	require.Equal(t, f.target, resp.HeadSha)
}

func TestApplyBundleParksWithoutChangingCheckout(t *testing.T) {
	for _, scenario := range []string{"diverged", "behind", "unstaged", "staged", "untracked", "ignored", "lease", "branch-lease", "in-progress"} {
		t.Run(scenario, func(t *testing.T) {
			f := newApplyBundleFixture(t)
			metadata := f.metadata(t)
			switch scenario {
			case "diverged", "lease":
				commitFile(t, f.repo, f.home, "local.txt", "user work", "local")
			case "behind":
				gitCmd(t, f.home, f.repo, "fetch", f.source, "HEAD")
				gitCmd(t, f.home, f.repo, "merge", "--ff-only", f.target)
				commitFile(t, f.repo, f.home, "local.txt", "user work", "ahead")
			case "branch-lease":
				gitCmd(t, f.home, f.repo, "switch", "-c", "other")
			case "unstaged", "staged":
				// Make an incoming commit modify an existing tracked path.
				commitFile(t, f.source, f.home, "base.txt", "engine edit", "edit")
				f.target = gitCmd(t, f.home, f.source, "rev-parse", "HEAD")
				gitCmd(t, f.home, f.source, "bundle", "create", "--version=3", f.bundle, "HEAD", "^"+f.base)
				metadata.TargetSha = f.target
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "base.txt"), []byte("user edit"), 0o644))
				if scenario == "staged" {
					gitCmd(t, f.home, f.repo, "add", "base.txt")
				}
			case "untracked", "ignored":
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "incoming.txt"), []byte("user file"), 0o644))
				if scenario == "ignored" {
					require.NoError(t, os.WriteFile(filepath.Join(f.repo, ".git", "info", "exclude"), []byte("incoming.txt\n"), 0o644))
				}
			case "in-progress":
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, ".git", "CHERRY_PICK_HEAD"), []byte(f.base+"\n"), 0o644))
			}
			if scenario != "lease" && scenario != "branch-lease" {
				metadata.ExpectedStateDigest = checkoutDigest(t, f.repo)
			}
			head := gitCmd(t, f.home, f.repo, "rev-parse", "HEAD")
			status := gitCmd(t, f.home, f.repo, "status", "--porcelain=v1", "--ignored")
			index, err := os.ReadFile(filepath.Join(f.repo, ".git", "index"))
			require.NoError(t, err)
			resp := applyBundle(t.Context(), metadata, f.bundle, f.size)
			require.NotNil(t, resp.Error)
			require.Contains(t, resp.Error.Message, resp.ParkedRef)
			require.Equal(t, "refs/dagger/checkpoints/"+f.target[:12], resp.ParkedRef)
			require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", resp.ParkedRef))
			require.Equal(t, head, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
			afterIndex, err := os.ReadFile(filepath.Join(f.repo, ".git", "index"))
			require.NoError(t, err)
			require.Equal(t, index, afterIndex)
			require.Equal(t, status, gitCmd(t, f.home, f.repo, "status", "--porcelain=v1", "--ignored"))
		})
	}
}

func TestApplyBundleCheckoutLayouts(t *testing.T) {
	for _, layout := range []string{"worktree", "detached", "separate-git-dir"} {
		t.Run(layout, func(t *testing.T) {
			f := newApplyBundleFixture(t)
			switch layout {
			case "worktree":
				linked := filepath.Join(t.TempDir(), "linked")
				gitCmd(t, f.home, f.repo, "worktree", "add", "-b", "linked", linked, f.base)
				f.repo = linked
			case "detached":
				gitCmd(t, f.home, f.repo, "checkout", "--detach", f.base)
			case "separate-git-dir":
				gitCmd(t, f.home, f.repo, "init", "--separate-git-dir", filepath.Join(t.TempDir(), "metadata"))
			}
			resp := applyBundle(t.Context(), f.metadata(t), f.bundle, f.size)
			require.Nil(t, resp.Error)
			require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
		})
	}
}

func TestApplyBundleLeaseLocksBeforeWorktree(t *testing.T) {
	f := newApplyBundleFixture(t)
	gitCmd(t, f.home, f.repo, "fetch", f.source, "HEAD")
	state, err := collectCheckoutState(t.Context(), f.repo)
	require.NoError(t, err)
	commitFile(t, f.repo, f.home, "local.txt", "local", "moved")
	err = fastForwardExport(t.Context(), f.repo, state, f.target)
	require.ErrorContains(t, err, "git ref lease")
	_, err = os.Stat(filepath.Join(f.repo, "incoming.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	// A same-SHA branch switch also fails without modifying either branch.
	state, err = collectCheckoutState(t.Context(), f.repo)
	require.NoError(t, err)
	gitCmd(t, f.home, f.repo, "switch", "-c", "switched")
	err = fastForwardExport(t.Context(), f.repo, state, state.headSHA)
	require.ErrorContains(t, err, "checkout branch changed")
}

type applyBundleTestStream struct {
	grpc.ServerStream
	requests []*ApplyBundleRequest
	response *ApplyBundleResponse
}

func (s *applyBundleTestStream) Context() context.Context { return context.Background() }
func (s *applyBundleTestStream) Recv() (*ApplyBundleRequest, error) {
	if len(s.requests) == 0 {
		return nil, io.EOF
	}
	req := s.requests[0]
	s.requests = s.requests[1:]
	return req, nil
}
func (s *applyBundleTestStream) SendAndClose(resp *ApplyBundleResponse) error {
	s.response = resp
	return nil
}

func TestApplyBundleStream(t *testing.T) {
	f := newApplyBundleFixture(t)
	data, err := os.ReadFile(f.bundle)
	require.NoError(t, err)
	metadata := &ApplyBundleRequest{Msg: &ApplyBundleRequest_Metadata{Metadata: f.metadata(t)}}
	chunk := &ApplyBundleRequest{Msg: &ApplyBundleRequest_Chunk{Chunk: data}}
	for _, requests := range [][]*ApplyBundleRequest{nil, {chunk}, {metadata, metadata}, {{}}} {
		stream := &applyBundleTestStream{requests: requests}
		require.NoError(t, (GitAttachable{}).ApplyBundle(stream))
		require.NotNil(t, stream.response.Error)
	}
	stream := &applyBundleTestStream{requests: []*ApplyBundleRequest{metadata, chunk}}
	require.NoError(t, (GitAttachable{}).ApplyBundle(stream))
	require.Nil(t, stream.response.Error)
	require.Equal(t, f.target, stream.response.HeadSha)
}

func TestApplyBundleValidatesTargetAndPreservesHandoffs(t *testing.T) {
	f := newApplyBundleFixture(t)
	metadata := f.metadata(t)
	metadata.TargetSha = strings.Repeat("a", 40)
	resp := applyBundle(t.Context(), metadata, f.bundle, f.size)
	require.NotNil(t, resp.Error)
	require.Contains(t, resp.Error.Message, "bundle ref does not match")
	metadata.TargetSha = "--all"
	require.NotNil(t, applyBundle(t.Context(), metadata, f.bundle, f.size).Error)
	shortRef := "refs/dagger/checkpoints/" + f.target[:12]
	gitCmd(t, f.home, f.repo, "update-ref", shortRef, f.base)
	resp = applyBundle(t.Context(), f.metadata(t), f.bundle, f.size)
	require.Nil(t, resp.Error)
	require.Equal(t, f.base, gitCmd(t, f.home, f.repo, "rev-parse", shortRef))
}

func TestApplyBundleMergeAndFileKinds(t *testing.T) {
	f := newApplyBundleFixture(t, "sha256")
	gitCmd(t, f.home, f.source, "switch", "-c", "side", f.base)
	commitFile(t, f.source, f.home, "side.txt", "side", "side")
	gitCmd(t, f.home, f.source, "switch", "main")
	gitCmd(t, f.home, f.source, "merge", "--no-ff", "side", "-m", "merge")
	files := map[string][]byte{"binary.bin": {0, 1, 2, 255}, "executable": []byte("#!/bin/sh\nexit 0\n")}
	for name, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(f.source, name), data, 0o755))
	}
	require.NoError(t, os.Symlink("executable", filepath.Join(f.source, "link")))
	gitCmd(t, f.home, f.source, "rm", "base.txt")
	gitCmd(t, f.home, f.source, "add", ".")
	gitCmd(t, f.home, f.source, "commit", "-m", "file kinds")
	f.target = gitCmd(t, f.home, f.source, "rev-parse", "HEAD")
	require.Len(t, f.target, 64)
	gitCmd(t, f.home, f.source, "bundle", "create", "--version=3", f.bundle, "HEAD", "^"+f.base)
	// Repository hooks must not run during import, ref preparation or commit.
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, ".git", "hooks", "reference-transaction"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	resp := applyBundle(t.Context(), f.metadata(t), f.bundle, f.size)
	require.Nil(t, resp.Error)
	require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
	require.Equal(t, gitCmd(t, f.home, f.source, "log", "--format=%H %P"), gitCmd(t, f.home, f.repo, "log", "--format=%H %P"))
	for name, expected := range files {
		actual, err := os.ReadFile(filepath.Join(f.repo, name))
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	st, err := os.Stat(filepath.Join(f.repo, "executable"))
	require.NoError(t, err)
	require.NotZero(t, st.Mode().Perm()&0o111)
	link, err := os.Readlink(filepath.Join(f.repo, "link"))
	require.NoError(t, err)
	require.Equal(t, "executable", link)
	_, err = os.Stat(filepath.Join(f.repo, "base.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
