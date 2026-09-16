package daggercmd

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestFormatWorkspaceGitURL(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name, cloneURL, ref, cwd, want string
	}{
		{"root", "https://github.com/dagger/dagger.git", "refs/heads/main", "/", "https://github.com/dagger/dagger.git#refs/heads/main"},
		{"selected tag and subdir", "https://github.com/dagger/python", "refs/tags/v1.0.1", "/ruff", "https://github.com/dagger/python#refs/tags/v1.0.1:ruff"},
		{"same named branch", "https://github.com/dagger/python", "refs/heads/v1.0.1", "/ruff", "https://github.com/dagger/python#refs/heads/v1.0.1:ruff"},
		{"exact nested path", "https://github.com/dagger/python", "refs/heads/main", "/ruff/tests/fixtures", "https://github.com/dagger/python#refs/heads/main:ruff/tests/fixtures"},
		{"SCP origin", "git@github.com:dagger/python.git", "refs/heads/feature/work", "ruff", "ssh://git@github.com/dagger/python.git#refs/heads/feature/work:ruff"},
		{"SSH port", "ssh://git@example.com:2222/team/repo.git", "refs/tags/v1.2.3", "/src", "ssh://git@example.com:2222/team/repo.git#refs/tags/v1.2.3:src"},
		{"detached", "https://github.com/dagger/python", sha, ".", "https://github.com/dagger/python#" + sha},
		{"custom ref", "https://github.com/dagger/python", "refs/pull/42/head", "ruff", "https://github.com/dagger/python#refs/pull/42/head:ruff"},
		{"path with spaces", "https://github.com/dagger/python", "refs/heads/main", "/test fixtures", "https://github.com/dagger/python#refs/heads/main:test fixtures"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatWorkspaceGitURL(tc.cloneURL, tc.ref, tc.cwd)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			// Parsing a #ref URL is local and proves the selected subdirectory
			// survives the form accepted by -W and Address.directory.
			parsed, err := gitref.Parse(t.Context(), got)
			require.NoError(t, err)
			require.Equal(t, gitref.GitRefSelector, parsed.Selector)
			require.Equal(t, tc.ref, parsed.ModVersion)
			wantPath, err := workspaceRelativeCwd(tc.cwd)
			require.NoError(t, err)
			gotPath, err := workspaceRelativeCwd(parsed.RepoRootSubdir)
			require.NoError(t, err)
			require.Equal(t, filepath.ToSlash(wantPath), filepath.ToSlash(gotPath))
			address, err := gitutil.ParseURL(got)
			require.NoError(t, err)
			require.Equal(t, tc.ref, address.Fragment.Ref)
			require.Equal(t, filepath.ToSlash(wantPath), address.Fragment.Subdir)
		})
	}
}

func TestFormatWorkspaceGitURLErrors(t *testing.T) {
	for _, tc := range []struct {
		cloneURL, ref, cwd, want string
	}{
		{"../repository", "main", "/", "invalid remote Git URL"},
		{"https://", "main", "/", "has no host"},
		{"https://github.com/dagger/python", "", "/", "no resolved Git ref"},
		{"https://github.com/dagger/python", "main", "/../other", "escapes workspace root"},
		{"https://github.com/dagger/python", "main", "/with#hash", "containing #"},
		{"https://github.com/dagger/python", "main", "/with%20space", "containing # or %"},
		{"https://github.com/dagger/python", "refs/heads/with%20space", "/", "containing # or %"},
	} {
		_, err := formatWorkspaceGitURL(tc.cloneURL, tc.ref, tc.cwd)
		require.ErrorContains(t, err, tc.want)
	}
}

func TestLocalWorkspaceGitOrigin(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "remote", "add", "origin", "git@github.com:acme/selected.git")
	selected := filepath.Join(repo, "project", "nested")
	require.NoError(t, os.MkdirAll(selected, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "project", "dagger.toml"), []byte("# workspace\n"), 0o600))

	// An unrelated caller repository must not provide the origin or path.
	caller := t.TempDir()
	runGit(t, caller, "init")
	runGit(t, caller, "remote", "add", "origin", "https://github.com/acme/caller")
	t.Chdir(caller)
	address := (&url.URL{Scheme: "file", Path: filepath.ToSlash(selected)}).String()
	origin, err := localWorkspaceGitOrigin(t.Context(), address)
	require.NoError(t, err)
	require.Equal(t, "git@github.com:acme/selected.git", origin)

	// Git applies configured transport rewrites to the origin it reports.
	runGit(t, repo, "config", "url.ssh://git@example.com/.insteadOf", "git@github.com:")
	origin, err = localWorkspaceGitOrigin(t.Context(), address)
	require.NoError(t, err)
	require.Equal(t, "ssh://git@example.com/acme/selected.git", origin)

	runGit(t, repo, "remote", "remove", "origin")
	_, err = localWorkspaceGitOrigin(t.Context(), address)
	require.ErrorContains(t, err, "Git origin")

	_, err = localWorkspaceGitOrigin(t.Context(), "directory://synthetic")
	require.ErrorContains(t, err, "no remote Git URL")
}
