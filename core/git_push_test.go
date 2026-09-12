package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

type gitPushFixture struct {
	dir string
	t   *testing.T
}

func newGitPushFixture(t *testing.T) gitPushFixture {
	t.Helper()
	f := gitPushFixture{dir: t.TempDir(), t: t}
	f.git("init", "-b", "main")
	f.commit("base", "base", "base")
	return f
}

func (f gitPushFixture) git(args ...string) string {
	f.t.Helper()
	git := gitutil.NewGitCLI(gitutil.WithDir(f.dir), gitutil.WithConfig(map[string]string{"user.name": "Test", "user.email": "test@example.com", "commit.gpgSign": "false"}))
	out, err := git.Run(f.t.Context(), args...)
	require.NoError(f.t, err)
	return strings.TrimSpace(string(out))
}

func (f gitPushFixture) commit(name, contents, message string) string {
	f.t.Helper()
	require.NoError(f.t, os.WriteFile(filepath.Join(f.dir, name), []byte(contents), 0o644))
	f.git("add", "--", name)
	f.git("commit", "-m", message)
	return f.git("rev-parse", "HEAD")
}

func pushTestRemote(t *testing.T, f gitPushFixture) (string, *gitutil.GitCLI) {
	t.Helper()
	remote := t.TempDir()
	f.git("init", "--bare", remote)
	return remote, gitutil.NewGitCLI(append(gitPushCLIOptions(), gitutil.WithDir(f.dir))...)
}

func TestGitPushResultsAndLeases(t *testing.T) {
	f := newGitPushFixture(t)
	remote, git := pushTestRemote(t, f)
	name := "refs/heads/destination"
	base := f.git("rev-parse", "HEAD")
	result, err := runGitPush(t.Context(), git, remote, name, base, "")
	require.NoError(t, err)
	require.Equal(t, &GitPushResult{Ref: name, SHA: base, Disposition: GitPushCreated}, result)
	result, err = runGitPush(t.Context(), git, remote, name, base, "")
	require.NoError(t, err)
	require.Equal(t, GitPushUpToDate, result.Disposition)
	require.Equal(t, base, result.PreviousSHA)
	_, err = runGitPush(t.Context(), git, remote, name, base, strings.Repeat("a", 40))
	require.ErrorContains(t, err, "stale lease")
	next := f.commit("next", "next", "next")
	result, err = runGitPush(t.Context(), git, remote, name, next, "")
	require.NoError(t, err)
	require.Equal(t, &GitPushResult{Ref: name, PreviousSHA: base, SHA: next, Disposition: GitPushFastForward}, result)
	_, err = runGitPush(t.Context(), git, remote, name, base, "")
	require.ErrorContains(t, err, "rejected")
	_, err = runGitPush(t.Context(), git, remote, name, base, base)
	require.ErrorContains(t, err, "stale info")
	result, err = runGitPush(t.Context(), git, remote, name, base, next)
	require.NoError(t, err)
	require.Equal(t, &GitPushResult{Ref: name, PreviousSHA: next, SHA: base, Disposition: GitPushForced}, result)
	result, err = runGitPush(t.Context(), git, remote, name, next, base)
	require.NoError(t, err)
	require.Equal(t, GitPushFastForward, result.Disposition, "a lease does not imply a forced update")
	_, err = runGitPush(t.Context(), git, remote, "refs/heads/missing", next, base)
	require.ErrorContains(t, err, "stale info", "a missing ref does not satisfy an exact SHA lease")
}

func TestGitPushConcurrentUpdate(t *testing.T) {
	f := newGitPushFixture(t)
	remote, git := pushTestRemote(t, f)
	name := "refs/heads/destination"
	base := f.git("rev-parse", "HEAD")
	middle := f.commit("middle", "middle", "middle")
	tip := f.commit("tip", "tip", "tip")
	_, err := runGitPush(t.Context(), git, remote, name, tip, "")
	require.NoError(t, err)
	f.git("--git-dir="+remote, "update-ref", name, base)
	// Change the destination immediately before Git connects. Results must
	// report Git's actual old value, not an earlier ls-remote observation.
	racing := git.New(gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
		f.git("--git-dir="+remote, "update-ref", name, middle)
		return cmd.Run()
	}))
	result, err := runGitPush(t.Context(), racing, remote, name, tip, "")
	require.NoError(t, err)
	require.Equal(t, middle, result.PreviousSHA)
	f.git("--git-dir="+remote, "update-ref", name, base)
	_, err = runGitPush(t.Context(), racing, remote, name, tip, base)
	require.ErrorContains(t, err, "stale info")
	require.Equal(t, middle, f.git("--git-dir="+remote, "rev-parse", name))
}

func TestGitPushHooksTagsAndCancellation(t *testing.T) {
	f := newGitPushFixture(t)
	remote, git := pushTestRemote(t, f)
	base := f.git("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, ".git/hooks/pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	_, err := runGitPush(t.Context(), git, remote, "refs/tags/published", base, "")
	require.NoError(t, err)
	tip := f.commit("tip", "tip", "tip")
	_, err = runGitPush(t.Context(), git, remote, "refs/tags/published", tip, "")
	require.ErrorContains(t, err, "already exists")
	result, err := runGitPush(t.Context(), git, remote, "refs/tags/published", tip, base)
	require.NoError(t, err)
	require.Equal(t, GitPushForced, result.Disposition)
	require.NoError(t, os.WriteFile(filepath.Join(remote, "hooks/pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	_, err = runGitPush(t.Context(), git, remote, "refs/heads/rejected", tip, "")
	require.ErrorContains(t, err, "hook declined")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = runGitPush(ctx, git, remote, "refs/heads/cancelled", tip, "")
	require.Error(t, err)
}

func TestGitPushValidation(t *testing.T) {
	name, err := (GitPushOpts{}).Ref(&gitutil.Ref{Name: "refs/heads/main"})
	require.NoError(t, err)
	require.Equal(t, "refs/heads/main", name)
	for _, source := range []*gitutil.Ref{nil, {Name: strings.Repeat("a", 40)}, {Name: "refs/tags/v1"}} {
		_, err := (GitPushOpts{}).Ref(source)
		require.ErrorContains(t, err, "explicit branch")
	}
	for _, invalid := range []string{"HEAD", "abc", strings.Repeat("0", 40), strings.Repeat("A", 40), "+refs/heads/main"} {
		_, err := (GitPushOpts{Branch: "main", ExpectedRemoteSHA: invalid}).Ref(nil)
		require.ErrorContains(t, err, "full lowercase")
	}
	for _, output := range []string{"", " \tsha:refs/heads/main\t", " \tsha:refs/heads/main\tabc..def", "!\tsha:refs/heads/main\t[rejected]"} {
		_, err := parseGitPushResult(output, "refs/heads/main", strings.Repeat("a", 40))
		require.Error(t, err)
	}
	limit := &gitPushOutputLimit{size: (16 << 20) - 1}
	_, err = limit.Write([]byte("a"))
	require.NoError(t, err)
	_, err = limit.Write([]byte("b"))
	require.ErrorContains(t, err, "exceeds 16 MiB")
}
