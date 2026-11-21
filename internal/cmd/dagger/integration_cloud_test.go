package daggercmd

import (
	"bytes"
	"slices"
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestIntegrationAccountEntriesFromSources(t *testing.T) {
	orgName := "dagger"
	githubSupportsChecks, otherSupportsChecks := true, false
	entries := integrationAccountEntriesFromSources([]cloudapi.Source{
		{
			Name:         "dagger",
			ID:           "123",
			Type:         "Organization",
			OrgName:      &orgName,
			ConfigURL:    "https://github.com/organizations/dagger/settings/installations/123",
			ConfiguredAt: "2026-05-26T00:00:00Z",
		},
		{
			Name:      "acme",
			ID:        "456",
			Type:      "Organization",
			ConfigURL: "https://gitlab.com/acme",
		},
	})

	require.Equal(t, []integrationAccountEntry{
		{
			ID:           "123",
			Provider:     "GitHub",
			Account:      "dagger",
			Type:         "Organization",
			Org:          "dagger",
			ConfiguredAt: "2026-05-26T00:00:00Z",
			Autocheck:    &githubSupportsChecks,
			ConfigURL:    "https://github.com/organizations/dagger/settings/installations/123",
		},
		{
			ID:        "456",
			Provider:  "GitLab",
			Account:   "acme",
			Type:      "Organization",
			Autocheck: &otherSupportsChecks,
			ConfigURL: "https://gitlab.com/acme",
		},
	}, entries)

	require.Equal(t, entries[:1], filterIntegrationAccountEntries(entries, "github"))
}

func TestIntegrationSetup(t *testing.T) {
	for _, tc := range []struct {
		name, provider, want, gitHubConnection string
		namespace, gitLabSources               string
		wantGrant                              bool
	}{
		{
			name:             "GitHub account authorization",
			provider:         "github",
			gitHubConnection: `{"githubConnection":null}`,
			want:             `{"status":"action_required","provider":"github","org":"example","action":"authorize_account","url":"https://github.com/login/oauth/authorize?state=test","redirectURI":"https://cloud.example/github/callback"}`,
		},
		{
			name:             "GitHub app installation after account authorization",
			provider:         "github",
			gitHubConnection: `{"githubConnection":{"githubLogin":"octocat"}}`,
			want:             `{"status":"action_required","provider":"github","org":"example","action":"install_app","url":"https://cloud.example/example/integrations/github","githubLogin":"octocat"}`,
		},
		{
			name:     "GitLab account authorization",
			provider: "GitLab",
			want:     `{"status":"action_required","provider":"gitlab","org":"example","action":"authorize_account","url":"https://cloud.example/example/integrations/gitlab"}`,
		},
		{
			name:          "GitLab grant only updates the named GitLab.com namespace",
			provider:      "gitlab",
			namespace:     "team/demo",
			gitLabSources: `{"gitlabSources":[{"id":"gitlab:8","instanceURL":"https://another.gitlab.test","namespacePath":"team/demo"},{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"team/demo"}]}`,
			want:          `{"status":"connected","provider":"gitlab","org":"example","namespace":"team/demo","id":"gitlab:42"}`,
			wantGrant:     true,
		},
		{
			name:          "GitLab existing grant is preserved",
			provider:      "gitlab",
			namespace:     "team/demo",
			gitLabSources: `{"gitlabSources":[{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"team/demo","access":{"connectionID":""}}]}`,
			want:          `{"status":"connected","provider":"gitlab","org":"example","namespace":"team/demo","id":"gitlab:42"}`,
		},
		{
			name:          "GitLab unavailable namespace asks for authorization",
			provider:      "gitlab",
			namespace:     "team/demo",
			gitLabSources: `{"gitlabSources":[]}`,
			want:          `{"status":"action_required","provider":"gitlab","org":"example","action":"authorize_account","namespace":"team/demo","url":"https://cloud.example/example/integrations/gitlab"}`,
		},
		{
			name:     "Entire plugin installation",
			provider: "entire",
			want:     `{"status":"action_required","provider":"entire","org":"example","action":"install_plugin","url":"https://entire.com/install?state=test"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reject repository settings mutations and operations for unrelated forges.
			fake := startFakeCloudChecks(t, func(operation string, variables map[string]any) string {
				switch operation {
				case "User":
					return `{"user":{"orgs":[{"id":"org","name":"example"}]}}`
				case "OrgByName":
					require.Equal(t, "example", variables["name"])
					return `{"org":{"id":"org","name":"example"}}`
				case "GetEntireInstallURL":
					require.Equal(t, "entire", tc.provider)
					require.Equal(t, "org", variables["org"])
					require.Equal(t, "https://cloud.example/setup/entire", variables["redirectURI"])
					return `{"entireInstallURL":"https://entire.com/install?state=test"}`
				case "GetGithubConnection":
					return tc.gitHubConnection
				case "GetSources":
					require.NotEmpty(t, tc.gitHubConnection)
					return `{"errors":[{"message":"no GitHub identity found"}]}`
				case "GetGitLabSources":
					require.Equal(t, "org", variables["org"])
					return tc.gitLabSources
				case "UseGitLabAccount":
					require.True(t, tc.wantGrant)
					require.Equal(t, "org", variables["org"])
					require.Equal(t, "gitlab:42", variables["source"])
					return `{"useGitLabAccount":{"connectionID":"connection"}}`
				case "GetGithubOAuthURL":
					require.Equal(t, "https://cloud.example/github/callback", variables["redirectURI"])
					require.NotEmpty(t, tc.gitHubConnection)
					return `{"githubOAuthURL":"https://github.com/login/oauth/authorize?state=test"}`
				default:
					return ""
				}
			})
			t.Setenv("DAGGER_CLOUD_EXTERNAL_URL", "https://cloud.example")
			out := new(bytes.Buffer)
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			cmd.SetOut(out)
			oldJSON, oldNamespace := cloudJSON, integrationNamespace
			t.Cleanup(func() { cloudJSON, integrationNamespace = oldJSON, oldNamespace })
			cloudOrgFlag, cloudJSON, integrationNamespace = "example", true, tc.namespace

			require.NoError(t, (&CloudCLI{}).IntegrationSetup(cmd, []string{tc.provider}))
			require.JSONEq(t, tc.want, out.String())
			require.Equal(t, tc.wantGrant, slices.Contains(fake.operations, "UseGitLabAccount"))
		})
	}
}

func TestIntegrationListOrganizationAccess(t *testing.T) {
	for _, tc := range []struct {
		provider, operation, data, want string
	}{
		{
			provider:  "gitlab",
			operation: "GetGitLabOrganizationSources",
			data:      `{"gitlabOrganizationSources":[{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"acme/team"}]}`,
			want:      `[{"id":"gitlab:42","provider":"GitLab","account":"acme/team","type":"Namespace","org":"example","configUrl":"https://gitlab.com/acme/team"}]`,
		},
		{
			provider:  "entire",
			operation: "GetEntireInstallations",
			data:      `{"entireInstallations":[{"id":"entire:42","name":"acme","manageURL":"https://entire.com/et/acme/-/settings/plugins"}]}`,
			want:      `[{"id":"entire:42","provider":"Entire","account":"acme","type":"Installation","configUrl":"https://entire.com/et/acme/-/settings/plugins"}]`,
		},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			startFakeCloudChecks(t, func(operation string, variables map[string]any) string {
				switch operation {
				case "User":
					return `{"user":{"orgs":[{"id":"org","name":"example"}]}}`
				case "OrgByName":
					return `{"org":{"id":"org","name":"example"}}`
				case tc.operation:
					require.Equal(t, "org", variables["org"])
					return tc.data
				default:
					return ""
				}
			})
			out := new(bytes.Buffer)
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			cmd.SetOut(out)
			oldJSON := cloudJSON
			t.Cleanup(func() { cloudJSON = oldJSON })
			cloudOrgFlag, cloudJSON = "example", true

			require.NoError(t, (&CloudCLI{}).IntegrationList(cmd, []string{tc.provider}))
			require.JSONEq(t, tc.want, out.String())
		})
	}
}
