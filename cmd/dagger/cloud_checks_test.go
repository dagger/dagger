package main

import (
	"testing"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

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

func TestCloudRowsForWorkspaceAddress(t *testing.T) {
	started := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	rows := cloudCheckRows("dagger", []cloudapi.CheckCommit{
		{
			Repo:      "https://github.com/acme/mono",
			CommitSHA: "abcdef123456",
			Timestamp: started,
			Refs: []cloudapi.CheckCommitRef{{
				Typename: "CheckCommitBranchRef",
				Name:     "main",
			}},
			Checks: []cloudapi.Check{{
				Name:      "lint",
				Status:    "success",
				StartedAt: &started,
				ModuleRef: "github.com/acme/mono/services/api",
			}},
		},
	})

	filtered, _, err := cloudRowsForWorkspaceAddress(
		t.Context(),
		rows,
		"github.com/acme/mono/services/api@main",
		nil,
	)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "green 1/1", cloudChecksSummary(filtered))
}

func TestCloudCheckWorkspaceAddress(t *testing.T) {
	row := cloudCheckRow{Dimensions: map[string]string{
		"workspace":  "github.com/acme/mono/services/api",
		"git-branch": "main",
	}}
	kind, address := cloudCheckWorkspaceAddress(row)
	require.Equal(t, "branch", kind)
	require.Equal(t, "github.com/acme/mono/services/api@main", address)

	row = cloudCheckRow{Dimensions: map[string]string{
		"github-repo": "acme/mono",
		"github-pr":   "42",
	}}
	kind, address = cloudCheckWorkspaceAddress(row)
	require.Equal(t, "pr", kind)
	require.Equal(t, "github.com/acme/mono@pull/42/head", address)
}
