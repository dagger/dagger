package workspace

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fakeGitConfigFS(files map[string]string) func(context.Context, string) ([]byte, error) {
	return func(_ context.Context, path string) ([]byte, error) {
		if data, ok := files[filepath.Clean(path)]; ok {
			return []byte(data), nil
		}
		return nil, errNotExistForTest
	}
}

type notExistError struct{}

func (notExistError) Error() string { return "not found" }

var errNotExistForTest = notExistError{}

func resolveIncludesForTest(t *testing.T, files map[string]string, state GitConfigState) string {
	t.Helper()
	data := files[filepath.Clean(state.ConfigPath)]
	return string(ResolveGitConfigIncludes(context.Background(), fakeGitConfigFS(files), state, []byte(data)))
}

func TestResolveGitConfigIncludes(t *testing.T) {
	t.Parallel()

	state := GitConfigState{
		ConfigPath: "/repo/.git/config",
		GitDir:     "/repo/.git",
		Branch:     "main",
	}

	t.Run("unconditional include is inlined at its directive", func(t *testing.T) {
		t.Parallel()
		out := resolveIncludesForTest(t, map[string]string{
			"/repo/.git/config": "[include]\n\tpath = origin.config\n",
			"/repo/.git/origin.config": `[remote "origin"]
	url = git@github.com:acme/api.git
`,
		}, state)

		url, ok := GitRemoteURL([]byte(out), "origin")
		require.True(t, ok)
		require.Equal(t, "git@github.com:acme/api.git", url)
	})

	t.Run("relative include paths resolve against the including file", func(t *testing.T) {
		t.Parallel()
		out := resolveIncludesForTest(t, map[string]string{
			"/repo/.git/config":           "[include]\n\tpath = ../shared/git.config\n",
			"/repo/shared/git.config":     "[include]\n\tpath = nested.config\n",
			"/repo/shared/nested.config":  `[remote "origin"]` + "\n\turl = https://github.com/acme/api\n",
			"/elsewhere/unrelated.config": "",
		}, state)

		url, ok := GitRemoteURL([]byte(out), "origin")
		require.True(t, ok)
		require.Equal(t, "https://github.com/acme/api", url)
	})

	t.Run("quoted include paths", func(t *testing.T) {
		t.Parallel()
		out := resolveIncludesForTest(t, map[string]string{
			"/repo/.git/config":        "[include]\n\tpath = \"origin.config\"\n",
			"/repo/.git/origin.config": `[remote "origin"]` + "\n\turl = https://github.com/acme/api\n",
		}, state)

		_, ok := GitRemoteURL([]byte(out), "origin")
		require.True(t, ok)
	})

	t.Run("unreadable includes are skipped", func(t *testing.T) {
		t.Parallel()
		out := resolveIncludesForTest(t, map[string]string{
			"/repo/.git/config": "[include]\n\tpath = missing.config\n[user]\n\tname = alice\n",
		}, state)
		require.Contains(t, out, "name = alice")
		_, ok := GitRemoteURL([]byte(out), "origin")
		require.False(t, ok)
	})

	t.Run("home-relative includes are skipped", func(t *testing.T) {
		t.Parallel()
		out := resolveIncludesForTest(t, map[string]string{
			"/repo/.git/config": "[include]\n\tpath = ~/git.config\n",
		}, state)
		_, ok := GitRemoteURL([]byte(out), "origin")
		require.False(t, ok)
	})

	t.Run("include cycles stop at the depth limit", func(t *testing.T) {
		t.Parallel()
		out := resolveIncludesForTest(t, map[string]string{
			"/repo/.git/config":      "[include]\n\tpath = loop.config\n",
			"/repo/.git/loop.config": "[include]\n\tpath = loop.config\n",
		}, state)
		require.NotEmpty(t, out)
	})

	t.Run("includeIf gitdir", func(t *testing.T) {
		t.Parallel()
		files := func(cond string) map[string]string {
			return map[string]string{
				"/repo/.git/config":        "[includeIf \"" + cond + "\"]\n\tpath = origin.config\n",
				"/repo/.git/origin.config": `[remote "origin"]` + "\n\turl = https://github.com/acme/api\n",
			}
		}

		for cond, want := range map[string]bool{
			"gitdir:/repo/":          true,  // trailing / matches everything below
			"gitdir:/repo/.git":      true,  // exact
			"gitdir:repo/.git":       true,  // relative patterns get **/ prepended
			"gitdir:/other/":         false, // different repo
			"gitdir:~/repos/":        false, // home-relative patterns cannot resolve
			"gitdir/i:/REPO/":        true,  // case-insensitive variant
			"gitdir:/REPO/":          false, // case-sensitive by default
			"onbranch:main":          true,
			"onbranch:release/":      false,
			"onbranch:ma*":           true,
			"hasconfig:remote.*.url": false, // not evaluated
		} {
			out := resolveIncludesForTest(t, files(cond), state)
			_, ok := GitRemoteURL([]byte(out), "origin")
			require.Equal(t, want, ok, "condition %q", cond)
		}
	})

	t.Run("includeIf onbranch with slashes and detached HEAD", func(t *testing.T) {
		t.Parallel()
		files := map[string]string{
			"/repo/.git/config":        "[includeIf \"onbranch:release/\"]\n\tpath = origin.config\n",
			"/repo/.git/origin.config": `[remote "origin"]` + "\n\turl = https://github.com/acme/api\n",
		}

		onRelease := state
		onRelease.Branch = "release/v1"
		_, ok := GitRemoteURL([]byte(resolveIncludesForTest(t, files, onRelease)), "origin")
		require.True(t, ok)

		detached := state
		detached.Branch = ""
		_, ok = GitRemoteURL([]byte(resolveIncludesForTest(t, files, detached)), "origin")
		require.False(t, ok)
	})

	t.Run("gitdir pattern with leading dot-slash resolves against the config dir", func(t *testing.T) {
		t.Parallel()
		// From /main/.git/config, "./worktrees/" means /main/.git/worktrees/**.
		out := resolveIncludesForTest(t, map[string]string{
			"/main/.git/config":        "[includeIf \"gitdir:./worktrees/\"]\n\tpath = origin.config\n",
			"/main/.git/origin.config": `[remote "origin"]` + "\n\turl = https://github.com/acme/api\n",
		}, GitConfigState{
			ConfigPath: "/main/.git/config",
			GitDir:     "/main/.git/worktrees/feature",
		})
		_, ok := GitRemoteURL([]byte(out), "origin")
		require.True(t, ok)
	})

	t.Run("later includes shadow earlier values like git config --get", func(t *testing.T) {
		t.Parallel()
		out := resolveIncludesForTest(t, map[string]string{
			"/repo/.git/config": `[remote "origin"]
	url = https://github.com/acme/old
[include]
	path = override.config
`,
			"/repo/.git/override.config": `[remote "origin"]
	url = https://github.com/acme/new
`,
		}, state)

		url, ok := GitRemoteURL([]byte(out), "origin")
		require.True(t, ok)
		require.Equal(t, "https://github.com/acme/new", url)
	})
}

func TestGitBranchFromHEAD(t *testing.T) {
	t.Parallel()

	require.Equal(t, "main", GitBranchFromHEAD([]byte("ref: refs/heads/main\n")))
	require.Equal(t, "release/v1", GitBranchFromHEAD([]byte("ref: refs/heads/release/v1")))
	require.Empty(t, GitBranchFromHEAD([]byte("0123456789abcdef0123456789abcdef01234567\n")))
	require.Empty(t, GitBranchFromHEAD(nil))
}
