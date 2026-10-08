package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger/core"

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

// TestWithGitUncommittedIsPerClient checks that Directory.__withGitUncommitted
// is cached per client. The field reads the calling client's checkout, so two
// clients selecting it with the same receiver, path and HEAD must each get
// their own uncommitted changes. Clients can select this internal field
// directly. The receiver is built in a container, without any per-client
// input, so only the field's own cache key can separate the two clients.
func (WorkspaceSuite) TestWithGitUncommittedIsPerClient(ctx context.Context, t *testctx.T) {
	// A fixed identity and dates make the host commit and the container commit
	// below the same SHA. The order is fixed so that both clients build the
	// same receiver recipe.
	gitEnv := [][2]string{
		{"GIT_AUTHOR_NAME", "Dagger Tests"},
		{"GIT_AUTHOR_EMAIL", "dagger@example.com"},
		{"GIT_AUTHOR_DATE", "2024-01-01T00:00:00Z"},
		{"GIT_COMMITTER_NAME", "Dagger Tests"},
		{"GIT_COMMITTER_EMAIL", "dagger@example.com"},
		{"GIT_COMMITTER_DATE", "2024-01-01T00:00:00Z"},
	}

	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
		for _, kv := range gitEnv {
			cmd.Env = append(cmd.Env, kv[0]+"="+kv[1])
		}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	git("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644))
	git("add", ".")
	git("commit", "-m", "initial")
	head := strings.TrimSpace(git("rev-parse", "HEAD"))

	note := filepath.Join(repo, "note.txt")
	read := func(c *dagger.Client) string {
		t.Helper()
		ctr := core.NewQuery(c).Container().From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithNewFile("/repo/tracked.txt", "base\n").
			WithWorkdir("/repo")
		for _, kv := range gitEnv {
			ctr = ctr.WithEnvVariable(kv[0], kv[1])
		}
		ctr = ctr.
			WithExec([]string{"git", "init", "-b", "main"}).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"})
		ctrHead, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, head, strings.TrimSpace(ctrHead), "the receiver must be a checkout of the host HEAD")
		receiver, err := ctr.Directory("/repo").ID(ctx)
		require.NoError(t, err)

		got, err := testutil.QueryWithClient[struct {
			Node struct {
				WithGitUncommitted struct {
					File struct {
						Contents string
					}
				} `json:"__withGitUncommitted"`
			}
		}](c, t, `query($receiver: ID!, $path: String!, $head: String!) {
  node(id: $receiver) {
    ... on Directory {
      __withGitUncommitted(checkoutPath: $path, expectedHeadSHA: $head) {
        file(path: "note.txt") { contents }
      }
    }
  }
}`, &testutil.QueryOptions{Variables: map[string]any{
			"receiver": receiver,
			"path":     repo,
			"head":     head,
		}})
		require.NoError(t, err)
		return got.Node.WithGitUncommitted.File.Contents
	}

	// Client A stays connected, so its result stays cached while client B
	// reads the same path at the same HEAD with different uncommitted changes.
	require.NoError(t, os.WriteFile(note, []byte("from client A\n"), 0o644))
	require.Equal(t, "from client A\n", read(connect(ctx, t)))
	require.NoError(t, os.WriteFile(note, []byte("from client B\n"), 0o644))
	require.Equal(t, "from client B\n", read(connect(ctx, t)), "client B must not see client A's uncommitted changes")
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return string(out)
}
