package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPackCommitExactClosure(t *testing.T) {
	skipIfNoGit(t)
	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "old", "old history", "old")
	parent := gitCmd(t, home, repo, "rev-parse", "HEAD")
	commitFile(t, repo, home, "file", "authorized", "tip")
	sha := gitCmd(t, home, repo, "rev-parse", "HEAD")
	gitCmd(t, home, repo, "checkout", "--orphan", "private")
	gitCmd(t, home, repo, "rm", "-rf", ".")
	commitFile(t, repo, home, "secret", "unrelated private object", "secret")
	secret := gitCmd(t, home, repo, "rev-parse", "HEAD:secret")
	replacement := gitCmd(t, home, repo, "rev-parse", "HEAD")
	gitCmd(t, home, repo, "tag", "private-tag")
	gitCmd(t, home, repo, "replace", sha, replacement)
	gitCmd(t, home, repo, "checkout", "main")
	state := checkoutDigest(t, repo)
	srv := &fakePackCheckoutServer{}
	require.NoError(t, GitAttachable{}.PackCommit(&PackCommitRequest{CheckoutPath: repo, CommitSha: sha}, srv))
	require.Nil(t, srv.metadata(t).Error)
	require.Empty(t, srv.metadata(t).HeadRef)
	require.Empty(t, srv.metadata(t).StateDigest)
	require.Equal(t, state, checkoutDigest(t, repo))
	dest := t.TempDir()
	gitCmd(t, home, dest, "init", "--bare")
	_, err := runHostGitBytes(t.Context(), dest, nil, strings.NewReader(string(srv.bundleBytes())), "index-pack", "--stdin", "--strict")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dest, "HEAD"), []byte(sha+"\n"), 0600))
	require.NoError(t, os.RemoveAll(repo))
	gitCmd(t, home, dest, "fsck", "--full", "--strict")
	require.Equal(t, "authorized", gitCmd(t, home, dest, "show", "HEAD:file"))
	inventory := gitCmd(t, home, dest, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
	require.NotContains(t, inventory, secret)
	require.NotContains(t, inventory, replacement)
	require.Empty(t, gitCmd(t, home, dest, "for-each-ref"))
	require.Contains(t, inventory, parent)
}

// The closure of a fixed SHA does not depend on the checkout's refs: a donor
// whose HEAD, branches and tags moved after capture still donates, and still
// only the captured commit's closure.
func TestPackCommitMovedCheckout(t *testing.T) {
	skipIfNoGit(t)
	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "file", "captured", "captured")
	sha := gitCmd(t, home, repo, "rev-parse", "HEAD")
	commitFile(t, repo, home, "file", "after capture", "after capture")
	after := gitCmd(t, home, repo, "rev-parse", "HEAD")
	afterBlob := gitCmd(t, home, repo, "rev-parse", "HEAD:file")
	gitCmd(t, home, repo, "tag", "moved")
	gitCmd(t, home, repo, "checkout", "-b", "feature")
	gitCmd(t, home, repo, "branch", "-D", "main")
	srv := &fakePackCheckoutServer{}
	require.NoError(t, GitAttachable{}.PackCommit(&PackCommitRequest{CheckoutPath: repo, CommitSha: sha}, srv))
	require.Nil(t, srv.metadata(t).Error)
	require.Equal(t, sha, srv.metadata(t).HeadSha)
	dest := t.TempDir()
	gitCmd(t, home, dest, "init", "--bare")
	_, err := runHostGitBytes(t.Context(), dest, nil, strings.NewReader(string(srv.bundleBytes())), "index-pack", "--stdin", "--strict")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dest, "HEAD"), []byte(sha+"\n"), 0600))
	gitCmd(t, home, dest, "fsck", "--full", "--strict")
	require.Equal(t, "captured", gitCmd(t, home, dest, "show", "HEAD:file"))
	inventory := gitCmd(t, home, dest, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
	require.NotContains(t, inventory, after)
	require.NotContains(t, inventory, afterBlob)
}

// Packing and streaming can take a long time; PackCommit must not hold the
// checkout lock, or CheckoutState on the same checkout times out meanwhile.
func TestPackCommitStreamsUnlocked(t *testing.T) {
	skipIfNoGit(t)
	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "file", "data", "tip")
	sha := gitCmd(t, home, repo, "rev-parse", "HEAD")
	sends := 0
	srv := &fakePackCheckoutServer{onSend: func(*PackCheckoutResponse) {
		sends++
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		unlock, err := gitCheckoutLocks.lock(ctx, filepath.Clean(repo))
		require.NoError(t, err, "checkout lock held while streaming")
		unlock()
	}}
	require.NoError(t, GitAttachable{}.PackCommit(&PackCommitRequest{CheckoutPath: repo, CommitSha: sha}, srv))
	require.Nil(t, srv.metadata(t).Error)
	require.Positive(t, srv.chunkCount())
	require.Equal(t, 1+srv.chunkCount(), sends)
}

func TestPackCommitUnavailable(t *testing.T) {
	skipIfNoGit(t)
	for _, mode := range []string{"missing", "unknown-commit", "shallow", "partial", "missing-parent", "corrupt-parent", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			repo, home := initRepo(t, "main")
			if mode == "missing-parent" || mode == "corrupt-parent" {
				commitFile(t, repo, home, "parent", "parent", "parent")
			}
			commitFile(t, repo, home, "file", "data", "tip")
			sha := gitCmd(t, home, repo, "rev-parse", "HEAD")
			req := &PackCommitRequest{CheckoutPath: repo, CommitSha: sha}
			srv := &fakePackCheckoutServer{}
			switch mode {
			case "missing":
				require.NoError(t, os.RemoveAll(repo))
			case "unknown-commit":
				req.CommitSha = strings.Repeat("e", 40)
			case "shallow":
				require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "shallow"), []byte(sha+"\n"), 0600))
			case "missing-parent":
				parent := gitCmd(t, home, repo, "rev-parse", "HEAD^")
				require.NoError(t, os.Remove(filepath.Join(repo, ".git", "objects", parent[:2], parent[2:])))
			case "corrupt-parent":
				parent := gitCmd(t, home, repo, "rev-parse", "HEAD^")
				require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "objects", parent[:2], parent[2:]), []byte("corrupt"), 0600))
			case "partial":
				blob := gitCmd(t, home, repo, "rev-parse", "HEAD:file")
				require.NoError(t, os.Remove(filepath.Join(repo, ".git", "objects", blob[:2], blob[2:])))
				gitCmd(t, home, repo, "config", "remote.origin.promisor", "true")
				gitCmd(t, home, repo, "config", "remote.origin.url", "http://127.0.0.1:1/never-fetch")
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				srv.ctx = ctx
			}
			err := (GitAttachable{}).PackCommit(req, srv)
			if mode == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, srv.metadata(t).Error)
			switch mode {
			case "partial", "missing-parent", "corrupt-parent":
				// Only pack-objects notices an incomplete or corrupt closure;
				// it fails without a pack, which the engine treats as a miss.
				require.Equal(t, PACK_FAILED, srv.metadata(t).Error.Type)
			default:
				require.Equal(t, HISTORY_UNAVAILABLE, srv.metadata(t).Error.Type)
			}
			require.Zero(t, srv.chunkCount())
		})
	}
}
