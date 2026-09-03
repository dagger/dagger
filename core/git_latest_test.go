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

func TestSelectLatestGitRefWithTagPrefix(t *testing.T) {
	t.Parallel()

	const (
		rootCommit   = "0000000000000000000000000000000000000001"
		moduleCommit = "0000000000000000000000000000000000000002"
	)
	remote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "HEAD", SHA: rootCommit},
			{Name: "refs/heads/main", SHA: rootCommit},
			{Name: "refs/tags/v9.0.0", SHA: rootCommit},
			{Name: "refs/tags/other/v10.0.0", SHA: rootCommit},
			{Name: "refs/tags/module/v1.1.0", SHA: rootCommit},
			{Name: "refs/tags/module/v1.2.0", SHA: moduleCommit},
		},
		Symrefs: map[string]string{"HEAD": "refs/heads/main"},
	}

	ref, err := SelectLatestGitRefWithTagPrefix(remote, "module")
	require.NoError(t, err)
	require.Equal(t, "refs/tags/module/v1.2.0", ref.Name)
	require.Equal(t, moduleCommit, ref.SHA)

	ref, err = SelectLatestGitRefWithTagPrefix(remote, "missing")
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v9.0.0", ref.Name)
	require.Equal(t, rootCommit, ref.SHA)
}

func TestSelectLatestGitRefFallsBackToHead(t *testing.T) {
	t.Parallel()

	const headCommit = "0000000000000000000000000000000000000001"
	remote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "HEAD", SHA: headCommit},
			{Name: "refs/heads/main", SHA: headCommit},
			{Name: "refs/tags/nightly", SHA: "0000000000000000000000000000000000000002"},
		},
		Symrefs: map[string]string{"HEAD": "refs/heads/main"},
	}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/heads/main", ref.Name)
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

func TestSelectLatestGitRefRejectsEquivalentVersions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		refs []*gitutil.Ref
	}{
		{
			name: "optional v prefix",
			refs: []*gitutil.Ref{
				{Name: "refs/tags/1.2.3", SHA: "1111111111111111111111111111111111111111"},
				{Name: "refs/tags/v1.2.3", SHA: "2222222222222222222222222222222222222222"},
			},
		},
		{
			name: "calver and semver",
			refs: []*gitutil.Ref{
				{Name: "refs/tags/24.04", SHA: "1111111111111111111111111111111111111111"},
				{Name: "refs/tags/24.4.0", SHA: "2222222222222222222222222222222222222222"},
			},
		},
		{
			name: "zero-padded version",
			refs: []*gitutil.Ref{
				{Name: "refs/tags/01.002.0003", SHA: "1111111111111111111111111111111111111111"},
				{Name: "refs/tags/1.2.3", SHA: "2222222222222222222222222222222222222222"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			remote := &gitutil.Remote{Refs: tc.refs}
			_, err := SelectLatestGitRef(remote)
			require.ErrorContains(t, err, "ambiguous latest release")
		})
	}
}

func TestSelectLatestGitRefAcceptsEquivalentVersionsWithSameSHA(t *testing.T) {
	t.Parallel()

	const commit = "1111111111111111111111111111111111111111"
	remote := &gitutil.Remote{Refs: []*gitutil.Ref{
		{Name: "refs/tags/1.2", SHA: commit},
		{Name: "refs/tags/v1.2.0", SHA: commit},
		{Name: "refs/tags/01.02.000", SHA: commit},
	}}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v1.2.0", ref.Name)
	require.Equal(t, commit, ref.SHA)
}

func TestSelectLatestGitRefPrefersCompleteAliasAtDifferentCommit(t *testing.T) {
	t.Parallel()

	const completeCommit = "2222222222222222222222222222222222222222"
	remote := &gitutil.Remote{Refs: []*gitutil.Ref{
		{Name: "refs/tags/v1.2", SHA: "1111111111111111111111111111111111111111"},
		{Name: "refs/tags/v1.2.0", SHA: completeCommit},
	}}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v1.2.0", ref.Name)
	require.Equal(t, completeCommit, ref.SHA)
}

func TestSelectLatestGitRefAcceptsCalver(t *testing.T) {
	t.Parallel()

	const latestCommit = "2222222222222222222222222222222222222222"
	remote := &gitutil.Remote{Refs: []*gitutil.Ref{
		{Name: "refs/tags/24.04", SHA: "1111111111111111111111111111111111111111"},
		{Name: "refs/tags/25.10", SHA: latestCommit},
	}}

	ref, err := SelectLatestGitRef(remote)
	require.NoError(t, err)
	require.Equal(t, "refs/tags/25.10", ref.Name)
	require.Equal(t, latestCommit, ref.SHA)
}

func TestValidateGitLatestRef(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		ref     string
		wantErr string
	}{
		{name: "stable tag", ref: "refs/tags/v1.2.3"},
		{name: "stable tag without v", ref: "refs/tags/1.2.3"},
		{name: "incomplete tag", ref: "refs/tags/v1.2"},
		{name: "calver tag", ref: "refs/tags/24.04"},
		{name: "uncanonicalized head", ref: "HEAD", wantErr: "invalid git-latest ref"},
		{name: "default branch", ref: "refs/heads/main"},
		{name: "non-semver tag", ref: "refs/tags/latest", wantErr: "not a semantic version"},
		{name: "prerelease tag", ref: "refs/tags/v2.0.0-rc.1", wantErr: "prerelease tags are not supported"},
		{name: "arbitrary ref", ref: "refs/pull/1/head", wantErr: "invalid git-latest ref"},
		{name: "empty branch", ref: "refs/heads/", wantErr: "invalid git-latest ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateGitLatestRef(tc.ref, "")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateGitLatestRefWithTagPrefix(t *testing.T) {
	t.Parallel()

	err := ValidateGitLatestRef(
		"refs/tags/module/v1.2.3",
		"module",
	)
	require.NoError(t, err)

	err = ValidateGitLatestRef(
		"refs/tags/other/v1.2.3",
		"module",
	)
	require.ErrorContains(t, err, "not a semantic version")
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
