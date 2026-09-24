package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestIncrementalGitCheckout(t *testing.T) {
	// Umask is process-global; neither this test nor its children run in parallel.
	for _, mask := range []int{0022, 0000} {
		t.Run(fmt.Sprintf("umask%03o", mask), func(t *testing.T) {
			old := unix.Umask(mask)
			defer unix.Umask(old)
			ctx := context.Background()
			source := historyRepo(t, "sha1")
			write := func(p, data string, mode os.FileMode) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, p)), 0777))
				require.NoError(t, os.WriteFile(filepath.Join(source, p), []byte(data), mode))
			}
			write(".gitattributes", "*.txt text eol=crlf\n*.ident ident\n", 0666)
			write("keep/unchanged", "untouched", 0666)
			write("edit.txt", "before\n", 0666)
			write("file-to-dir", "old", 0666)
			write("dir-to-file/old", "old", 0666)
			write("delete/deep/file", "old", 0666)
			write("mode", "exec", 0666)
			write("identifier.ident", "$Id$\n", 0666)
			require.NoError(t, os.Symlink("keep", filepath.Join(source, "link-to-dir")))
			write("dir-to-link/deep/old", "old", 0666)
			write("keep/deep/untouched", "nested", 0666)
			write("dir-to-outside/deep/old", "old", 0666)
			outside := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(outside, "deep"), 0755))
			require.NoError(t, os.Chtimes(filepath.Join(outside, "deep"), time.Unix(42, 0), time.Unix(42, 0)))
			gitMirrorTestRun(t, source, "add", ".")
			gitMirrorTestRun(t, source, "commit", "-m", "base")
			parent := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
			dest := t.TempDir()
			cli := gitutil.NewGitCLI(gitutil.WithDir(source))
			require.NoError(t, doLocalGitTreeCheckout(ctx, cli, localTreeCheckoutCLI(dest), nil, source, &gitutil.Ref{SHA: parent}))
			unchanged, err := os.Stat(filepath.Join(dest, "keep/unchanged"))
			require.NoError(t, err)
			for _, p := range []string{"file-to-dir", "dir-to-file", "delete", "link-to-dir", "dir-to-link", "dir-to-outside"} {
				require.NoError(t, os.RemoveAll(filepath.Join(source, p)))
			}
			write("edit.txt", "after\n", 0666)
			write("file-to-dir/new/deep", "new", 0666)
			write("dir-to-file", "new", 0666)
			write("link-to-dir/new", "new", 0666)
			write("identifier.ident", "new $Id$\n", 0666)
			write("odd\n:\tname", "awkward", 0666)
			require.NoError(t, os.Symlink("keep", filepath.Join(source, "dir-to-link")))
			require.NoError(t, os.Symlink(outside, filepath.Join(source, "dir-to-outside")))
			require.NoError(t, os.Chmod(filepath.Join(source, "mode"), 0777))
			gitMirrorTestRun(t, source, "add", ".")
			gitMirrorTestRun(t, source, "commit", "-m", "child")
			child := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
			gitMirrorTestRun(t, source, "gc")
			packs, err := filepath.Glob(filepath.Join(source, ".git/objects/pack/*.pack"))
			require.NoError(t, err)
			require.NotEmpty(t, packs)
			// Source worktree/config/index are deliberately not checkout inputs.
			write("keep/unchanged", "DIRTY", 0666)
			write("untracked", "untracked", 0666)
			write(".gitattributes", "* -text\n", 0666)
			gitMirrorTestRun(t, source, "add", ".")
			for _, pack := range packs {
				require.NoError(t, os.Chtimes(pack, time.Unix(42, 0), time.Unix(42, 0)))
			}
			before := localTreeSnapshot(t, source)
			plan, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child)
			require.NoError(t, err)
			require.Empty(t, reason)
			require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, child, plan))
			outsideInfo, err := os.Stat(filepath.Join(outside, "deep"))
			require.NoError(t, err)
			require.Equal(t, int64(42), outsideInfo.ModTime().Unix(), "never normalize through a replacement symlink")
			rootInfo, err := os.Stat(dest)
			require.NoError(t, err)
			require.Equal(t, int64(1), rootInfo.ModTime().Unix())
			fresh := t.TempDir()
			require.NoError(t, doLocalGitTreeCheckout(ctx, cli, localTreeCheckoutCLI(fresh), nil, source, &gitutil.Ref{SHA: child}))
			require.Equal(t, localTreeSnapshot(t, fresh), localTreeSnapshot(t, dest))
			after, err := os.Stat(filepath.Join(dest, "keep/unchanged"))
			require.NoError(t, err)
			require.True(t, os.SameFile(unchanged, after), "unchanged file must not be rewritten")
			require.Equal(t, before, localTreeSnapshot(t, source))
			for _, pack := range packs {
				info, err := os.Stat(pack)
				require.NoError(t, err)
				require.Equal(t, int64(42), info.ModTime().Unix())
			}
			require.NoDirExists(t, filepath.Join(dest, ".git"))
			gitMirrorTestRun(t, source, "reset", "--hard", child)
			gitMirrorTestRun(t, source, "commit", "--allow-empty", "-m", "same tree")
			empty := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
			plan, reason, err = planIncrementalGitCheckout(ctx, cli, child, empty)
			require.NoError(t, err)
			require.Empty(t, reason)
			require.Empty(t, plan.changed)
			require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, empty, plan))
			require.Equal(t, localTreeSnapshot(t, fresh), localTreeSnapshot(t, dest))
		})
	}
}

