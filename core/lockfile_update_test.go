package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestUpdateWorkspaceLockEntry(t *testing.T) {
	t.Parallel()

	_, err := updateWorkspaceLockEntry(context.Background(), nil, workspace.LookupEntry{
		Namespace: "acme",
		Operation: "resolve",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported lock entry "acme" "resolve"`)
}

func TestParseGitLatestLockPin(t *testing.T) {
	t.Parallel()

	t.Run("accepts release tag", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseGitLatestLockPin("refs/tags/v1.2.3@0123456789abcdef0123456789abcdef01234567", false)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v1.2.3", ref.Name)
		require.Equal(t, "0123456789abcdef0123456789abcdef01234567", ref.SHA)
	})

	t.Run("accepts head fallback", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/heads/main@0123456789abcdef0123456789abcdef01234567", false)
		require.NoError(t, err)

		_, err = ParseGitLatestLockPin("HEAD@0123456789abcdef0123456789abcdef01234567", false)
		require.NoError(t, err)
	})

	t.Run("rejects non-release tag", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/tags/latest@0123456789abcdef0123456789abcdef01234567", false)
		require.ErrorContains(t, err, "not a release tag")
	})

	t.Run("honors prerelease option", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/tags/v1.2.3-rc.1@0123456789abcdef0123456789abcdef01234567", false)
		require.ErrorContains(t, err, "not a release tag")

		_, err = ParseGitLatestLockPin("refs/tags/v1.2.3-rc.1@0123456789abcdef0123456789abcdef01234567", true)
		require.NoError(t, err)
	})

	t.Run("rejects unrelated refs", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/pull/1/head@0123456789abcdef0123456789abcdef01234567", false)
		require.ErrorContains(t, err, "must be a release tag or HEAD branch")
	})

	t.Run("rejects invalid pin", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/tags/v1.2.3@not-a-sha", false)
		require.ErrorContains(t, err, "invalid commit sha")
	})
}
