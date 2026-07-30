package core

import (
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestSelectLatestGitRef(t *testing.T) {
	t.Parallel()

	const (
		headCommit   = "0000000000000000000000000000000000000001"
		stableCommit = "0000000000000000000000000000000000000002"
	)
	remote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "HEAD", SHA: headCommit},
			{Name: "refs/heads/main", SHA: headCommit},
			{Name: "refs/tags/v1.9.0", SHA: "0000000000000000000000000000000000000004"},
			{Name: "refs/tags/2.0.0", SHA: stableCommit},
			{Name: "refs/tags/v3.0.0-rc.1", SHA: "0000000000000000000000000000000000000003"},
			{Name: "refs/tags/not-a-release", SHA: "0000000000000000000000000000000000000005"},
		},
		Symrefs: map[string]string{"HEAD": "refs/heads/main"},
	}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/tags/2.0.0", ref.Name)
	require.Equal(t, stableCommit, ref.SHA)
}

func TestSelectLatestGitRefFallsBackToHead(t *testing.T) {
	t.Parallel()

	const headCommit = "0000000000000000000000000000000000000001"
	remote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "HEAD", SHA: headCommit},
			{Name: "refs/heads/trunk", SHA: headCommit},
			{Name: "refs/tags/nightly", SHA: "0000000000000000000000000000000000000002"},
		},
		Symrefs: map[string]string{"HEAD": "refs/heads/trunk"},
	}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/heads/trunk", ref.Name)
	require.Equal(t, headCommit, ref.SHA)
}

func TestSelectLatestGitRefAnnotatedTag(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	remote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "refs/tags/v1.2.3", SHA: "1111111111111111111111111111111111111111"},
			{Name: "refs/tags/v1.2.3^{}", SHA: commit},
		},
	}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v1.2.3", ref.Name)
	require.Equal(t, commit, ref.SHA)
}

func TestSelectLatestGitRefOnlyPrereleasesFallsBackToHead(t *testing.T) {
	t.Parallel()

	const headCommit = "0123456789abcdef0123456789abcdef01234567"
	remote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "HEAD", SHA: headCommit},
			{Name: "refs/heads/main", SHA: headCommit},
			{Name: "refs/tags/v2.0.0-rc.1", SHA: "1111111111111111111111111111111111111111"},
		},
		Symrefs: map[string]string{"HEAD": "refs/heads/main"},
	}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/heads/main", ref.Name)
	require.Equal(t, headCommit, ref.SHA)
}

func TestSelectLatestGitRefEquivalentVersionsDeterministic(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	for _, refs := range [][]*gitutil.Ref{
		{
			{Name: "refs/tags/1.2.3", SHA: "1111111111111111111111111111111111111111"},
			{Name: "refs/tags/v1.2.3", SHA: commit},
		},
		{
			{Name: "refs/tags/v1.2.3", SHA: commit},
			{Name: "refs/tags/1.2.3", SHA: "1111111111111111111111111111111111111111"},
		},
	} {
		remote := &gitutil.Remote{Refs: refs}
		ref, err := SelectLatestGitRef(remote)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v1.2.3", ref.Name)
		require.Equal(t, commit, ref.SHA)
	}
}

func TestGitRefPinRoundTrip(t *testing.T) {
	t.Parallel()

	ref := &gitutil.Ref{
		Name: "refs/tags/v1.2.3",
		SHA:  "0123456789abcdef0123456789abcdef01234567",
	}
	pin, err := EncodeGitRefPin(ref)
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v1.2.3@0123456789abcdef0123456789abcdef01234567", pin)

	decoded, err := DecodeGitRefPin(pin)
	require.NoError(t, err)
	require.Equal(t, ref, decoded)
}

func TestGitRefPinRoundTripWithAtInRefName(t *testing.T) {
	t.Parallel()

	ref := &gitutil.Ref{
		Name: "refs/tags/release@v1.2.3",
		SHA:  "0123456789abcdef0123456789abcdef01234567",
	}
	pin, err := EncodeGitRefPin(ref)
	require.NoError(t, err)

	decoded, err := DecodeGitRefPin(pin)
	require.NoError(t, err)
	require.Equal(t, ref, decoded)
}

func TestDecodeGitRefPinRejectsInvalidPins(t *testing.T) {
	t.Parallel()

	for _, pin := range []string{
		"",
		"refs/tags/v1.2.3",
		"@0123456789abcdef0123456789abcdef01234567",
		"refs/tags/v1.2.3@not-a-commit",
	} {
		_, err := DecodeGitRefPin(pin)
		require.Error(t, err, pin)
	}
}

func TestDecodeGitLatestRefPinValidatesSelectedRef(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name    string
		ref     string
		wantErr string
	}{
		{name: "stable tag", ref: "refs/tags/v1.2.3"},
		{name: "stable tag without v", ref: "refs/tags/1.2.3"},
		{name: "head", ref: "HEAD"},
		{name: "default branch", ref: "refs/heads/main"},
		{name: "non-semver tag", ref: "refs/tags/latest", wantErr: "not a semantic version"},
		{name: "prerelease tag", ref: "refs/tags/v2.0.0-rc.1", wantErr: "prerelease tags are not supported"},
		{name: "arbitrary ref", ref: "refs/pull/1/head", wantErr: "invalid git.latest ref"},
		{name: "empty branch", ref: "refs/heads/", wantErr: "invalid git.latest ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ref, err := DecodeGitLatestRefPin(tc.ref + "@" + commit)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.ref, ref.Name)
			require.Equal(t, commit, ref.SHA)
		})
	}
}

func TestGitRepositoryCloneWithBackendResetsRemoteMetadata(t *testing.T) {
	t.Parallel()

	const headCommit = "0123456789abcdef0123456789abcdef01234567"
	backend := &RemoteGitRepository{}
	repo := &GitRepository{
		Backend: backend,
		Remote: &gitutil.Remote{
			Head:    &gitutil.Ref{Name: "refs/heads/main", SHA: headCommit},
			Refs:    []*gitutil.Ref{{Name: "refs/tags/v1.2.3", SHA: headCommit}},
			Symrefs: map[string]string{"HEAD": "refs/heads/main"},
		},
		DiscardGitDir: true,
	}

	clone := repo.CloneWithBackend(backend)
	require.NotSame(t, repo, clone)
	require.Same(t, backend, clone.Backend)
	require.True(t, clone.DiscardGitDir)
	require.Empty(t, clone.Remote.Refs)
	require.Empty(t, clone.Remote.Symrefs)
	require.Equal(t, repo.Remote.Head, clone.Remote.Head)
	require.NotSame(t, repo.Remote.Head, clone.Remote.Head)
}
