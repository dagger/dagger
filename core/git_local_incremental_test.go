package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
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
			// Names that must survive -z staging, never parsed as options or lists.
			for _, p := range []string{"-leading-dash", "comma,name", "ünïcödé/☃.txt"} {
				write(p, "old\n", 0666)
			}
			// A delta of hundreds of paths across many directories.
			bulk := func(i int) string { return fmt.Sprintf("bulk/%02d/file%03d", i%17, i) }
			for i := range 300 {
				write(bulk(i), "base", 0666)
			}
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
			for _, p := range []string{"-leading-dash", "comma,name", "ünïcödé/☃.txt", "-new/-x,y\n☃ z.txt"} {
				write(p, "new\n", 0666)
			}
			for i := range 300 {
				switch {
				case i%7 == 0:
					require.NoError(t, os.Remove(filepath.Join(source, bulk(i))))
				case i%5 == 0:
					require.NoError(t, os.Chmod(filepath.Join(source, bulk(i)), 0777))
				case i%3 == 0:
					write(bulk(i), "child", 0666)
				}
				write(fmt.Sprintf("bulk/new%02d/file%03d", i%13, i), "added", 0666)
			}
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
			plan, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child, false)
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
			plan, reason, err = planIncrementalGitCheckout(ctx, cli, child, empty, false)
			require.NoError(t, err)
			require.Empty(t, reason)
			require.Empty(t, plan.changed)
			require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, empty, plan))
			require.Equal(t, localTreeSnapshot(t, fresh), localTreeSnapshot(t, dest))

			// Only removals: nothing to stage or check out.
			gitMirrorTestRun(t, source, "rm", "-q", "--", "comma,name", bulk(1))
			gitMirrorTestRun(t, source, "commit", "-m", "removals")
			removals := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
			plan, reason, err = planIncrementalGitCheckout(ctx, cli, empty, removals, false)
			require.NoError(t, err)
			require.Empty(t, reason)
			require.Empty(t, plan.checkout)
			require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, removals, plan))
			fresh = t.TempDir()
			require.NoError(t, doLocalGitTreeCheckout(ctx, cli, localTreeCheckoutCLI(fresh), nil, source, &gitutil.Ref{SHA: removals}))
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
	_, reason, err := planIncrementalGitCheckout(ctx, cli, child, parent, false)
	require.NoError(t, err)
	require.Equal(t, "not-ancestor", reason)
	_, _, err = planIncrementalGitCheckout(ctx, cli, parent, strings.Repeat("a", 40), false)
	require.Error(t, err)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = planIncrementalGitCheckout(cancelled, cli, parent, child, false)
	require.ErrorIs(t, err, context.Canceled)
	for _, control := range []string{"nested/.gitattributes", ".gitmodules"} {
		gitMirrorTestRun(t, source, "reset", "--hard", parent)
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, control)), 0755))
		tip := historyCommit(t, source, control, "control")
		_, reason, err := planIncrementalGitCheckout(ctx, cli, parent, tip, false)
		require.NoError(t, err)
		require.Equal(t, "checkout-controls", reason)
	}
	_, _, err = parseIncrementalGitCheckoutPlan([]byte("M\x00file\x00"))
	require.ErrorContains(t, err, "invalid git tree diff entry")
	zero, blob := strings.Repeat("0", 40), strings.Repeat("1", 40)
	_, _, err = parseIncrementalGitCheckoutPlan([]byte(":000000 100644 " + zero + " " + blob[:39] + " A\x00file\x00"))
	require.ErrorContains(t, err, "invalid git tree diff entry")
	_, _, err = parseIncrementalGitCheckoutPlan([]byte(":000000 040000 " + zero + " " + blob + " A\x00file\x00"))
	require.ErrorContains(t, err, "unexpected git tree diff mode")
	plan, reason, err := parseIncrementalGitCheckoutPlan([]byte(":100644 100755 " + zero + " " + blob + " M\x00-x,\n\x00"))
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, []incrementalGitCheckoutEntry{{mode: "100755", sha: blob, path: "-x,\n"}}, plan.checkout)
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
	plan, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child, false)
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
			_, reason, err := planIncrementalGitCheckout(ctx, cli, child, tip, false)
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
			plan, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child, false)
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
	_, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child, false)
	require.NoError(t, err)
	require.Empty(t, reason)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, other, child, false)
	require.NoError(t, err)
	require.Equal(t, "not-ancestor", reason)

	require.NoError(t, os.Remove(filepath.Join(source, ".git/info/grafts")))
	gitMirrorTestRun(t, source, "replace", child, other)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, parent, child, false)
	require.NoError(t, err)
	require.Empty(t, reason)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, other, child, false)
	require.NoError(t, err)
	require.Equal(t, "not-ancestor", reason, "a replacement must not make a descendant an ancestor")
}

