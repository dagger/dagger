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
	_, _, err = parseIncrementalGitCheckoutPlan([]byte("M\x00file\x00"))
	require.ErrorContains(t, err, "invalid git tree diff entry")
}

// A full checkout initializes submodules from the gitlinks and .gitmodules
// alone. Gitlinks the delta leaves alone are already in the parent's tree;
// any gitlink in the delta, even inside a replaced directory, falls back.
func TestIncrementalGitCheckoutGitlinks(t *testing.T) {
	ctx := context.Background()
	// Only these private fixtures allow local submodule URLs.
	allowFile := gitutil.WithArgs("-c", "protocol.file.allow=always")
	submodule := historyRepo(t, "sha1")
	pinned := historyCommit(t, submodule, "value", "pinned")
	later := historyCommit(t, submodule, "value", "later")
	source := historyRepo(t, "sha1")
	run := func(args ...string) string {
		return gitMirrorTestRun(t, source, append([]string{"-c", "protocol.file.allow=always"}, args...)...)
	}
	write := func(p, data string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, p)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(source, p), []byte(data), 0644))
	}
	run("submodule", "add", "file://"+submodule, "vendor/module")
	gitMirrorTestRun(t, filepath.Join(source, "vendor/module"), "checkout", pinned)
	write("file", "base")
	write("vendor/file", "base")
	run("add", ".")
	run("commit", "-m", "base")
	parent := run("rev-parse", "HEAD")
	cli := gitutil.NewGitCLI(gitutil.WithDir(source), allowFile)
	full := func(sha string) string {
		dest := t.TempDir()
		checkout := gitutil.NewGitCLI(gitutil.WithDir(dest), gitutil.WithWorkTree(dest), gitutil.WithGitDir(filepath.Join(dest, ".git")), allowFile)
		require.NoError(t, doLocalGitTreeCheckout(ctx, cli, checkout, nil, source, &gitutil.Ref{SHA: sha}))
		return dest
	}
	dest := full(parent)
	data, err := os.ReadFile(filepath.Join(dest, "vendor/module/value"))
	require.NoError(t, err)
	require.Equal(t, "pinned", string(data))
	submoduleFile, err := os.Stat(filepath.Join(dest, "vendor/module/value"))
	require.NoError(t, err)

	// Unchanged gitlinks, next to changed files and a file/directory replacement.
	write("file", "child")
	require.NoError(t, os.Remove(filepath.Join(source, "vendor/file")))
	write("vendor/file/inner", "replaced")
	write("vendor/new/deep", "new")
	run("add", "-A", "file", "vendor/file", "vendor/new")
	run("commit", "-m", "child")
	child := run("rev-parse", "HEAD")
	plan, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.ElementsMatch(t, []string{"file", "vendor/file", "vendor/file/inner", "vendor/new/deep"}, plan.changed)
	require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, child, plan))
	require.Equal(t, localTreeSnapshot(t, full(child)), localTreeSnapshot(t, dest))
	after, err := os.Stat(filepath.Join(dest, "vendor/module/value"))
	require.NoError(t, err)
	require.True(t, os.SameFile(submoduleFile, after), "unchanged submodule content must not be rewritten")

	blob := func(data string) string {
		tmp := filepath.Join(t.TempDir(), "blob")
		require.NoError(t, os.WriteFile(tmp, []byte(data), 0644))
		return run("hash-object", "-w", "--no-filters", tmp)
	}
	file := blob("file")
	for name, edit := range map[string][]string{
		"bumped":                   {"--cacheinfo", "160000," + later + ",vendor/module"},
		"added":                    {"--add", "--cacheinfo", "160000," + pinned + ",other"},
		"added within directory":   {"--add", "--cacheinfo", "160000," + pinned + ",added/dir/module"},
		"removed":                  {"--force-remove", "vendor/module"},
		"replaced by file":         {"--force-remove", "vendor/module", "--add", "--cacheinfo", "100644," + file + ",vendor/module"},
		"replaced by directory":    {"--force-remove", "vendor/module", "--add", "--cacheinfo", "100644," + file + ",vendor/module/file"},
		"file replaced by gitlink": {"--force-remove", "file", "--add", "--cacheinfo", "160000," + pinned + ",file"},
		// The directory holding the gitlink becomes a file: diff-tree -r
		// still lists the gitlink leaf under the removed directory.
		"within replaced directory": {"--force-remove", "vendor/module", "vendor/file/inner", "vendor/new/deep", "--add", "--cacheinfo", "100644," + file + ",vendor"},
	} {
		t.Run(name, func(t *testing.T) {
			run("read-tree", child)
			run(append([]string{"update-index"}, edit...)...)
			tip := run("commit-tree", "-p", child, "-m", name, run("write-tree"))
			_, reason, err := planIncrementalGitCheckout(ctx, cli, child, tip)
			require.NoError(t, err)
			require.Equal(t, "gitlink-change", reason)
		})
	}
}

// A .gitattributes that is itself converted on checkout differs from its blob
// in the parent tree. A full checkout reads attributes from the index; the
// delta must too, rather than parsing the converted worktree copy.
func TestIncrementalGitCheckoutConvertedAttributes(t *testing.T) {
	for _, attrs := range []string{
		"* text working-tree-encoding=UTF-16LE eol=lf\n",
		"* text eol=crlf\n",
	} {
		t.Run(attrs, func(t *testing.T) {
			ctx := context.Background()
			source := historyRepo(t, "sha1")
			stage := func(p, data string) {
				tmp := filepath.Join(t.TempDir(), "blob")
				require.NoError(t, os.WriteFile(tmp, []byte(data), 0644))
				blob := gitMirrorTestRun(t, source, "hash-object", "-w", "--no-filters", tmp)
				gitMirrorTestRun(t, source, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+p)
			}
			stage(".gitattributes", attrs)
			stage("a.txt", "one\n")
			gitMirrorTestRun(t, source, "commit", "-m", "base")
			parent := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
			stage("a.txt", "one\ntwo\n")
			stage("dir/b.txt", "new\n")
			gitMirrorTestRun(t, source, "commit", "-m", "child")
			child := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
			cli := gitutil.NewGitCLI(gitutil.WithDir(source))
			dest := t.TempDir()
			require.NoError(t, doLocalGitTreeCheckout(ctx, cli, localTreeCheckoutCLI(dest), nil, source, &gitutil.Ref{SHA: parent}))
			plan, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child)
			require.NoError(t, err)
			require.Empty(t, reason)
			require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, child, plan))
			fresh := t.TempDir()
			require.NoError(t, doLocalGitTreeCheckout(ctx, cli, localTreeCheckoutCLI(fresh), nil, source, &gitutil.Ref{SHA: child}))
			require.Equal(t, localTreeSnapshot(t, fresh), localTreeSnapshot(t, dest))
		})
	}
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
