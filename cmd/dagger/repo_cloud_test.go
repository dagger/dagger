package main

import (
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

func TestNormalizeGitRepo(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "github host",
			ref:  "github.com/dagger/dagger",
			want: "github.com/dagger/dagger",
		},
		{
			name: "https url",
			ref:  "https://github.com/dagger/dagger.git",
			want: "github.com/dagger/dagger",
		},
		{
			name: "ssh url",
			ref:  "git@github.com:dagger/dagger.git",
			want: "github.com/dagger/dagger",
		},
		{
			name: "trailing slash",
			ref:  "https://github.com/dagger/dagger/",
			want: "github.com/dagger/dagger",
		},
		{
			name: "git suffix with trailing slash",
			ref:  "https://github.com/dagger/dagger.git/",
			want: "github.com/dagger/dagger",
		},
		{
			name: "https url with query",
			ref:  "https://github.com/dagger/dagger.git?access_token=secret#fragment",
			want: "github.com/dagger/dagger",
		},
		{
			name: "github host with query",
			ref:  "github.com/dagger/dagger?access_token=secret",
			want: "github.com/dagger/dagger",
		},
		{
			name: "gitlab url",
			ref:  "https://gitlab.com/dagger/dagger.git",
			want: "gitlab.com/dagger/dagger",
		},
		{
			name: "gitlab nested group",
			ref:  "git@gitlab.com:dagger/subgroup/dagger.git",
			want: "gitlab.com/dagger/subgroup/dagger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeGitRepo(tt.ref)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeGitRepoRejectsInvalidRefs(t *testing.T) {
	tests := []string{
		"",
		"dagger/dagger",
		"github.com/dagger",
		"github.com/dagger/dagger/tree/main",
		"https://gitlab.com/dagger/dagger/-/tree/main",
		"git@github.com:dagger/dagger/tree/main",
	}

	for _, ref := range tests {
		t.Run(ref, func(t *testing.T) {
			_, err := normalizeGitRepo(ref)
			require.Error(t, err)
		})
	}
}

func TestSourceMatchesRepo(t *testing.T) {
	githubSource := &cloudapi.Source{
		Owner:     "dagger",
		ConfigURL: "https://github.com/settings/installations/123",
		Type:      "Organization",
	}
	require.True(t, sourceMatchesRepo(githubSource, "github.com/dagger/dagger"))
	require.False(t, sourceMatchesRepo(githubSource, "gitlab.com/dagger/dagger"))

	genericSource := &cloudapi.Source{
		Owner:     "dagger",
		ConfigURL: "https://gitlab.com/dagger",
		Type:      "Organization",
	}
	require.True(t, sourceMatchesRepo(genericSource, "gitlab.com/dagger/dagger"))
}

func TestIntegrationSupportsAutocheck(t *testing.T) {
	require.True(t, integrationSupportsAutocheck(cloudapi.Integration{Name: "GitHub"}))
	require.False(t, integrationSupportsAutocheck(cloudapi.Integration{Name: "GitLab"}))
	require.False(t, integrationSupportsAutocheck(cloudapi.Integration{Name: "Bitbucket"}))
}

func TestRedactGitRemote(t *testing.T) {
	got := redactGitRemote("https://token:x-oauth-basic@github.com/dagger/dagger.git")
	require.Equal(t, "https://github.com/dagger/dagger.git", got)
}

func TestRedactGitRemoteDropsQueryAndFragment(t *testing.T) {
	got := redactGitRemote("https://token:x-oauth-basic@github.com/dagger/dagger.git?access_token=secret#fragment")
	require.Equal(t, "https://github.com/dagger/dagger.git", got)

	got = redactGitRemote("github.com/dagger/dagger?access_token=secret#fragment")
	require.Equal(t, "github.com/dagger/dagger", got)
}
