package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// A retained checkout without .git is the source-only tree a discarded
// checkout writes, whichever way the retained one was built, and a delta from
// its HEAD applied first gives a descendant's tree.
func TestRetainedCheckoutTree(t *testing.T) {
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
	write(".gitattributes", "*.txt text eol=crlf\n*.id ident\n", 0644)
	write("nested/.gitattributes", "*.txt -text\n", 0644)
	write("a.txt", "one\ntwo\n", 0644)
	write("nested/b.txt", "raw\n", 0644)
	write("nested/deep/file.id", "$Id$\n", 0644)
	write("run", "#!/bin/sh\n", 0755)
	require.NoError(t, os.Symlink("nested/b.txt", filepath.Join(source, "link")))
	parent := commit("parent")
	write("a.txt", "one\ntwo\nthree\n", 0644)
	commit("middle")
	write("added/new.txt", "new\n", 0644)
	require.NoError(t, os.Remove(filepath.Join(source, "run")))
	require.NoError(t, os.Chmod(filepath.Join(source, "nested/b.txt"), 0755))
	child := commit("child")
	gitMirrorTestRun(t, source, "tag", "v1", parent)
	cli := gitutil.NewGitCLI(gitutil.WithDir(source))

	discarded := func(sha string) (string, snapshotRootInfo) {
		dest := t.TempDir()
		root, err := readSnapshotRootInfo(dest)
		require.NoError(t, err)
		require.NoError(t, doLocalGitTreeCheckout(ctx, cli, localTreeCheckoutCLI(dest), nil, source, &gitutil.Ref{SHA: sha}))
		return dest, root
	}
	want, root := discarded(parent)
	wantChild, _ := discarded(child)
	retained := map[string]func(*gitutil.Ref) string{
		"fetched": func(ref *gitutil.Ref) string { return freshRetainedCheckout(t, source, ref) },
		"copy-on-write": func(ref *gitutil.Ref) string {
			dest := cowCopy(t, source)
			require.NoError(t, cowWipeCheckout(ctx, dest, dest, ref))
			return dest
		},
	}
	for name, checkout := range retained {
		for _, ref := range []*gitutil.Ref{{SHA: parent}, {Name: "refs/heads/main", SHA: parent}} {
			t.Run(name+"/"+ref.Name, func(t *testing.T) {
				full := checkout(ref)
				// A copy-on-write checkout inherits its repository's root.
				require.NoError(t, os.Chmod(full, 0751))
				head, err := retainedCheckoutHead(ctx, full)
				require.NoError(t, err)
				require.Equal(t, parent, head)
				require.NoError(t, finishRetainedCheckoutTree(full, root))
				require.Equal(t, localTreeSnapshot(t, want), localTreeSnapshot(t, full))

				// The objects come from the ref's own storage, not the checkout.
				full = checkout(ref)
				plan, reason, err := planIncrementalGitCheckout(ctx, cli, parent, child, false)
				require.NoError(t, err)
				require.Empty(t, reason)
				require.NoError(t, applyIncrementalGitCheckout(ctx, cli, full, child, plan))
				require.NoError(t, finishRetainedCheckoutTree(full, root))
				require.Equal(t, localTreeSnapshot(t, wantChild), localTreeSnapshot(t, full))
			})
		}
	}

	t.Run("refused", func(t *testing.T) {
		full := freshRetainedCheckout(t, source, &gitutil.Ref{SHA: parent})
		require.NoError(t, os.WriteFile(filepath.Join(full, ".gitmodules"), nil, 0644))
		_, err := retainedCheckoutHead(ctx, full)
		require.ErrorIs(t, err, errNativeCommitUnsupported)
		_, err = retainedCheckoutHead(ctx, t.TempDir())
		require.ErrorIs(t, err, errNativeCommitUnsupported)
	})
}
