package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestWorkspaceGitUncommittedUsesPackedHostDelta exercises the host-backed fast
// path end to end. The ignored FIFO is the regression guard: syncing the whole
// checkout through host.directory cannot represent it, while the git delta
// transport never opens ignored paths.
func (WorkspaceSuite) TestWorkspaceGitUncommittedUsesPackedHostDelta(ctx context.Context, t *testctx.T) {
	repo := t.TempDir()
	git := func(args ...string) string { //nolint:unparam // helper mirrors the git CLI
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"HOME="+t.TempDir(),
			"GIT_AUTHOR_NAME=Dagger Tests",
			"GIT_AUTHOR_EMAIL=dagger@example.com",
			"GIT_COMMITTER_NAME=Dagger Tests",
			"GIT_COMMITTER_EMAIL=dagger@example.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}

	git("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.ignored\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "modified.txt"), []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "deleted.txt"), []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "mode.txt"), []byte("mode\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{0, 1, 2, 3}, 0o644))
	require.NoError(t, os.Symlink("modified.txt", filepath.Join(repo, "link")))
	git("add", ".")
	git("commit", "-m", "initial")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "modified.txt"), []byte("new\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("staged\n"), 0o644))
	git("add", "staged.txt")
	require.NoError(t, os.Remove(filepath.Join(repo, "deleted.txt")))
	require.NoError(t, os.Chmod(filepath.Join(repo, "mode.txt"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{0, 255, 1, 254}, 0o644))
	require.NoError(t, os.Remove(filepath.Join(repo, "link")))
	require.NoError(t, os.Symlink("staged.txt", filepath.Join(repo, "link")))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "skip.ignored"), []byte("ignored\n"), 0o644))
	require.NoError(t, unix.Mkfifo(filepath.Join(repo, "unsupported.ignored"), 0o600))

	nested := filepath.Join(repo, "nested")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	nestedGit := exec.CommandContext(ctx, "git", "-C", nested, "init")
	nestedGit.Env = os.Environ()
	out, err := nestedGit.CombinedOutput()
	require.NoError(t, err, string(out))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "inner.txt"), []byte("nested\n"), 0o644))

	c := connect(ctx, t, dagger.WithWorkdir(repo))
	got, err := testutil.QueryWithClient[struct {
		CurrentWorkspace struct {
			Git struct {
				Uncommitted uncommittedChanges `json:"uncommitted"`
			} `json:"git"`
		} `json:"currentWorkspace"`
	}](c, t, `{
  currentWorkspace {
    git { uncommitted { isEmpty diffStats { path kind addedLines removedLines } } }
  }
}`, nil)
	require.NoError(t, err)

	changes := got.CurrentWorkspace.Git.Uncommitted
	require.False(t, changes.IsEmpty)
	for _, path := range []string{
		"modified.txt", "staged.txt", "deleted.txt", "mode.txt",
		"binary.dat", "link", "untracked.txt",
	} {
		require.Contains(t, changes.paths(), path, "missing %s from %s", path, mustJSON(t, changes))
	}
	for _, path := range []string{"skip.ignored", "unsupported.ignored", "nested/inner.txt"} {
		require.NotContains(t, changes.paths(), path, "unexpected %s in %s", path, mustJSON(t, changes))
	}
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return string(out)
}
