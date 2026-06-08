package main

import (
	"fmt"
	"io"
	"testing"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	telemetry "github.com/dagger/otel-go"
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
				URL:      "https://github.com/acme/hello/pull/4242",
				Title:    "Add hello checks",
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
	require.Equal(t, "https://github.com/acme/hello/pull/4242", rows[0].Dimensions["url"])
	require.Equal(t, "Add hello checks", rows[0].Dimensions["description"])
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

func TestCloudChecksEmojiSummary(t *testing.T) {
	rows := []cloudCheckRow{
		{Dimensions: map[string]string{"check": "lint"}, Result: "green"},
		{Dimensions: map[string]string{"check": "unit"}, Result: "red"},
		{Dimensions: map[string]string{"check": "docs"}, Result: "pending"},
		{Dimensions: map[string]string{"check": "deploy"}, Result: "pending"},
	}
	require.Equal(t, "🟡2 🔴1 🟢1", cloudChecksEmojiSummary(rows))
}

func TestWorkspaceActivityRowsIncludePRMetadata(t *testing.T) {
	started := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	rows := cloudCheckRows("dagger", []cloudapi.CheckCommit{{
		Repo:      "https://github.com/acme/mono",
		CommitSHA: "abcdef123456",
		Timestamp: started,
		Refs: []cloudapi.CheckCommitRef{{
			Typename: "CheckCommitPullRequestRef",
			Number:   42,
			URL:      "https://github.com/acme/mono/pull/42",
			Title:    "Add workspace activity",
		}},
		Checks: []cloudapi.Check{{
			Name:      "lint",
			Status:    "success",
			StartedAt: &started,
			ModuleRef: "github.com/acme/mono",
		}},
	}})

	activityRows := workspaceActivityRows(rows)
	require.Len(t, activityRows, 1)
	require.Equal(t, "https://github.com/acme/mono/pull/42", activityRows[0].URL)
	require.Equal(t, "Add workspace activity", activityRows[0].Description)
	require.Equal(t, "🟢1", activityRows[0].Checks)
}

func TestWorkspaceActivityRowsUseCommitMessageDescription(t *testing.T) {
	started := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	rows := cloudCheckRows("dagger", []cloudapi.CheckCommit{{
		Repo:          "https://github.com/acme/mono",
		CommitSHA:     "abcdef123456",
		CommitMessage: "Update workspace docs\n\nSigned-off-by: Ava",
		Timestamp:     started,
		Refs: []cloudapi.CheckCommitRef{{
			Typename: "CheckCommitBranchRef",
			Name:     "main",
		}},
		Checks: []cloudapi.Check{{
			Name:      "lint",
			Status:    "success",
			StartedAt: &started,
			ModuleRef: "github.com/acme/mono",
		}},
	}})

	activityRows := workspaceActivityRows(rows)
	require.Len(t, activityRows, 1)
	require.Equal(t, "Update workspace docs", activityRows[0].Description)
}

func TestSyntheticCloudCheckSpanMarksCheckStatus(t *testing.T) {
	started := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	span, _, _ := syntheticCloudCheckSpan("trace", "span", cloudapi.Check{
		Name:      "lint",
		Status:    "success",
		StartedAt: &started,
	}, started)
	require.Equal(t, "lint", span.Attributes[telemetry.CheckNameAttr])
	require.Equal(t, true, span.Attributes[telemetry.CheckPassedAttr])

	span, _, _ = syntheticCloudCheckSpan("trace", "span", cloudapi.Check{
		Name:      "unit",
		Status:    "failure",
		StartedAt: &started,
	}, started)
	require.Equal(t, "unit", span.Attributes[telemetry.CheckNameAttr])
	require.Equal(t, false, span.Attributes[telemetry.CheckPassedAttr])
}

func TestCloudCheckReplayFrontendFollowsProgressMode(t *testing.T) {
	originalProgress := progress
	t.Cleanup(func() {
		progress = originalProgress
	})

	progress = "plain"
	require.Contains(t, fmt.Sprintf("%T", newCloudCheckReplayFrontend(io.Discard)), "frontendPlain")

	progress = "dots"
	require.Contains(t, fmt.Sprintf("%T", newCloudCheckReplayFrontend(io.Discard)), "frontendDots")

	progress = "logs"
	require.Contains(t, fmt.Sprintf("%T", newCloudCheckReplayFrontend(io.Discard)), "frontendLogs")

	progress = "tty"
	require.Contains(t, fmt.Sprintf("%T", newCloudCheckReplayFrontend(io.Discard)), "frontendPretty")

	progress = "report"
	require.Contains(t, fmt.Sprintf("%T", newCloudCheckReplayFrontend(io.Discard)), "frontendPretty")
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
