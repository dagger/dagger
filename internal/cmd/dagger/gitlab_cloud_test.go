package daggercmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestGitLabWorkspaceSourcesPreserveInstanceAndNestedNamespace(t *testing.T) {
	sources := []cloudapi.Source{
		{ID: "12", Name: "team", Provider: "GITHUB", Instance: "https://github.com"},
		{ID: "gitlab:12", Name: "team", Provider: "GITLAB", Instance: "https://gitlab.com"},
		{ID: "gitlab:13", Name: "team/sub", Provider: "GITLAB", Instance: "https://gitlab.com"},
		{ID: "gitlab:14", Name: "team/sub", Provider: "GITLAB", Instance: "https://gitlab.example.com"},
		{ID: "gitlab:15", Name: "team/sub", Provider: "GITLAB", Instance: "http://localhost:8929"},
	}
	for _, tc := range []struct{ remote, id string }{
		{"git@gitlab.com:team/sub/project.git", "gitlab:13"},
		{"https://gitlab.example.com/team/sub/project.git", "gitlab:14"},
		{"http://localhost:8929/team/sub/project.git", "gitlab:15"},
		{"ssh://git@gitlab.com:2222/team/sub/project.git", "gitlab:13"},
		{"https://user:token@gitlab.example.com/team/sub/project.git#main", "gitlab:14"},
		{"github.com/team/project", "12"},
		{"team/project", "12"},
	} {
		source, ok := workspaceSourceForRepo(sources, tc.remote)
		require.True(t, ok, tc.remote)
		require.Equal(t, tc.id, source.ID)
	}
	_, ok := workspaceSourceForRepo(sources, "gitlab.com/team-other/project")
	require.False(t, ok)
	repos := setWorkspaceAutocheckRepoSelected([]string{"github.com/team/project", "gitlab.example.com/team/sub/project"}, "gitlab.com/team/sub/project", true)
	require.Equal(t, []string{"github.com/team/project", "gitlab.com/team/sub/project", "gitlab.example.com/team/sub/project"}, repos)
	repos = setWorkspaceAutocheckRepoSelected(repos, "git@gitlab.com:team/sub/project.git", false)
	require.Equal(t, []string{"github.com/team/project", "gitlab.example.com/team/sub/project"}, repos)
	require.Equal(t, "http://localhost:8929", repositoryInstance("http://localhost:8929/team/sub/project.git"))
	instance, err := registeredGitLabInstance(&cloudapi.GitLabSetup{Instances: []string{"https://gitlab.com", "http://localhost:8929"}}, "localhost:8929/team/sub/project")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8929", instance)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"sources":[],"gitlabInstances":["http://localhost:8929"],"gitlabConnections":[]}}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("DAGGER_CLOUD_URL", server.URL)
	client, err := cloudapi.NewClient(t.Context(), &auth.Cloud{Token: &oauth2.Token{AccessToken: "test-only", TokenType: "Bearer"}})
	require.NoError(t, err)
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	_, err = workspaceAutocheckStateFromSource(cmd, client, "http://localhost:8929/team/sub/project.git")
	require.ErrorIs(t, err, errCloudSourceNotConfigured)
	require.Contains(t, err.Error(), "--instance http://localhost:8929")
	require.NotContains(t, err.Error(), "https://localhost:8929")
}