// A pull cherry-picks several commits on top of the receiver's HEAD, or
// fast-forwards it through merges: the retained base is then an ancestor, not
// the sole parent. Its delta must still produce exactly the full checkout.
func TestIncrementalGitCheckoutAncestor(t *testing.T) {
	ctx := context.Background()
	source := historyRepo(t, "sha1")
	write := func(p, data string, mode os.FileMode) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, p)), 0777))
		require.NoError(t, os.WriteFile(filepath.Join(source, p), []byte(data), mode))
	}
	commit := func(message string) string {
		gitMirrorTestRun(t, source, "add", "-A", ".")
		gitMirrorTestRun(t, source, "commit", "-q", "-m", message)
		return gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	}
	full := func(sha string) string {
		dest := t.TempDir()
		require.NoError(t, doLocalGitTreeCheckout(ctx, gitutil.NewGitCLI(gitutil.WithDir(source)), localTreeCheckoutCLI(dest), nil, source, &gitutil.Ref{SHA: sha}))
		return dest
	}
	write(".gitattributes", "*.txt text eol=crlf\n", 0644)
	write("keep/unchanged", "untouched", 0644)
	write("edit.txt", "base\n", 0644)
	write("mode", "exec", 0644)
	write("dir-to-link/deep/old", "old", 0644)
	write("removed/later", "old", 0644)
	require.NoError(t, os.Symlink("keep", filepath.Join(source, "link")))
	base := commit("base")

	// Several commits on top, a merged side branch, and changes that a later
	// commit reverts: only the net delta from base to tip may be written.
	write("edit.txt", "first\n", 0644)
	require.NoError(t, os.Chmod(filepath.Join(source, "mode"), 0755))
	write("transient", "gone again", 0644)
	first := commit("first")
	gitMirrorTestRun(t, source, "checkout", "-q", "-b", "side")
	write("side/new.txt", "side\n", 0644)
	require.NoError(t, os.Remove(filepath.Join(source, "link")))
	require.NoError(t, os.Symlink("edit.txt", filepath.Join(source, "link")))
	commit("side")
	gitMirrorTestRun(t, source, "checkout", "-q", "main")
	require.NoError(t, os.RemoveAll(filepath.Join(source, "dir-to-link")))
	require.NoError(t, os.Symlink("keep", filepath.Join(source, "dir-to-link")))
	require.NoError(t, os.Remove(filepath.Join(source, "transient")))
	commit("main")
	gitMirrorTestRun(t, source, "merge", "-q", "--no-ff", "-m", "merge", "side")
	write("edit.txt", "tip\n", 0644)
	require.NoError(t, os.RemoveAll(filepath.Join(source, "removed")))
	tip := commit("tip")

	cli := gitutil.NewGitCLI(gitutil.WithDir(source))
	for _, from := range []string{base, first} {
		dest := full(from)
		plan, reason, err := planIncrementalGitCheckout(ctx, cli, from, tip, false)
		require.NoError(t, err)
		require.Empty(t, reason)
		require.NotContains(t, plan.changed, "keep/unchanged")
		if from == base {
			require.NotContains(t, plan.changed, "transient", "added and removed again since the base")
		}
		require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, tip, plan))
		require.Equal(t, localTreeSnapshot(t, full(tip)), localTreeSnapshot(t, dest))
	}
	dest := full(base)
	unchanged, err := os.Stat(filepath.Join(dest, "keep/unchanged"))
	require.NoError(t, err)
	plan, _, err := planIncrementalGitCheckout(ctx, cli, base, tip, false)
	require.NoError(t, err)
	require.NoError(t, applyIncrementalGitCheckout(ctx, cli, dest, tip, plan))
	after, err := os.Stat(filepath.Join(dest, "keep/unchanged"))
	require.NoError(t, err)
	require.True(t, os.SameFile(unchanged, after), "unchanged file must not be rewritten")

	// The base itself: nothing to write.
	plan, reason, err := planIncrementalGitCheckout(ctx, cli, tip, tip, false)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Empty(t, plan.changed)

	// Descendants, siblings and unrelated commits are not bases.
	gitMirrorTestRun(t, source, "checkout", "-q", "--detach", base)
	sibling := historyCommit(t, source, "sibling", "sibling")
	gitMirrorTestRun(t, source, "checkout", "-q", "--orphan", "unrelated")
	unrelated := historyCommit(t, source, "unrelated", "unrelated")
	for _, from := range []string{sibling, unrelated} {
		_, reason, err := planIncrementalGitCheckout(ctx, cli, from, tip, false)
		require.NoError(t, err)
		require.Equal(t, "not-ancestor", reason)
	}
	_, reason, err = planIncrementalGitCheckout(ctx, cli, tip, base, false)
	require.NoError(t, err)
	require.Equal(t, "not-ancestor", reason)

	// Ancestry comes from the commit objects, never info/grafts: a graft can
	// neither forge an ancestor nor hide a real one.
	grafts := filepath.Join(source, ".git/info/grafts")
	require.NoError(t, os.MkdirAll(filepath.Dir(grafts), 0755))
	require.NoError(t, os.WriteFile(grafts, []byte(tip+" "+sibling+"\n"), 0600))
	require.Contains(t, gitMirrorTestRun(t, source, "rev-list", tip), sibling)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, sibling, tip, false)
	require.NoError(t, err)
	require.Equal(t, "not-ancestor", reason)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, base, tip, false)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.NoError(t, os.Remove(grafts))

	// Owned shallow storage: a base beyond the boundary is not an ancestor,
	// even when its objects happen to be present.
	require.NoError(t, os.WriteFile(filepath.Join(source, ".git/shallow"), []byte(first+"\n"), 0600))
	_, reason, err = planIncrementalGitCheckout(ctx, cli, base, tip, true)
	require.NoError(t, err)
	require.Equal(t, "not-ancestor", reason)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, first, tip, true)
	require.NoError(t, err)
	require.Empty(t, reason)
	_, reason, err = planIncrementalGitCheckout(ctx, cli, first, tip, false)
	require.NoError(t, err)
	require.Equal(t, "repository-layout", reason, "only owned storage may be shallow")
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

