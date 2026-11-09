package daggercmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloudChecksOnGitLab(t *testing.T) {
	const repo = "gitlab.com/Team/Subgroup/PrivateProject"
	const sources = `{"gitlabOrganizationSources":[{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"Team/Subgroup"}]}`
	for _, tc := range []struct {
		name, sources, available, repositories, features, grantError, writeError, wantError string
		wantGrant                                                                           bool
	}{
		{name: "uses the organization's saved namespace access", sources: sources, repositories: `{"sourceRepositories":[{"repository":"` + repo + `","eligible":true}]}`},
		{name: "uses the connected account when explicitly enabling a repository", sources: `{"gitlabOrganizationSources":[]}`, available: `{"gitlabSources":[{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"Team/Subgroup","access":null}]}`, repositories: `{"sourceRepositories":[{"repository":"` + repo + `","eligible":true}]}`, wantGrant: true},
		{name: "a refused personal grant cannot enable checks", sources: `{"gitlabOrganizationSources":[]}`, available: `{"gitlabSources":[{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"Team/Subgroup","access":null}]}`, repositories: `{"sourceRepositories":[{"repository":"` + repo + `","eligible":true}]}`, grantError: `{"errors":[{"message":"only organization admins can configure GitLab"}]}`, wantError: "only organization admins", wantGrant: true},
		{name: "missing organization access opens Cloud setup", sources: `{"gitlabOrganizationSources":[]}`, wantError: "/example/integrations/gitlab"},
		{name: "does not select the same namespace from another server", sources: `{"gitlabOrganizationSources":[{"id":"gitlab:42","instanceURL":"https://another.gitlab.test","namespacePath":"Team/Subgroup"}]}`, wantError: "/example/integrations/gitlab"},
		{name: "does not treat a parent group as the subgroup source", sources: `{"gitlabOrganizationSources":[{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"Team"}]}`, wantError: "/example/integrations/gitlab"},
		{name: "namespace spelling is preserved", sources: `{"gitlabOrganizationSources":[{"id":"gitlab:42","instanceURL":"https://gitlab.com","namespacePath":"team/subgroup"}]}`, wantError: "/example/integrations/gitlab"},
		{name: "does not select a repository the API marks ineligible", sources: sources, repositories: `{"sourceRepositories":[{"repository":"` + repo + `","eligible":false}]}`, wantError: "already enabled through another Cloud source or organization"},
		{name: "organization pilot is required", features: featuresActive, wantError: "CLOUD_GITLAB is not enabled"},
		{name: "a missing repository opens Cloud settings", sources: sources, repositories: `{"sourceRepositories":[]}`, wantError: "/example/integrations/gitlab"},
		{name: "reports an API authorization rejection", sources: sources, repositories: `{"sourceRepositories":[{"repository":"` + repo + `","eligible":true}]}`, writeError: `{"errors":[{"message":"only organization admins can configure GitLab"}]}`, wantError: "only organization admins"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := startFakeCloudChecks(t, func(operation string, variables map[string]any) string {
				switch operation {
				case "GetUserRepositories":
					return `{"user":{"repositories":[]}}`
				case "OrgByName":
					return `{"org":{"id":"org","name":"example"}}`
				case "GetOrgDetails":
					if tc.features != "" {
						return tc.features
					}
					return strings.Replace(featuresActive, `"features":[`, `"features":[{"name":"CLOUD_GITLAB","status":"ACTIVE"},`, 1)
				case "GetGitLabOrganizationSources":
					require.Equal(t, "org", variables["org"])
					return tc.sources
				case "GetGitLabSources":
					if tc.available != "" {
						return tc.available
					}
					return `{"gitlabSources":[]}`
				case "UseGitLabAccount":
					require.Equal(t, "org", variables["org"])
					require.Equal(t, "gitlab:42", variables["source"])
					if tc.grantError != "" {
						return tc.grantError
					}
					return `{"useGitLabAccount":{"connectionID":"connection"}}`
				case "GetSourceRepositories":
					require.Equal(t, "org", variables["org"])
					require.Equal(t, "gitlab:42", variables["installationId"])
					return tc.repositories
				case "UpdateOrgRepoSettings":
					if tc.writeError != "" {
						return tc.writeError
					}
					return `{"updateOrgRepoSettings":true}`
				}
				return ""
			})
			cloudOrgFlag = "example"
			out, err := runCloudChecksOn(t, true, "git@gitlab.com:Team/Subgroup/PrivateProject.git")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Empty(t, out)
				if tc.writeError == "" {
					require.NotContains(t, fake.operations, "UpdateOrgRepoSettings")
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, "checks for repo "+repo+" enabled\n", out)
				requireRepoSetting(t, fake, "org", map[string]any{"repo": repo, "sourceId": "gitlab:42", "runChecksOnDefaultBranch": true, "runChecksOnPullRequest": true})
			}
			require.Equal(t, tc.wantGrant, slices.Contains(fake.operations, "UseGitLabAccount"))
			require.NotContains(t, fake.operations, "GetSources", "GitLab never requires GitHub discovery")
		})
	}
}

func TestGitLabIntegrationURL(t *testing.T) {
	for _, tc := range []struct{ api, frontend, want string }{
		{"", "", "https://dagger.cloud/example/integrations/gitlab"},
		{"https://feature-api.preview.dagger.cloud", "", "https://feature-v3.preview.dagger.cloud/example/integrations/gitlab"},
		{"http://localhost:8020", "", "http://localhost:3000/example/integrations/gitlab"},
		{"https://custom-api.example", "https://custom.example", "https://custom.example/example/integrations/gitlab"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Setenv("DAGGER_CLOUD_URL", tc.api)
			t.Setenv("DAGGER_CLOUD_EXTERNAL_URL", tc.frontend)
			got, err := gitLabIntegrationURL("example")
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
