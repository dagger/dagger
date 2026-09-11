package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func integrationBundle(t *testing.T, f *applyBundleFixture, beforeTree, afterTree string) *ApplyBundleMetadata {
	t.Helper()
	before := gitCmd(t, f.home, f.source, "commit-tree", beforeTree, "-p", f.base, "-m", "before snapshot")
	after := gitCmd(t, f.home, f.source, "commit-tree", afterTree, "-p", f.target, "-p", before, "-m", "after snapshot")
	gitCmd(t, f.home, f.source, "update-ref", "refs/heads/transport", after)
	gitCmd(t, f.home, f.source, "bundle", "create", f.bundle, "refs/heads/transport", "^"+f.base)
	st, err := os.Stat(f.bundle)
	require.NoError(t, err)
	f.size = st.Size()
	meta := f.metadata(t)
	meta.BundleRef, meta.IntegrationWorktreeSha = "refs/heads/transport", after
	return meta
}

func TestApplyBundleIntegration(t *testing.T) {
	for _, scenario := range []string{"clean", "captured-dirt", "staged-dirt", "stale-file", "untracked-obstruction", "unrelated-staging", "pending-conflict", "index-locked", "sha256"} {
		t.Run(scenario, func(t *testing.T) {
			var format []string
			if scenario == "sha256" {
				format = []string{"sha256"}
			}
			f := newApplyBundleFixture(t, format...)
			beforeTree := gitCmd(t, f.home, f.source, "rev-parse", f.base+"^{tree}")
			if scenario == "captured-dirt" || scenario == "staged-dirt" {
				// The incoming commit incorporates the exact untracked file
				// already present in the captured worktree.
				beforeTree = gitCmd(t, f.home, f.source, "rev-parse", f.target+"^{tree}")
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "incoming.txt"), []byte("incoming\n"), 0o644))
				if scenario == "staged-dirt" {
					gitCmd(t, f.home, f.repo, "add", "incoming.txt")
				}
			}
			require.NoError(t, os.WriteFile(filepath.Join(f.source, "pending.txt"), []byte("pending\n"), 0o644))
			gitCmd(t, f.home, f.source, "add", "pending.txt")
			afterTree := gitCmd(t, f.home, f.source, "write-tree")
			meta := integrationBundle(t, &f, beforeTree, afterTree)
			switch scenario {
			case "index-locked":
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, ".git", "index.lock"), []byte("another writer"), 0o600))
			case "stale-file":
				// The planned commit changes a tracked path.
				commitFile(t, f.source, f.home, "base.txt", "incoming edit", "edit")
				f.target = gitCmd(t, f.home, f.source, "rev-parse", "HEAD")
				afterTree = gitCmd(t, f.home, f.source, "rev-parse", "HEAD^{tree}")
				meta = integrationBundle(t, &f, beforeTree, afterTree)
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "base.txt"), []byte("outside edit"), 0o644))
			case "untracked-obstruction":
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "incoming.txt"), []byte("outside"), 0o644))
			case "pending-conflict":
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "pending.txt"), []byte("outside"), 0o644))
			case "unrelated-staging":
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "unrelated.txt"), []byte("staged"), 0o644))
				gitCmd(t, f.home, f.repo, "add", "unrelated.txt")
				require.NoError(t, os.WriteFile(filepath.Join(f.repo, "unrelated.txt"), []byte("unstaged"), 0o644))
			}
			head := gitCmd(t, f.home, f.repo, "rev-parse", "HEAD")
			status := gitCmd(t, f.home, f.repo, "status", "--porcelain")
			resp := applyBundle(t.Context(), meta, f.bundle, f.size)
			if scenario == "stale-file" || scenario == "untracked-obstruction" || scenario == "pending-conflict" || scenario == "index-locked" {
				require.NotNil(t, resp.Error)
				require.Equal(t, head, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
				require.Equal(t, status, gitCmd(t, f.home, f.repo, "status", "--porcelain"))
				if scenario == "index-locked" {
					data, err := os.ReadFile(filepath.Join(f.repo, ".git", "index.lock"))
					require.NoError(t, err)
					require.Equal(t, "another writer", string(data))
				}
				return
			}
			require.Nil(t, resp.Error, "%+v", resp.Error)
			require.Equal(t, f.target, gitCmd(t, f.home, f.repo, "rev-parse", "HEAD"))
			require.Equal(t, "incoming", gitCmd(t, f.home, f.repo, "show", ":incoming.txt"))
			if scenario == "unrelated-staging" {
				require.Equal(t, "staged", gitCmd(t, f.home, f.repo, "show", ":unrelated.txt"))
				data, err := os.ReadFile(filepath.Join(f.repo, "unrelated.txt"))
				require.NoError(t, err)
				require.Equal(t, "unstaged", string(data))
			}
			data, err := os.ReadFile(filepath.Join(f.repo, "pending.txt"))
			require.NoError(t, err)
			require.Equal(t, "pending\n", string(data))
			require.Contains(t, gitCmd(t, f.home, f.repo, "status", "--porcelain"), "?? pending.txt")
		})
	}
}