type coldChainTestLazy struct {
	LazyState
	run func(context.Context, *Directory) error
}

func (lazy *coldChainTestLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return dir.evaluateLazy(ctx, &lazy.LazyState, "test", func(ctx context.Context) error { return lazy.run(ctx, dir) })
}
func (*coldChainTestLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}
func (*coldChainTestLazy) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	return nil, errors.New("test lazy has no encoding")
}

// A cold history (a session resumed on an empty engine) must not walk every
// ancestor: requesting a tree may materialize its cold parent, but that parent
// may only build on an already materialized grandparent.
func TestIncrementalGitCheckoutColdChain(t *testing.T) {
	ctx, cache, srv := containerPersistenceTestCache(t, "", newContainerPersistenceTestSnapshots(), "chain")
	var (
		mu        sync.Mutex
		evaluated []int
		fulls     int
		deltas    int
	)
	// Each canonical tree stands in for LocalGitRef.Tree: a delta on its
	// parent when incrementalParentTree provides it, a full checkout otherwise.
	tree := func(i int, parent dagql.ObjectResult[*Directory]) dagql.ObjectResult[*Directory] {
		dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
		dir.Lazy = &coldChainTestLazy{LazyState: NewLazyState(), run: func(ctx context.Context, dir *Directory) error {
			mu.Lock()
			evaluated = append(evaluated, i)
			mu.Unlock()
			path := "/"
			full := true
			if parent.Self() != nil {
				_, parentPath, ok, err := incrementalParentTree(ctx, parent)
				if err != nil {
					return err
				}
				path, full = parentPath, !ok
			}
			mu.Lock()
			if full {
				fulls++
			} else {
				deltas++
			}
			mu.Unlock()
			dir.SetPath(path)
			id := fmt.Sprintf("tree%d", i)
			dir.SetSnapshot(&cacheVolumeTestImmutableRef{id: id, snapshotID: id})
			return nil
		}}
		return attachStoredSnapshotTestValue(t, ctx, cache, srv, "chain", fmt.Sprintf("tree%d", i), dir, false).(dagql.ObjectResult[*Directory])
	}
	const length = 100
	trees := make([]dagql.ObjectResult[*Directory], length)
	for i := range trees {
		var parent dagql.ObjectResult[*Directory]
		if i > 0 {
			parent = trees[i-1]
		}
		trees[i] = tree(i, parent)
	}
	requireRun := func(res dagql.ObjectResult[*Directory], wantFulls, wantDeltas int, wantEvaluated ...int) {
		t.Helper()
		mu.Lock()
		evaluated = nil
		mu.Unlock()
		require.NoError(t, cache.Evaluate(ctx, res))
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, wantEvaluated, evaluated)
		require.Equal(t, wantFulls, fulls)
		require.Equal(t, wantDeltas, deltas)
	}

	requireRun(trees[0], 1, 0, 0)
	// A cold parent on a materialized grandparent is still all deltas.
	requireRun(trees[2], 1, 2, 2, 1)
	// A cold chain costs one full checkout of the parent and one delta, not
	// a walk through every ancestor.
	requireRun(trees[length-1], 2, 3, length-1, length-2)
	for _, ancestor := range trees[3 : length-2] {
		require.True(t, dagql.HasPendingLazyComputation(ancestor), "cold ancestor materialized")
	}
	// Later commits are deltas off the materialized tip.
	requireRun(tree(length, trees[length-1]), 2, 4, length)
	// A restored snapshot is not cold: opening it is not a checkout.
	restored := attachStoredSnapshotTestValue(t, ctx, cache, srv, "chain", "restored", storedSnapshotTestValue("Directory", "saved", "/", true), false).(dagql.ObjectResult[*Directory])
	require.False(t, dagql.HasPendingLazyComputation(restored))
	requireRun(tree(length+2, tree(length+1, restored)), 2, 6, length+2, length+1)
}
