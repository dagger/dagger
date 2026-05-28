package main

import (
	"testing"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

func TestCloudListProjectionColumns(t *testing.T) {
	rows := []cloudCheckRow{
		{Dimensions: map[string]string{"github-repo": "acme/hello", "github-pr": "1", "git-sha": "aaa", "check": "lint"}},
		{Dimensions: map[string]string{"github-repo": "acme/hello", "git-branch": "main", "git-sha": "bbb", "check": "lint"}},
		{Dimensions: map[string]string{"github-repo": "acme/hello", "git-branch": "main", "git-sha": "bbb", "check": "test"}},
	}

	require.Equal(t,
		[]string{"github-pr", "git-branch", "git-sha", "check"},
		cloudListProjectionColumns("check", cloudCheckSelectorFlags{GitHubRepo: []string{"acme/hello"}}, rows),
	)
	require.Equal(t,
		[]string{"github-pr", "git-sha"},
		cloudListProjectionColumns("github-pr", cloudCheckSelectorFlags{GitHubRepo: []string{"acme/hello"}}, rows),
	)
	require.Equal(t,
		[]string{"github-repo"},
		cloudListProjectionColumns("github-repo", cloudCheckSelectorFlags{}, rows),
	)
}

func TestCloudCheckRowsAndSelectors(t *testing.T) {
	started := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	duration := 12
	rows := cloudCheckRows("dagger", []cloudapi.CheckCommit{
		{
			Repo:      "https://github.com/acme/hello",
			CommitSHA: "abcdef123456",
			Timestamp: started,
			Refs: []cloudapi.CheckCommitRef{{
				Typename: "CheckCommitPullRequestRef",
				Number:   4242,
			}},
			Checks: []cloudapi.Check{{
				Name:      "lint",
				Status:    "success",
				StartedAt: &started,
				Duration:  &duration,
				TraceID:   "trace123",
				ModuleRef: "github.com/acme/hello",
			}},
		},
	})

	require.Len(t, rows, 1)
	require.Equal(t, "acme/hello", rows[0].Dimensions["github-repo"])
	require.Equal(t, "4242", rows[0].Dimensions["github-pr"])
	require.Equal(t, "lint", rows[0].Dimensions["check"])
	require.Equal(t, "green", rows[0].Result)

	filtered := filterCloudCheckRows(rows, cloudCheckSelectorFlags{
		GitHubRepo: []string{"github.com/acme/hello"},
		GitHubPR:   []string{"4242"},
		GitSHA:     []string{"abcdef"},
		Check:      []string{"lint"},
	})
	require.Len(t, filtered, 1)
}
