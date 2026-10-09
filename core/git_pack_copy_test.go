package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// gitPackTestMirror is a warm, complete mirror of a source with followable
// and unfollowable tags of every kind, as the engine leaves it after
// fetching a ref and its tags.
func gitPackTestMirror(t *testing.T) (source, mirror string, git *gitutil.GitCLI, base, next string) {
	t.Helper()
	source, base, next = gitMirrorTestSource(t)
	gitBundleTestRun(t, source, "tag", "-a", "v-base", "-m", "annotated base", base)
	gitBundleTestRun(t, source, "tag", "-a", "v-tree", "-m", "annotated tree", base+"^{tree}")
	gitBundleTestRun(t, source, "tag", "-a", "v-next", "-m", "annotated next", next)
	gitBundleTestRun(t, source, "tag", "l-next", next)
	orphan := gitBundleTestRun(t, source, "commit-tree", base+"^{tree}", "-m", "orphan")
	gitBundleTestRun(t, source, "tag", "-a", "v-orphan", "-m", "unrelated", orphan)
	repo, git, mirror := gitMirrorTestRepo(t, source)
	refs, err := gitRefsToFetch(t.Context(), git, 0, []*RemoteGitRef{{Ref: &gitutil.Ref{SHA: next}}})
	require.NoError(t, err)
	require.NoError(t, repo.fetchObjects(t.Context(), git, 0, true, refs))
	return source, mirror, git, base, next
}

type gitPackTestCheckout struct {
	dir    string
	git    *gitutil.GitCLI
	method string
}

func gitPackTestCheckoutFrom(t *testing.T, mirror string, mirrorGit *gitutil.GitCLI, ref *gitutil.Ref, pack bool, opts ...gitutil.Option) gitPackTestCheckout {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	git := gitutil.NewGitCLI(append([]gitutil.Option{gitutil.WithWorkTree(dir), gitutil.WithGitDir(gitDir)}, opts...)...)
	var tmpref, method string
	var err error
	if pack {
		tmpref, method, err = copyGitCheckout(ctx, git, mirrorGit, gitDir, ref, 0)
	} else {
		tmpref, err = fetchGitCheckout(ctx, git, "file://"+mirror, ref, 0)
		method = "fetch"
	}
	require.NoError(t, err)
	remotes := []GitRemote{{Name: "origin", URL: "https://example.com/repo", Implicit: true}}
	require.NoError(t, finishGitCheckout(ctx, git, remotes, "https://example.com/repo", ref, false, tmpref, gitCheckoutFresh))
	return gitPackTestCheckout{dir: dir, git: gitutil.NewGitCLI(gitutil.WithWorkTree(dir), gitutil.WithGitDir(gitDir)), method: method}
}

// state is everything about a checkout a caller can observe: refs (followed
// tags included), HEAD, configuration, the exact object inventory, and a
// clean worktree. fsck must pass unless the checkout is known to be broken.
func (c gitPackTestCheckout) state(t *testing.T) map[string]string {
	t.Helper()
	_, err := runGitEnv(t.Context(), c.dir, "fsck", "--full", "--strict", "--no-dangling")
	require.NoError(t, err)
	return c.stateNoFsck(t)
}

func (c gitPackTestCheckout) stateNoFsck(t *testing.T) map[string]string {
	t.Helper()
	ctx := t.Context()
	run := func(args ...string) string {
		out, err := c.git.Run(ctx, args...)
		require.NoError(t, err, "git %v", args)
		return strings.TrimSpace(string(out))
	}
	objects := strings.Split(run("cat-file", "--batch-all-objects", "--batch-check=%(objectname) %(objecttype)"), "\n")
	sort.Strings(objects)
	config := strings.Split(run("config", "--local", "--list"), "\n")
	sort.Strings(config)
	return map[string]string{
		"refs":    run("for-each-ref", "--format=%(refname) %(objectname)"),
		"head":    run("rev-parse", "--symbolic-full-name", "HEAD") + " " + run("rev-parse", "HEAD"),
		"config":  strings.Join(config, "\n"),
		"objects": strings.Join(objects, "\n"),
		"status":  run("status", "--porcelain"),
		"shallow": run("rev-parse", "--is-shallow-repository"),
	}
}

func gitPackTestPacks(t *testing.T, dir string) []string {
	t.Helper()
	packs, err := filepath.Glob(filepath.Join(dir, ".git", "objects", "pack", "*.pack"))
	require.NoError(t, err)
	return packs
}