func TestIncrementalGitCheckoutGates(t *testing.T) {
	ctx := context.Background()
	source := historyRepo(t, "sha1")
	parent := historyCommit(t, source, "file", "base")
	child := historyCommit(t, source, "file", "child")
	cli := gitutil.NewGitCLI(gitutil.WithDir(source))
	_, reason, err := planIncrementalGitCheckout(ctx, cli, child, parent)
	require.NoError(t, err)
	require.Equal(t, "parent-mismatch", reason)
	_, _, err = planIncrementalGitCheckout(ctx, cli, parent, strings.Repeat("a", 40))
	require.Error(t, err)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = planIncrementalGitCheckout(cancelled, cli, parent, child)
	require.ErrorIs(t, err, context.Canceled)
	for _, control := range []string{"nested/.gitattributes", ".gitmodules"} {
		gitMirrorTestRun(t, source, "reset", "--hard", parent)
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, control)), 0755))
		tip := historyCommit(t, source, control, "control")
		_, reason, err := planIncrementalGitCheckout(ctx, cli, parent, tip)
		require.NoError(t, err)
		require.Equal(t, "checkout-controls", reason)
	}
	gitMirrorTestRun(t, source, "reset", "--hard", parent)
	gitMirrorTestRun(t, source, "update-index", "--add", "--cacheinfo", "160000,"+parent+",sub")
	gitMirrorTestRun(t, source, "commit", "-m", "gitlink")
	tip := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	_, reason, err = planIncrementalGitCheckout(ctx, cli, parent, tip)
	require.NoError(t, err)
	require.Equal(t, "gitlinks", reason)
	gitMirrorTestRun(t, source, "commit", "--allow-empty", "-m", "unchanged gitlink")
	next := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	_, reason, err = planIncrementalGitCheckout(ctx, cli, tip, next)
	require.NoError(t, err)
	require.Equal(t, "gitlinks", reason)
}

func TestIncrementalGitCheckoutActualParent(t *testing.T) {
	ctx := context.Background()
	source := historyRepo(t, "sha1")
	parent := historyCommit(t, source, "file", "base")
	child := historyCommit(t, source, "file", "child")
	other := historyCommit(t, source, "file", "other")
	cli := gitutil.NewGitCLI(gitutil.WithDir(source))

	// Revision traversal accepts this rewritten parent; checkout provenance
	// must instead use the headers stored in the original commit object.
	require.NoError(t, os.WriteFile(filepath.Join(source, ".git/info/grafts"), []byte(child+" "+other+"\n"), 0600))
	require.Contains(t, gitMirrorTestRun(t, source, "rev-list", "--parents", "-n", "1", child), other)
	_, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child)
	require.NoError(t, err)
	require.Empty(t, reason)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, other, child)
	require.NoError(t, err)
	require.Equal(t, "parent-mismatch", reason)

	require.NoError(t, os.Remove(filepath.Join(source, ".git/info/grafts")))
	gitMirrorTestRun(t, source, "replace", child, other)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, parent, child)
	require.NoError(t, err)
	require.Empty(t, reason)
}

func TestIncrementalGitCheckoutProvenance(t *testing.T) {
	sha := strings.Repeat("a", 40)
	parent := dagql.ObjectResult[*GitRef]{} // no parent is never eligible
	repo := &LocalGitRepository{CheckoutBase: &GitCheckoutBase{Parent: parent, CommitSHA: sha}}
	ref := &LocalGitRef{Ref: &gitutil.Ref{SHA: sha}, repo: repo}
	require.False(t, ref.incrementalCheckoutEligible())
	repo.CheckoutBase = nil
	require.False(t, ref.incrementalCheckoutEligible())
}
