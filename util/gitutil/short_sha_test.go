package gitutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsCommitSHAPrefix(t *testing.T) {
	for truthy, prefixes := range map[bool][]string{
		true: {
			"0123", // minimum abbreviation length
			"db59252",
			"01234567890abcdef01234567890abcdef012345", // full SHA is a prefix of itself
		},
		false: {
			"",        // empty string
			"012",     // too short for git to disambiguate
			"g123",    // not hex
			"DB59252", // git object names are lowercase
			"12345678901234567890123456789012345678901", // longer than a full SHA
		},
	} {
		for _, prefix := range prefixes {
			t.Run(fmt.Sprintf("%t/%q", truthy, prefix), func(t *testing.T) {
				assert.Equal(t, truthy, IsCommitSHAPrefix(prefix))
			})
		}
	}
}

// shortSHARepo is a git repository fixture with fully deterministic object
// SHAs: identities and dates are pinned via the environment, so every run of
// the test sees the same commits.
type shortSHARepo struct {
	t   *testing.T
	dir string
}

func (f shortSHARepo) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@dagger.io",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@dagger.io",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = string(exitErr.Stderr)
		}
		require.NoError(f.t, err, "git %s: %s", strings.Join(args, " "), stderr)
	}
	return strings.TrimSpace(string(out))
}

func TestResolveShortSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	ctx := context.Background()
	f := shortSHARepo{t: t, dir: t.TempDir()}

	f.git("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "file"), []byte("hello\n"), 0o600))
	f.git("add", "file")
	f.git("commit", "-q", "-m", "initial")
	first := f.git("rev-parse", "HEAD")
	f.git("commit", "-q", "--allow-empty", "-m", "second")
	second := f.git("rev-parse", "HEAD")
	require.True(t, IsCommitSHA(first))
	require.True(t, IsCommitSHA(second))

	cli := NewGitCLI(WithDir(f.dir))

	t.Run("unique prefix resolves to the full SHA", func(t *testing.T) {
		sha, err := cli.ResolveShortSHA(ctx, first[:7])
		require.NoError(t, err)
		require.Equal(t, first, sha)

		sha, err = cli.ResolveShortSHA(ctx, second[:12])
		require.NoError(t, err)
		require.Equal(t, second, sha)
	})

	t.Run("full SHA resolves to itself", func(t *testing.T) {
		sha, err := cli.ResolveShortSHA(ctx, first)
		require.NoError(t, err)
		require.Equal(t, first, sha)
	})

	t.Run("annotated tag object peels to its commit", func(t *testing.T) {
		f.git("tag", "-a", "-m", "annotated", "v1.0.0", first)
		tagSHA := f.git("rev-parse", "v1.0.0")
		require.NotEqual(t, first, tagSHA, "annotated tag should be its own object")

		sha, err := cli.ResolveShortSHA(ctx, tagSHA[:12])
		require.NoError(t, err)
		require.Equal(t, first, sha)
	})

	t.Run("non-commit objects are ignored", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(f.dir, "blob"), []byte("just a blob\n"), 0o600))
		blobSHA := f.git("hash-object", "-w", filepath.Join(f.dir, "blob"))
		require.True(t, IsCommitSHA(blobSHA))

		_, err := cli.ResolveShortSHA(ctx, blobSHA[:12])
		require.ErrorIs(t, err, ErrShortSHANotFound)
	})

	t.Run("unknown prefix is not found", func(t *testing.T) {
		_, err := cli.ResolveShortSHA(ctx, "abcdef123456")
		require.ErrorIs(t, err, ErrShortSHANotFound)
	})

	t.Run("invalid prefixes are rejected", func(t *testing.T) {
		for _, prefix := range []string{"", "abc", "xyz1234", strings.Repeat("a", 41)} {
			_, err := cli.ResolveShortSHA(ctx, prefix)
			require.ErrorContains(t, err, "invalid short commit SHA", "prefix %q", prefix)
		}
	})

	t.Run("ambiguous prefix reports candidate count", func(t *testing.T) {
		// manufacture two commits sharing a 4-character prefix: with pinned
		// dates the generated SHAs are deterministic, so the loop always
		// terminates at the same iteration
		emptyTree := f.git("hash-object", "-t", "tree", "-w", "/dev/null")
		prefixes := map[string]string{first[:4]: first, second[:4]: second}
		var prefix string
		for i := 0; ; i++ {
			require.Less(t, i, 5000, "no 4-character prefix collision found")
			sha := f.git("commit-tree", "-m", fmt.Sprintf("filler-%d", i), emptyTree)
			p := sha[:4]
			if other, ok := prefixes[p]; ok && other != sha {
				prefix = p
				break
			}
			prefixes[p] = sha
		}

		_, err := cli.ResolveShortSHA(ctx, prefix)
		require.ErrorIs(t, err, ErrShortSHAAmbiguous)
		require.ErrorContains(t, err, "matches 2 commits")
	})
}
