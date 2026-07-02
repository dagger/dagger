package daggercmd

import (
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

func cloudRerunCheckList() []cloudapi.Check {
	return []cloudapi.Check{
		{ID: "1", Name: "ci:bootstrap", Status: "FAILURE"},
		{ID: "2", Name: "lint", Status: "SUCCESS"},
		{ID: "3", Name: "test", Status: "FAILURE"},
	}
}

// setCloudRerunFlags sets the package-level selector flags for one test case and
// restores them afterward, since cloudRerunTargets reads them directly.
func setCloudRerunFlags(t *testing.T, checks []string, all bool) {
	t.Helper()
	prevChecks, prevAll := cloudRerunChecks, cloudRerunAll
	cloudRerunChecks, cloudRerunAll = checks, all
	t.Cleanup(func() { cloudRerunChecks, cloudRerunAll = prevChecks, prevAll })
}

func TestCloudRerunTargetsDefaultsToFailed(t *testing.T) {
	setCloudRerunFlags(t, nil, false)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"ci:bootstrap", "test"}, cloudCheckNames(targets))
}

func TestCloudRerunTargetsAll(t *testing.T) {
	setCloudRerunFlags(t, nil, true)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"ci:bootstrap", "lint", "test"}, cloudCheckNames(targets))
}

func TestCloudRerunTargetsByName(t *testing.T) {
	setCloudRerunFlags(t, []string{"lint"}, false)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"lint"}, cloudCheckNames(targets))
}

func TestCloudRerunTargetsUnknownNameErrors(t *testing.T) {
	setCloudRerunFlags(t, []string{"ci:bootstrap:lint"}, false)
	_, err := cloudRerunTargets(cloudRerunCheckList())
	require.ErrorContains(t, err, "ci:bootstrap:lint")
	require.ErrorContains(t, err, "available:")
}

func TestCloudRerunTargetsNoFailuresErrors(t *testing.T) {
	setCloudRerunFlags(t, nil, false)
	_, err := cloudRerunTargets([]cloudapi.Check{
		{ID: "1", Name: "lint", Status: "SUCCESS"},
	})
	require.ErrorIs(t, err, errNoCloudRerunTargets)
}

func TestCloudRerunTargetsIncludesFailedLoad(t *testing.T) {
	setCloudRerunFlags(t, nil, false)
	targets, err := cloudRerunTargets([]cloudapi.Check{
		{ID: "1", Name: cloudLoadCheckName, Status: "FAILURE"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{cloudLoadCheckName}, cloudCheckNames(targets))
}

func TestCloudRerunPlanRoutesAndSkips(t *testing.T) {
	checkTargets, loadTargets, skipped := cloudRerunPlan([]cloudapi.Check{
		{ID: "1", Name: "ci:bootstrap", Status: "FAILURE"},
		{ID: "2", Name: cloudLoadCheckName, Status: "FAILURE"},
	})
	require.Equal(t, []string{"ci:bootstrap"}, cloudCheckNames(checkTargets))
	require.Equal(t, []string{cloudLoadCheckName}, cloudCheckNames(loadTargets))
	require.Empty(t, skipped)
}

func TestCloudRerunPlanSkipsPassedLoad(t *testing.T) {
	checkTargets, loadTargets, skipped := cloudRerunPlan([]cloudapi.Check{
		{ID: "1", Name: cloudLoadCheckName, Status: "SUCCESS"},
	})
	require.Empty(t, checkTargets)
	require.Empty(t, loadTargets)
	require.Contains(t, skipped[cloudLoadCheckName], "only a failed load check")
}

func TestCloudRerunRefLabelPreferspr(t *testing.T) {
	commit := cloudapi.CheckCommit{
		Repo:      "https://github.com/acme/hello",
		CommitSHA: "abcdef1234567890",
		Refs: []cloudapi.CheckCommitRef{{
			Typename: "CheckCommitPullRequestRef",
			Number:   42,
		}},
	}
	require.Equal(t, "PR #42 (acme/hello@abcdef123456)", cloudRerunRefLabel(commit))
}

func TestCloudRerunRefLabelFallsBackToSHA(t *testing.T) {
	commit := cloudapi.CheckCommit{
		Repo:      "https://github.com/acme/hello",
		CommitSHA: "abcdef1234567890",
		Refs: []cloudapi.CheckCommitRef{{
			Typename: "CheckCommitBranchRef",
			Name:     "main",
		}},
	}
	require.Equal(t, "acme/hello@abcdef123456", cloudRerunRefLabel(commit))
}

func TestCloudChecksPageURL(t *testing.T) {
	check := cloudapi.Check{
		Name:          "ci:bootstrap",
		ModuleRef:     "github.com/acme/hello",
		ModuleVersion: "abcdef123456",
	}
	require.Equal(t,
		"https://dagger.cloud/acme/checks/github.com/acme/hello@abcdef123456?check=ci:bootstrap",
		cloudChecksPageURL("acme", check))
}

func TestCloudChecksPageURLEmptyWithoutModule(t *testing.T) {
	require.Empty(t, cloudChecksPageURL("acme", cloudapi.Check{Name: "lint"}))
}
