package vcs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A repository on a server no rule knows by name, named by its .git suffix,
// resolves from its path alone: resolving it starts no git process, so a
// module source parse costs no subprocesses on every load.
func TestStaticRepoRootStartsNoProcess(t *testing.T) {
	bin := t.TempDir()
	ran := filepath.Join(t.TempDir(), "git-ran")
	// PATH holds only this git, so it records its run with a redirection,
	// which needs no other program.
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n: > "+ran+"\nexit 1\n"), 0o755))
	t.Setenv("PATH", bin)

	root, err := RepoRootForImportPath("git.example.test/org/repo.git/sub", false)
	require.NoError(t, err)
	require.Equal(t, &RepoRoot{
		VCS:  vcsGit,
		Repo: "https://git.example.test/org/repo",
		Root: "git.example.test/org/repo.git",
	}, root)
	require.NoFileExists(t, ran)
}
