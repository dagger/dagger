package gitutil

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetURLRewrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}

	SetURLRewrites(map[string][]string{"file://" + repo: {"https://example.invalid/mirrored"}})
	defer SetURLRewrites(nil)

	// Also pass per-command config to check both sets of GIT_CONFIG_* entries
	// apply together.
	cli := NewGitCLI(WithConfig(map[string]string{"gc.autoDetach": "false"}))
	out, err := cli.Run(context.Background(), "ls-remote", "https://example.invalid/mirrored", "HEAD")
	require.NoError(t, err)
	require.Contains(t, string(out), "HEAD")
}
