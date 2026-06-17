package core

import (
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestSelectLatestReleaseTag(t *testing.T) {
	t.Parallel()

	t.Run("accepts v-prefixed and bare semver tags", func(t *testing.T) {
		t.Parallel()

		tag, ok := SelectLatestReleaseTag([]string{"1.2.3", "v1.4.0", "v1.3.0"}, false)
		require.True(t, ok)
		require.Equal(t, "v1.4.0", tag)
	})

	t.Run("compares numeric versions instead of lexical order", func(t *testing.T) {
		t.Parallel()

		tag, ok := SelectLatestReleaseTag([]string{"v1.9.0", "v1.10.0", "v1.2.30"}, false)
		require.True(t, ok)
		require.Equal(t, "v1.10.0", tag)
	})

	t.Run("excludes prereleases by default", func(t *testing.T) {
		t.Parallel()

		tag, ok := SelectLatestReleaseTag([]string{"v1.2.0", "v1.3.0-rc.1"}, false)
		require.True(t, ok)
		require.Equal(t, "v1.2.0", tag)
	})

	t.Run("includes prereleases when requested", func(t *testing.T) {
		t.Parallel()

		tag, ok := SelectLatestReleaseTag([]string{"v1.2.0", "v1.3.0-rc.1"}, true)
		require.True(t, ok)
		require.Equal(t, "v1.3.0-rc.1", tag)
	})

	t.Run("prefers final release over prerelease of same version", func(t *testing.T) {
		t.Parallel()

		tag, ok := SelectLatestReleaseTag([]string{"v1.3.0-rc.1", "v1.3.0", "v1.2.9"}, true)
		require.True(t, ok)
		require.Equal(t, "v1.3.0", tag)
	})

	t.Run("rejects partial and non-semver tags", func(t *testing.T) {
		t.Parallel()

		_, ok := SelectLatestReleaseTag([]string{"latest", "3.20", "edge"}, false)
		require.False(t, ok)
	})
}

func TestSelectLatestGitReleaseRef(t *testing.T) {
	t.Parallel()

	const headSHA = "1111111111111111111111111111111111111111"
	const releaseSHA = "2222222222222222222222222222222222222222"

	t.Run("selects greatest release tag", func(t *testing.T) {
		t.Parallel()

		ref, err := SelectLatestGitReleaseRef(&gitutil.Remote{
			Refs: []*gitutil.Ref{
				{Name: "refs/tags/v1.0.0", SHA: headSHA},
				{Name: "refs/tags/v1.1.0", SHA: releaseSHA},
			},
		}, false)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v1.1.0", ref.Name)
		require.Equal(t, releaseSHA, ref.SHA)
	})

	t.Run("falls back to head", func(t *testing.T) {
		t.Parallel()

		ref, err := SelectLatestGitReleaseRef(&gitutil.Remote{
			Refs: []*gitutil.Ref{
				{Name: "HEAD", SHA: headSHA},
				{Name: "refs/heads/main", SHA: headSHA},
			},
			Symrefs: map[string]string{"HEAD": "refs/heads/main"},
		}, false)
		require.NoError(t, err)
		require.Equal(t, "refs/heads/main", ref.Name)
		require.Equal(t, headSHA, ref.SHA)
	})

	t.Run("falls back to head when only prerelease tags are excluded", func(t *testing.T) {
		t.Parallel()

		ref, err := SelectLatestGitReleaseRef(&gitutil.Remote{
			Refs: []*gitutil.Ref{
				{Name: "HEAD", SHA: headSHA},
				{Name: "refs/heads/main", SHA: headSHA},
				{Name: "refs/tags/v2.0.0-rc.1", SHA: releaseSHA},
			},
			Symrefs: map[string]string{"HEAD": "refs/heads/main"},
		}, false)
		require.NoError(t, err)
		require.Equal(t, "refs/heads/main", ref.Name)
		require.Equal(t, headSHA, ref.SHA)
	})

	t.Run("selects prerelease tag when requested", func(t *testing.T) {
		t.Parallel()

		ref, err := SelectLatestGitReleaseRef(&gitutil.Remote{
			Refs: []*gitutil.Ref{
				{Name: "HEAD", SHA: headSHA},
				{Name: "refs/heads/main", SHA: headSHA},
				{Name: "refs/tags/v2.0.0-rc.1", SHA: releaseSHA},
			},
			Symrefs: map[string]string{"HEAD": "refs/heads/main"},
		}, true)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v2.0.0-rc.1", ref.Name)
		require.Equal(t, releaseSHA, ref.SHA)
	})
}
