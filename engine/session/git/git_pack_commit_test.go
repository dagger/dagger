package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackCommitExactClosure(t *testing.T) {
	skipIfNoGit(t)
	for _, depth := range []int32{0, 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
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
			require.NoError(t, GitAttachable{}.PackCommit(&PackCommitRequest{CheckoutPath: repo, ExpectedStateDigest: state, CommitSha: sha, Depth: depth}, srv))
			require.Nil(t, srv.metadata(t).Error)
			require.Empty(t, srv.metadata(t).HeadRef)
			require.Equal(t, state, checkoutDigest(t, repo))
			dest := t.TempDir()
			gitCmd(t, home, dest, "init", "--bare")
			if depth == 1 {
				require.NoError(t, os.WriteFile(filepath.Join(dest, "shallow"), []byte(sha+"\n"), 0600))
			}
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
			if depth == 1 {
				require.NotContains(t, inventory, parent)
			} else {
				require.Contains(t, inventory, parent)
			}
		})
	}
}

func TestPackCommitUnavailable(t *testing.T) {
	skipIfNoGit(t)
	for _, mode := range []string{"moved", "missing", "shallow", "partial", "missing-parent", "corrupt-parent", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			repo, home := initRepo(t, "main")
			if mode == "missing-parent" || mode == "corrupt-parent" {
				commitFile(t, repo, home, "parent", "parent", "parent")
			}
			commitFile(t, repo, home, "file", "data", "tip")
			sha := gitCmd(t, home, repo, "rev-parse", "HEAD")
			state := checkoutDigest(t, repo)
			req := &PackCommitRequest{CheckoutPath: repo, ExpectedStateDigest: state, CommitSha: sha}
			srv := &fakePackCheckoutServer{}
			switch mode {
			case "moved":
				gitCmd(t, home, repo, "tag", "moved")
			case "missing":
				require.NoError(t, os.RemoveAll(repo))
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
			case "moved":
				require.Equal(t, CHECKOUT_STATE_MISMATCH, srv.metadata(t).Error.Type)
			case "corrupt-parent":
				require.Equal(t, PACK_FAILED, srv.metadata(t).Error.Type)
			default:
				require.Equal(t, HISTORY_UNAVAILABLE, srv.metadata(t).Error.Type)
			}
			require.Zero(t, srv.chunkCount())
		})
	}
}
