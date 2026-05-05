package schema

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/util/gitutil"
)

func TestRemotePeeledTagRefs(t *testing.T) {
	rawAnnotatedTagSHA := strings.Repeat("a", 40)
	peeledAnnotatedTagSHA := strings.Repeat("b", 40)
	lightweightTagSHA := strings.Repeat("c", 40)

	tags := remotePeeledTagRefs(&gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "refs/tags/v1.0.0", SHA: rawAnnotatedTagSHA},
			{Name: "refs/tags/v1.0.0^{}", SHA: peeledAnnotatedTagSHA},
			{Name: "refs/tags/v1.1.0", SHA: lightweightTagSHA},
			{Name: "refs/heads/main", SHA: strings.Repeat("d", 40)},
			{Name: "refs/tags/not-a-commit", SHA: "not-a-sha"},
		},
	})

	require.Equal(t, map[string]string{
		"refs/tags/v1.0.0": peeledAnnotatedTagSHA,
		"refs/tags/v1.1.0": lightweightTagSHA,
	}, tags)
}

func TestLocalPeeledTagRefs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	ctx := context.Background()
	dir := t.TempDir()
	git := gitutil.NewGitCLI(gitutil.WithDir(dir))
	runGit := func(args ...string) []byte {
		t.Helper()
		out, err := git.Run(ctx, args...)
		require.NoError(t, err)
		return out
	}

	runGit("init")
	runGit("config", "user.email", "test@dagger.io")
	runGit("config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("hello\n"), 0o600))
	runGit("add", "file")
	runGit("commit", "-m", "init")
	commitSHA := strings.TrimSpace(string(runGit("rev-parse", "HEAD")))
	require.True(t, gitutil.IsCommitSHA(commitSHA))

	runGit("tag", "v1.0.0")
	runGit("tag", "-a", "v1.1.0", "-m", "annotated")

	tags, err := localPeeledTagRefs(ctx, git)
	require.NoError(t, err)
	require.Equal(t, commitSHA, tags["refs/tags/v1.0.0"])
	require.Equal(t, commitSHA, tags["refs/tags/v1.1.0"])
}

func TestReconcileGitTagRefs(t *testing.T) {
	shaA := strings.Repeat("a", 40)
	shaB := strings.Repeat("b", 40)
	shaC := strings.Repeat("c", 40)

	tags, err := reconcileGitTagRefs(
		map[string]string{
			"refs/tags/local-only": shaA,
			"refs/tags/matching":   shaB,
		},
		map[string]string{
			"refs/tags/matching":    shaB,
			"refs/tags/remote-only": shaC,
		},
	)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"refs/tags/local-only":  shaA,
		"refs/tags/matching":    shaB,
		"refs/tags/remote-only": shaC,
	}, gitReleaseTagsByName(tags))

	_, err = reconcileGitTagRefs(
		map[string]string{"refs/tags/v1.0.0": shaA},
		map[string]string{"refs/tags/v1.0.0": shaB},
	)
	require.ErrorContains(t, err, `git tag "v1.0.0" resolves to different commits`)
}

func TestSemverReleaseTags(t *testing.T) {
	tags := []gitReleaseTag{
		{RefName: "refs/tags/v1.0.0", SHA: strings.Repeat("a", 40)},
		{RefName: "refs/tags/v2.0.0-rc.1", SHA: strings.Repeat("b", 40)},
		{RefName: "refs/tags/v1.5.0", SHA: strings.Repeat("c", 40)},
		{RefName: "refs/tags/not-semver", SHA: strings.Repeat("d", 40)},
		{RefName: "refs/heads/v3.0.0", SHA: strings.Repeat("e", 40)},
	}

	stable := semverReleaseTags(tags, false)
	sortGitReleaseTags(stable)
	require.Equal(t, []string{
		"refs/tags/v1.5.0",
		"refs/tags/v1.0.0",
	}, gitReleaseTagNames(stable))

	withPreRelease := semverReleaseTags(tags, true)
	sortGitReleaseTags(withPreRelease)
	require.Equal(t, []string{
		"refs/tags/v2.0.0-rc.1",
		"refs/tags/v1.5.0",
		"refs/tags/v1.0.0",
	}, gitReleaseTagNames(withPreRelease))
}

func gitReleaseTagsByName(tags []gitReleaseTag) map[string]string {
	byName := make(map[string]string, len(tags))
	for _, tag := range tags {
		byName[tag.RefName] = tag.SHA
	}
	return byName
}

func gitReleaseTagNames(tags []gitReleaseTag) []string {
	names := make([]string, len(tags))
	for i, tag := range tags {
		names[i] = tag.RefName
	}
	return names
}
