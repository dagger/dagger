package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func localTreeCheckoutCLI(dir string) *gitutil.GitCLI {
	return gitutil.NewGitCLI(gitutil.WithDir(dir), gitutil.WithWorkTree(dir), gitutil.WithGitDir(filepath.Join(dir, ".git")))
}

func TestLocalGitTreeCheckout(t *testing.T) {
	ctx := context.Background()
	source := historyRepo(t, "sha1")
	base := historyCommit(t, source, "file", "base")
	require.NoError(t, os.WriteFile(filepath.Join(source, "executable"), []byte("#!/bin/sh\n"), 0755))
	require.NoError(t, os.Symlink("file", filepath.Join(source, "link")))
	gitMirrorTestRun(t, source, "add", ".")
	gitMirrorTestRun(t, source, "commit", "-m", "modes")
	tip := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "tag", "v1", tip)
	gitMirrorTestRun(t, source, "gc")
	// Exercise common object storage, alternates chains, shallow boundaries,
	// and a source worktree that must never be copied into the result.
	common := filepath.Join(t.TempDir(), "common repo")
	require.NoError(t, os.Rename(source, common))
	source = common
	worktree := filepath.Join(t.TempDir(), "linked")
	gitMirrorTestRun(t, source, "worktree", "add", "--detach", worktree, tip)
	shared := t.TempDir()
	gitMirrorTestRun(t, shared, "clone", "--shared", source, ".")
	shallow := t.TempDir()
	gitMirrorTestRun(t, shallow, "clone", "--depth=1", "file://"+worktree, ".")
	bare := t.TempDir()
	gitMirrorTestRun(t, bare, "clone", "--bare", source, ".")
	require.NoError(t, os.WriteFile(filepath.Join(source, "file"), []byte("dirty"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "untracked"), []byte("untracked"), 0600))
	gitMirrorTestRun(t, source, "add", "file")

	for _, tc := range []struct {
		name, dir, sha, ref string
	}{
		{"packed dirty branch", source, tip, "refs/heads/main"},
		{"old commit", source, base, ""},
		{"tag", source, tip, "refs/tags/v1"},
		{"linked worktree", worktree, tip, ""},
		{"alternates", shared, tip, ""},
		{"shallow", shallow, tip, ""},
		{"bare", bare, tip, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := &gitutil.Ref{Name: tc.ref, SHA: tc.sha}
			before := historySnapshot(t, tc.dir)
			baseline := t.TempDir()
			require.NoError(t, doGitCheckout(ctx, localTreeCheckoutCLI(baseline), nil, tc.dir, ref, 1, true))
			historyForbidFetch(t)
			actual := t.TempDir()
			require.NoError(t, doLocalGitTreeCheckout(ctx, gitutil.NewGitCLI(gitutil.WithDir(tc.dir)), localTreeCheckoutCLI(actual), nil, tc.dir, ref))
			require.Equal(t, localTreeSnapshot(t, baseline), localTreeSnapshot(t, actual))
			require.NoDirExists(t, filepath.Join(actual, ".git"))
			require.Equal(t, before, historySnapshot(t, tc.dir), "source must remain immutable")
		})
	}
}

// Include executable bits, symlinks and normalized timestamps, not only bytes.
func localTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, ent os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := ent.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		var content string
		if info.Mode()&os.ModeSymlink != 0 {
			content, err = os.Readlink(path)
		} else if !ent.IsDir() {
			var data []byte
			data, err = os.ReadFile(path)
			content = string(data)
		}
		out[rel] = fmt.Sprintf("%s %s %s", info.Mode(), info.ModTime().UTC().Format(time.RFC3339Nano), content)
		return err
	}))
	return out
}

func TestLocalGitTreeCheckoutConcurrent(t *testing.T) {
	ctx := context.Background()
	source := historyRepo(t, "sha256")
	sha := historyCommit(t, source, "file", "content")
	awkward := filepath.Join(t.TempDir(), "objects :\"\\\n repo")
	require.NoError(t, os.Rename(source, awkward))
	source = awkward
	before := historySnapshot(t, source)
	historyForbidFetch(t)
	var wg sync.WaitGroup
	for range 4 {
		dest := t.TempDir()
		wg.Go(func() {
			err := doLocalGitTreeCheckout(ctx, gitutil.NewGitCLI(gitutil.WithDir(source)), localTreeCheckoutCLI(dest), nil, source, &gitutil.Ref{SHA: sha})
			assertLocalTreeCheckout(t, dest, err)
		})
	}
	wg.Wait()
	require.Equal(t, before, historySnapshot(t, source))
}

func TestLocalGitTreeCheckoutFailure(t *testing.T) {
	source := historyRepo(t, "sha1")
	sha := historyCommit(t, source, "file", "content")
	before := historySnapshot(t, source)
	historyForbidFetch(t)
	t.Run("missing object", func(t *testing.T) {
		err := doLocalGitTreeCheckout(context.Background(), gitutil.NewGitCLI(gitutil.WithDir(source)), localTreeCheckoutCLI(t.TempDir()), nil, source, &gitutil.Ref{SHA: "1234567890123456789012345678901234567890"})
		require.Error(t, err)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := doLocalGitTreeCheckout(ctx, gitutil.NewGitCLI(gitutil.WithDir(source)), localTreeCheckoutCLI(t.TempDir()), nil, source, &gitutil.Ref{SHA: sha})
		require.ErrorIs(t, err, context.Canceled)
	})
	require.Equal(t, before, historySnapshot(t, source))
}

func TestLocalGitTreeCheckoutSubmodules(t *testing.T) {
	ctx := context.Background()
	sub := historyRepo(t, "sha1")
	historyCommit(t, sub, "file", "submodule")
	source := historyRepo(t, "sha1")
	historyCommit(t, source, "file", "parent")
	gitMirrorTestRun(t, source, "-c", "protocol.file.allow=always", "submodule", "add", sub, "sub")
	gitMirrorTestRun(t, source, "commit", "-am", "add submodule")
	ref := &gitutil.Ref{SHA: gitMirrorTestRun(t, source, "rev-parse", "HEAD")}
	before := historySnapshot(t, source)
	baseline := t.TempDir()
	// Production deliberately forbids local-file submodules. Override that
	// policy only in this fixture, to avoid needing a network Git server.
	allowFile := gitutil.WithArgs("-c", "protocol.file.allow=always")
	require.NoError(t, doGitCheckout(ctx, localTreeCheckoutCLI(baseline).New(allowFile), nil, source, ref, 1, true))
	actual := t.TempDir()
	checkout := localTreeCheckoutCLI(actual).New(allowFile, gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
		// Submodule cloning is still necessary; only the superproject must
		// borrow objects rather than fetch or copy its history.
		if slices.Contains(cmd.Args, "fetch") {
			return fmt.Errorf("unexpected superproject fetch: %v", cmd.Args)
		}
		return cmd.Run()
	}))
	require.NoError(t, doLocalGitTreeCheckout(ctx, gitutil.NewGitCLI(gitutil.WithDir(source)), checkout, nil, source, ref))
	require.Equal(t, localTreeSnapshot(t, baseline), localTreeSnapshot(t, actual))
	require.Equal(t, before, historySnapshot(t, source))
	data, err := os.ReadFile(filepath.Join(actual, "sub", "file"))
	require.NoError(t, err)
	require.Equal(t, "submodule", string(data))
}

func assertLocalTreeCheckout(t *testing.T, dir string, err error) {
	t.Helper()
	if err != nil {
		t.Error(err)
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(data) != "content" {
		t.Errorf("checkout content = %q, error = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Errorf("checkout retained .git: %v", err)
	}
}