// Exercise the actual built command's credential/config/argument handoff, using
// the same isolated CLI helper as the existing GitHub/account flow tests.
func TestGitLabIntegrationCommandHandoff(t *testing.T) {
	for _, tc := range []struct {
		name, instance string
		connected      bool
		reauthorize    bool
	}{
		{name: "public account remains connected", instance: "https://gitlab.com", connected: true},
		{name: "connect second local instance", instance: "http://localhost:8929"},
		{name: "reauthorize existing discovery account", instance: "https://gitlab.com", reauthorize: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var operations []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Operation string            `json:"operationName"`
					Variables map[string]string `json:"variables"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Equal(t, "Bearer test-login-token", r.Header.Get("Authorization"))
				operations = append(operations, request.Operation)
				w.Header().Set("Content-Type", "application/json")
				switch request.Operation {
				case "GetGitLabSetup":
					fmt.Fprint(w, `{"data":{"gitlabInstances":["https://gitlab.com","http://localhost:8929"],"gitlabConnections":[{"id":"public","instance":"https://gitlab.com","username":"public-user"}]}}`)
				case "GetGitLabOAuthURL":
					require.Equal(t, map[string]string{"instance": tc.instance, "redirectURI": "http://localhost:3001/gitlab/callback"}, request.Variables)
					fmt.Fprintf(w, `{"data":{"gitlabOAuthURL":%q}}`, tc.instance+"/oauth/authorize?state=test-only")
				default:
					t.Errorf("unexpected operation %s", request.Operation)
					http.Error(w, "unexpected operation", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			args := []string{"cloud", "integration", "create", "gitlab", "--instance", tc.instance, "--redirect-uri", "http://localhost:3001/gitlab/callback", "--json"}
			if tc.reauthorize {
				args = append(args, "--reauthorize")
			}
			daggerCloudWithConfig(t, []string{"DAGGER_CLOUD_URL=" + server.URL, "DAGGER_PROGRESS=report"}, args,
				map[string]string{"credentials.json": `{"access_token":"test-login-token","token_type":"Bearer"}`},
				func(t *testing.T, err error, out, stderr *bytes.Buffer) {
					require.NoError(t, err, stderr.String())
					var result map[string]string
					require.NoError(t, json.Unmarshal(out.Bytes(), &result))
					require.Equal(t, tc.instance, result["instance"])
					require.Equal(t, "gitlab", result["provider"])
					if tc.connected {
						require.Equal(t, "connected", result["status"])
						require.Equal(t, []string{"GetGitLabSetup"}, operations)
					} else {
						require.Equal(t, "http://localhost:3001/gitlab/callback", result["redirectURI"])
						require.Equal(t, tc.instance+"/oauth/authorize?state=test-only", result["url"])
						require.Equal(t, []string{"GetGitLabSetup", "GetGitLabOAuthURL"}, operations)
					}
				})
		})
	}
}

func TestGitLabConnectionsDoNotImplyGitHubConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Operation string `json:"operationName"`
			Variables struct{ Providers []cloudapi.GitProvider }
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		switch request.Operation {
		case "GetGithubConnection":
			fmt.Fprint(w, `{"data":{"githubConnection":null}}`)
		case "GetSources":
			if len(request.Variables.Providers) == 1 && request.Variables.Providers[0] == cloudapi.GitProviderGitHub {
				fmt.Fprint(w, `{"errors":[{"message":"no GitHub identity found"}]}`)
			} else {
				fmt.Fprint(w, `{"data":{"sources":[{"id":"gitlab:42","name":"team/sub","provider":"GITLAB","instance":"https://gitlab.example.com"}]}}`)
			}
		default:
			t.Errorf("unexpected operation %s", request.Operation)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("DAGGER_CLOUD_URL", server.URL)
	client, err := cloudapi.NewClient(t.Context(), &auth.Cloud{Token: &oauth2.Token{AccessToken: "test-only", TokenType: "Bearer"}})
	require.NoError(t, err)
	connected, _ := (&CloudCLI{}).githubConnected(t.Context(), client)
	require.False(t, connected)
}

func TestGitLabMergeRequestCheckRows(t *testing.T) {
	started := time.Now()
	rows := cloudCheckRows("test", []cloudapi.CheckCommit{{
		Repo: "https://gitlab.example.com/team/sub/project", CommitSHA: "abcdef123456", Timestamp: started,
		Events: []cloudapi.CheckEvent{{Provider: "gitlab"}},
		Refs:   []cloudapi.CheckCommitRef{{Typename: "CheckCommitPullRequestRef", Number: 7, URL: "https://gitlab.example.com/team/sub/project/-/merge_requests/7"}},
		Checks: []cloudapi.Check{{Name: "test", Status: "success", StartedAt: &started}},
	}})
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].Dimensions["github-repo"])
	require.Empty(t, rows[0].Dimensions["github-pr"])
	require.Equal(t, "7", rows[0].Dimensions["gitlab-mr"])
	filtered := filterCloudCheckRows(rows, cloudCheckSelectorFlags{GitRepo: []string{"git@gitlab.example.com:team/sub/project.git"}, GitLabMR: []string{"7"}})
	require.Len(t, filtered, 1)
	kind, address := cloudCheckWorkspaceAddress(rows[0])
	require.Equal(t, "mr", kind)
	require.Equal(t, "gitlab.example.com/team/sub/project@refs/merge-requests/7/head", address)
	require.Equal(t, "7", cloudMergeRequestNumber("refs/merge-requests/7/head"))
	require.Empty(t, cloudMergeRequestNumber("refs/merge-requests/not-a-number/head"))
}

func TestGitLabOAuthRedirectSupportsAlternateCloud(t *testing.T) {
	previous := integrationRedirectURI
	integrationRedirectURI = ""
	t.Cleanup(func() { integrationRedirectURI = previous })
	t.Setenv("DAGGER_CLOUD_EXTERNAL_URL", "https://cloud-test.example.com/")
	require.Equal(t, "https://cloud-test.example.com/gitlab/callback", gitlabOAuthRedirect())
	integrationRedirectURI = "http://localhost:3000/gitlab/callback"
	require.Equal(t, integrationRedirectURI, gitlabOAuthRedirect())
}
