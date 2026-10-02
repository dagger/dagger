package daggercmd

import (
	"errors"
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
)

func user(orgs ...string) *cloudapi.UserResponse {
	u := &cloudapi.UserResponse{}
	for _, name := range orgs {
		u.Orgs = append(u.Orgs, cloudauth.Org{Name: name})
	}
	return u
}

func TestUserOrgMembershipError(t *testing.T) {
	repo := "github.com/dagger/dagger"

	t.Run("member returns nil", func(t *testing.T) {
		require.NoError(t, userOrgMembershipError(user("acme", "dagger"), "dagger", repo))
	})

	t.Run("member match is case-insensitive", func(t *testing.T) {
		require.NoError(t, userOrgMembershipError(user("Dagger"), "dagger", repo))
	})

	t.Run("non-member", func(t *testing.T) {
		err := userOrgMembershipError(user("acme", "widgets"), "dagger", repo)
		require.Error(t, err)
		require.Contains(t, err.Error(), repo)
		require.Contains(t, err.Error(), "owned by another Dagger Cloud organization")
		require.Contains(t, err.Error(), "not a member")
	})

	t.Run("non-member with no orgs", func(t *testing.T) {
		err := userOrgMembershipError(user(), "dagger", repo)
		require.Error(t, err)
		require.Contains(t, err.Error(), "owned by another Dagger Cloud organization")
	})
}

func TestGitHubAppInstallURL(t *testing.T) {
	const prod = "https://github.com/apps/dagger-cloud/installations/select_target"
	const dev = "https://github.com/apps/dagger-cloud-dev/installations/select_target"

	t.Run("production when DAGGER_CLOUD_URL unset", func(t *testing.T) {
		t.Setenv("DAGGER_CLOUD_URL", "")
		require.Equal(t, prod, gitHubAppInstallURL())
	})

	t.Run("production for api.dagger.cloud", func(t *testing.T) {
		t.Setenv("DAGGER_CLOUD_URL", "https://api.dagger.cloud")
		require.Equal(t, prod, gitHubAppInstallURL())
	})

	t.Run("dev for a local/non-prod API", func(t *testing.T) {
		t.Setenv("DAGGER_CLOUD_URL", "http://localhost:8020")
		require.Equal(t, dev, gitHubAppInstallURL())
	})
}

func TestGitHubAppNotInstalledError(t *testing.T) {
	t.Setenv("DAGGER_CLOUD_URL", "")
	err := gitHubAppNotInstalledError("github.com/dagger/hello-dagger")
	require.Error(t, err)
	require.Contains(t, err.Error(), `"dagger"`) // owner
	require.Contains(t, err.Error(), "not installed")
	require.Contains(t, err.Error(), "https://github.com/apps/dagger-cloud/installations/select_target")
}

func TestIsCloudUnauthorized(t *testing.T) {
	require.True(t, isCloudUnauthorized(errors.New("GetOrgMappedSources: unauthorized; unauthorized")))
	require.False(t, isCloudUnauthorized(errors.New("connection refused")))
	require.False(t, isCloudUnauthorized(nil))
}

func TestRepoAccessError(t *testing.T) {
	err := &repoAccessError{settingsURL: "https://github.com/settings/installations/12345"}
	require.EqualError(t, err,
		"Cloud checks need GitHub access to this repository. Visit https://github.com/settings/installations/12345 to enable it and then run the command again")
}

func TestInstallationSettingsURL(t *testing.T) {
	t.Run("prefers the source's config URL", func(t *testing.T) {
		url := installationSettingsURL(workspaceAutocheckState{
			InstallationID: "12345",
			ConfigURL:      "https://github.com/organizations/acme/settings/installations/12345",
		})
		require.Equal(t, "https://github.com/organizations/acme/settings/installations/12345", url)
	})
	t.Run("falls back to the canonical URL", func(t *testing.T) {
		url := installationSettingsURL(workspaceAutocheckState{InstallationID: "67890"})
		require.Equal(t, "https://github.com/settings/installations/67890", url)
	})
}

func TestIsRepoNotInInstallation(t *testing.T) {
	require.True(t, isRepoNotInInstallation(errors.New(
		"ConfigureSource: repository github.com/marcosnils/bin does not belong to installation 161781848. Verify that the GitHub app has been granted access to the repository.")))
	require.False(t, isRepoNotInInstallation(errors.New("unauthorized")))
	require.False(t, isRepoNotInInstallation(nil))
}
