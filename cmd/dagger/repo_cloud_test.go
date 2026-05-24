package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeGitHubRepo(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeGitHubRepo(tt.ref)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeGitHubRepoRejectsInvalidRefs(t *testing.T) {
	tests := []string{
		"",
		"dagger/dagger",
		"github.com/dagger",
		"github.com/dagger/dagger/tree/main",
		"https://gitlab.com/dagger/dagger",
		"git@gitlab.com:dagger/dagger.git",
		"git@github.com:dagger/dagger/tree/main",
	}

	for _, ref := range tests {
		t.Run(ref, func(t *testing.T) {
			_, err := normalizeGitHubRepo(ref)
			require.Error(t, err)
		})
	}
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