// The pack-objects copy must produce exactly the checkout the fetch does,
// including the tags the fetch follows automatically.
func TestGitCheckoutPackMatchesFetch(t *testing.T) {
	_, mirror, mirrorGit, base, _ := gitPackTestMirror(t)
	for _, name := range []string{"refs/heads/main", ""} {
		t.Run("ref="+name, func(t *testing.T) {
			ref := &gitutil.Ref{Name: name, SHA: base}
			fetched := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, false)
			packed := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, true)
			require.Equal(t, "pack", packed.method)
			want := fetched.state(t)
			require.Equal(t, want, packed.state(t))
			// Tags into base's history (including one of its tree) follow;
			// tags of later or unrelated history do not.
			for _, tag := range []string{"base", "v-base", "v-tree"} {
				require.Contains(t, want["refs"], "refs/tags/"+tag+" ")
			}
			for _, tag := range []string{"v-next", "l-next", "v-orphan"} {
				require.NotContains(t, want["refs"], "refs/tags/"+tag+" ")
			}
			require.Equal(t, "false", want["shallow"])
			require.NoFileExists(t, filepath.Join(packed.dir, ".git", "objects", "info", "alternates"))
		})
	}
}

func TestGitCheckoutPackFallsBackToFetch(t *testing.T) {
	t.Run("pack failure", func(t *testing.T) {
		_, mirror, mirrorGit, base, _ := gitPackTestMirror(t)
		ref := &gitutil.Ref{Name: "refs/heads/main", SHA: base}
		// pack-objects writes its pack, then fails: the partial copy must be
		// discarded, not mixed into the fetched one.
		failPack := gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
			err := cmd.Run()
			if slices.Contains(cmd.Args, "pack-objects") {
				return errors.New("injected pack failure")
			}
			return err
		})
		packed := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, true, failPack)
		require.Equal(t, "fetch", packed.method)
		fetched := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, false)
		// This small fetch unpacks to loose objects: any pack is the failed copy's.
		require.Empty(t, gitPackTestPacks(t, fetched.dir))
		require.Empty(t, gitPackTestPacks(t, packed.dir))
		require.Equal(t, fetched.state(t), packed.state(t))
	})
	t.Run("tag of a tag", func(t *testing.T) {
		source, base, _ := gitMirrorTestSource(t)
		gitBundleTestRun(t, source, "tag", "-a", "v-base", "-m", "annotated base", base)
		gitBundleTestRun(t, source, "tag", "-a", "v-nested", "-m", "tag of a tag", "v-base")
		repo, mirrorGit, mirror := gitMirrorTestRepo(t, source)
		refs, err := gitRefsToFetch(t.Context(), mirrorGit, 0, []*RemoteGitRef{{Ref: &gitutil.Ref{SHA: base}}})
		require.NoError(t, err)
		require.NoError(t, repo.fetchObjects(t.Context(), mirrorGit, 0, true, refs))
		ref := &gitutil.Ref{SHA: base}
		packed := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, true)
		require.Equal(t, "fetch", packed.method)
		fetched := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, false)
		require.Equal(t, fetched.state(t), packed.state(t))
		require.Contains(t, fetched.state(t)["refs"], "refs/tags/v-nested ")
	})
	t.Run("shallow mirror", func(t *testing.T) {
		source, _, next := gitMirrorTestSource(t)
		repo, mirrorGit, mirror := gitMirrorTestRepo(t, source)
		gitMirrorTestFetch(t, repo, mirrorGit, next, 1)
		require.FileExists(t, filepath.Join(mirror, "shallow"))
		ref := &gitutil.Ref{SHA: next}
		packed := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, true)
		require.Equal(t, "fetch", packed.method, "a shallow source's history is what its boundaries say")
		fetched := gitPackTestCheckoutFrom(t, mirror, mirrorGit, ref, false)
		// A full checkout from a shallow mirror is incomplete either way (the
		// engine unshallows mirrors before full-history copies); what matters
		// is that the fallback reproduces it rather than inventing history.
		require.Equal(t, fetched.stateNoFsck(t), packed.stateNoFsck(t))
	})
}

func TestPackGitClosureRefusesShallowDestination(t *testing.T) {
	source, base, _ := gitMirrorTestSource(t)
	dest := t.TempDir()
	gitBundleTestRun(t, dest, "init", "--bare", "--quiet")
	require.NoError(t, os.WriteFile(filepath.Join(dest, "shallow"), []byte(base+"\n"), 0o644))
	err := packGitClosure(t.Context(), gitutil.NewGitCLI(gitutil.WithGitDir(dest)), gitutil.NewGitCLI(gitutil.WithDir(source)), []string{base})
	require.ErrorContains(t, err, "destination has shallow")
}
