package daggercmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCloudChecksCommandAndHiddenAlias(t *testing.T) {
	root := testRootCommand()
	for _, name := range []string{"checks", "check"} {
		cmd, args, err := root.Find([]string{"cloud", name})
		require.NoError(t, err)
		require.Empty(t, args)
		require.Same(t, cloudCheckCmd, cmd)
		require.Equal(t, "checks", cmd.Name())
		require.Empty(t, helpAliases(cmd))
		for _, subcommand := range []string{"on", "off", "status", "list", "rerun"} {
			cmd, args, err := root.Find([]string{"cloud", name, subcommand})
			require.NoError(t, err)
			require.Empty(t, args)
			require.Equal(t, "dagger cloud checks "+subcommand, cmd.CommandPath())
		}
	}
	require.NotContains(t, renderHelp(t, cloudCheckCmd), "ALIASES")
	require.NotContains(t, renderHelp(t, cloudCmd), "checks, check")
}

func TestCloudChecksCommandPrompt(t *testing.T) {
	oldOrg := cloudOrgFlag
	cloudOrgFlag = "team's org"
	t.Cleanup(func() { cloudOrgFlag = oldOrg })
	for _, args := range []string{"signup", "integration create github --open"} {
		command := cloudSetupCommand(args)
		require.Equal(t, "dagger cloud "+args+" --org 'team'\"'\"'s org'", command)
	}
}

func TestCloudChecksOnUnauthenticated(t *testing.T) {
	for _, name := range []string{"checks", "check"} {
		t.Run(name, func(t *testing.T) {
			daggerCloudWithEnv(t, nil, []string{"cloud", name, "on", "github.com/example/project"}, func(t *testing.T, err error, out, stderr *bytes.Buffer) {
				require.Error(t, err)
				require.Contains(t, stderr.String(), "dagger cloud signup\ndagger cloud checks on github.com/example/project")
				require.Empty(t, out.String())
			})
		})
	}
}

func TestCloudChecksPrerequisiteCommands(t *testing.T) {
	oldWorkspace, oldOrg := workspaceRef, cloudOrgFlag
	workspaceRef = "github.com/example/project@main"
	cloudOrgFlag = "team's org"
	t.Cleanup(func() { workspaceRef, cloudOrgFlag = oldWorkspace, oldOrg })
	cmd := &cobra.Command{}
	err := cloudChecksPrerequisiteError(cmd, []string{"github.com/example/project"}, "Account required", "dagger cloud signup")
	_, script, found := strings.Cut(err.Error(), "then retry:\n\n")
	require.True(t, found)
	shell := exec.Command("sh")
	shell.Stdin = strings.NewReader("dagger() { printf '<%s>\\n' \"$@\"; }\n" + script)
	out, shellErr := shell.CombinedOutput()
	require.NoError(t, shellErr, string(out))
	require.Equal(t, "<cloud>\n<signup>\n<--org>\n<team's org>\n<-W>\n<github.com/example/project@main>\n<cloud>\n<checks>\n<on>\n<github.com/example/project>\n<--org>\n<team's org>\n", string(out))
}

// fakeCloudChecks serves Cloud GraphQL operations from respond, which returns
// the operation's data or "" for an unexpected operation, and records the
// operations it served.
type fakeCloudChecks struct {
	operations []string
	variables  map[string]map[string]any
}

const featuresActive = `{"org":{"id":"org","name":"example","createdAt":"","subscription":{"status":"","subscriptionID":"","planID":"","hasCaching":false},"features":[{"name":"CLOUD_CHECKS","status":"ACTIVE"},{"name":"CLOUD_MODULES","status":"ACTIVE"},{"name":"CLOUD_ENTIRE_FORGE","status":"ACTIVE"}]}}`

func startFakeCloudChecks(t *testing.T, respond func(operation string, variables map[string]any) string) *fakeCloudChecks {
	t.Helper()
	fake := &fakeCloudChecks{variables: map[string]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		operation := request.OperationName
		if operation == "" {
			// Queries built by the typed GraphQL client carry no name.
			operation = "User"
			if strings.Contains(request.Query, "org(name:") {
				operation = "OrgByName"
			}
		}
		fake.operations = append(fake.operations, operation)
		fake.variables[operation] = request.Variables
		response := respond(operation, request.Variables)
		if response == "" {
			t.Errorf("unexpected Cloud operation: %s", operation)
			http.Error(w, "unexpected operation", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(response, `{"errors"`) {
			fmt.Fprint(w, response)
			return
		}
		fmt.Fprintf(w, `{"data":%s}`, response)
	}))
	t.Cleanup(server.Close)
	t.Setenv("DAGGER_CLOUD_URL", server.URL)
	t.Setenv("DAGGER_CLOUD_TOKEN", "test-token")
	oldTTY, oldWorkspace, oldOrg, oldApply := stdinIsTTY, workspaceRef, cloudOrgFlag, autoApply
	stdinIsTTY, workspaceRef, cloudOrgFlag, autoApply = false, "", "", true
	t.Cleanup(func() { stdinIsTTY, workspaceRef, cloudOrgFlag, autoApply = oldTTY, oldWorkspace, oldOrg, oldApply })
	return fake
}

func runCloudChecksOn(t *testing.T, enabled bool, repo string) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	requireCloudFeatures(cmd, cloudChecksRequiredFeatures...)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := runCloudCheckSet(enabled)(cmd, []string{repo})
	return out.String(), err
}

// requireRepoSetting asserts the single repository setting that
// UpdateOrgRepoSettings sent.
func requireRepoSetting(t *testing.T, fake *fakeCloudChecks, org string, want map[string]any) {
	t.Helper()
	variables := fake.variables["UpdateOrgRepoSettings"]
	require.NotNil(t, variables, "UpdateOrgRepoSettings was not called")
	require.Equal(t, org, variables["org"])
	require.Equal(t, []any{want}, variables["settings"])
}

func TestCloudChecksOnGitHub(t *testing.T) {
	const claimed = `{"user":{"repositories":[{"ref":"github.com/example/project","settings":%s,"mappedSource":{"provider":"GITHUB","installationId":"installation","org":{"id":"org","name":"example"}}}]}}`
	for _, tc := range []struct {
		name         string
		repositories string
		sources      string
		features     string
		wantError    string
		wantQueries  []string
		wantSetting  map[string]any
	}{
		{
			name:         "GitHub App install required",
			repositories: `{"user":{"repositories":[]}}`,
			sources:      `{"sources":[]}`,
			wantError:    "installations/select_target",
			wantQueries:  []string{"GetUserRepositories", "GetSources", "GetGithubConnection"},
		},
		{
			name:         "claims an unclaimed repository through its installation",
			repositories: `{"user":{"repositories":[]}}`,
			sources:      `{"sources":[{"id":"installation","name":"example","orgId":"org","orgName":"example"}]}`,
			wantQueries:  []string{"GetUserRepositories", "GetSources", "User", "GetOrgDetails", "UpdateOrgRepoSettings"},
			wantSetting:  map[string]any{"repo": "github.com/example/project", "sourceId": "installation", "runChecksOnDefaultBranch": true, "runChecksOnPullRequest": true},
		},
		{
			name:         "already running default branch and pull request checks",
			repositories: fmt.Sprintf(claimed, `{"repo":"github.com/example/project","effectiveRunChecksOnDefaultBranch":true,"effectiveRunChecksOnPullRequest":true}`),
			wantQueries:  []string{"GetUserRepositories", "GetOrgDetails"},
		},
		{
			name:         "turns on the triggers of a claimed repository",
			repositories: fmt.Sprintf(claimed, `{"repo":"github.com/example/project","effectiveRunChecksOnDefaultBranch":false,"effectiveRunChecksOnPullRequest":false}`),
			wantQueries:  []string{"GetUserRepositories", "GetOrgDetails", "UpdateOrgRepoSettings"},
			wantSetting:  map[string]any{"repo": "github.com/example/project", "runChecksOnDefaultBranch": true, "runChecksOnPullRequest": true},
		},
		{
			name:         "claimed repository whose org lacks the features",
			repositories: fmt.Sprintf(claimed, `{"repo":"github.com/example/project","effectiveRunChecksOnDefaultBranch":true,"effectiveRunChecksOnPullRequest":true}`),
			features:     `{"org":{"id":"org","name":"example","createdAt":"","subscription":{"status":"","subscriptionID":"","planID":"","hasCaching":false},"features":[{"name":"CLOUD_CHECKS","status":"UNUSED"}]}}`,
			wantError:    `CLOUD_CHECKS + CLOUD_MODULES are not enabled for organization "example"`,
			wantQueries:  []string{"GetUserRepositories", "GetOrgDetails"},
		},
		{
			name:         "features are checked before claiming",
			repositories: `{"user":{"repositories":[]}}`,
			sources:      `{"sources":[{"id":"installation","name":"example","orgId":"org","orgName":"example"}]}`,
			features:     `{"org":{"id":"org","name":"example","createdAt":"","subscription":{"status":"","subscriptionID":"","planID":"","hasCaching":false},"features":[]}}`,
			wantError:    `CLOUD_CHECKS + CLOUD_MODULES are not enabled for organization "example"`,
			wantQueries:  []string{"GetUserRepositories", "GetSources", "User", "GetOrgDetails"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := startFakeCloudChecks(t, func(operation string, _ map[string]any) string {
				switch operation {
				case "GetUserRepositories":
					return tc.repositories
				case "GetSources":
					return tc.sources
				case "GetGithubConnection":
					// The identity is connected; only the app install is missing.
					return `{"githubConnection":{"githubLogin":"octocat","connectedAt":""}}`
				case "User":
					return `{"user":{"id":"user","orgs":[{"id":"org","name":"example"}]}}`
				case "GetOrgDetails":
					if tc.features != "" {
						return tc.features
					}
					return featuresActive
				case "UpdateOrgRepoSettings":
					return `{"updateOrgRepoSettings":true}`
				}
				return ""
			})
			out, err := runCloudChecksOn(t, true, "github.com/example/project")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Empty(t, out)
			} else {
				require.NoError(t, err)
				require.Equal(t, "checks for repo github.com/example/project enabled\n", out)
			}
			require.Equal(t, tc.wantQueries, fake.operations)
			if tc.wantSetting != nil {
				requireRepoSetting(t, fake, "org", tc.wantSetting)
			}
		})
	}
}

// A claim rejected for a repo the installation cannot access must surface the
// installation settings page, not the raw backend error.
func TestCloudChecksOnRepoNotGrantedPointsAtInstallationSettings(t *testing.T) {
	startFakeCloudChecks(t, func(operation string, _ map[string]any) string {
		switch operation {
		case "GetUserRepositories":
			return `{"user":{"repositories":[]}}`
		case "GetSources":
			return `{"sources":[{"id":"161781848","name":"marcosnils","orgId":"org","orgName":"marcosnils","configUrl":"https://github.com/settings/installations/161781848"}]}`
		case "User":
			return `{"user":{"id":"user","orgs":[{"id":"org","name":"marcosnils"}]}}`
		case "GetOrgDetails":
			return featuresActive
		case "UpdateOrgRepoSettings":
			return `{"errors":[{"message":"github.com/marcosnils/bin is not available through this GitHub installation"}]}`
		}
		return ""
	})
	_, err := runCloudChecksOn(t, true, "github.com/marcosnils/bin")
	require.EqualError(t, err,
		"Cloud checks need GitHub access to this repository. Visit https://github.com/settings/installations/161781848 to enable it and then run the command again")
}

func TestCloudChecksOnEntire(t *testing.T) {
	const repo = "entire://aws-us-east-2.entire.io/et/acme/api"
	installations := `{"entireInstallations":[{"id":"entire:7","name":"acme","manageURL":"https://entire.com/et/acme/settings/plugins"}]}`
	for _, tc := range []struct {
		name          string
		installations string
		repositories  string
		features      string
		wantError     []string
		wantQueries   []string
		wantSetting   map[string]any
	}{
		{
			name:          "claims the repository through the installation of its project",
			installations: installations,
			repositories:  `{"sourceRepositories":[{"repository":"entire.com/et/acme/web","selected":false,"eligible":true},{"repository":"entire.com/et/acme/api","selected":false,"eligible":true}]}`,
			wantQueries:   []string{"GetUserRepositories", "OrgByName", "GetOrgDetails", "GetEntireInstallations", "GetSourceRepositories", "UpdateOrgRepoSettings"},
			wantSetting:   map[string]any{"repo": "entire.com/et/acme/api", "sourceId": "entire:7", "runChecksOnDefaultBranch": true, "runChecksOnPullRequest": true},
		},
		{
			name:          "plugin not installed on the project",
			installations: `{"entireInstallations":[]}`,
			wantError: []string{
				"Dagger plugin installed on the Entire project of entire.com/et/acme/api",
				"https://entire.com/plugins/dagger/install?state=signed",
				"dagger cloud checks on " + repo + " --org example",
			},
			wantQueries: []string{"GetUserRepositories", "OrgByName", "GetOrgDetails", "GetEntireInstallations", "GetEntireInstallURL"},
		},
		{
			name:          "repository claimed by another organization",
			installations: installations,
			repositories:  `{"sourceRepositories":[{"repository":"entire.com/et/acme/api","selected":false,"eligible":false,"claimedByOrgName":"other"}]}`,
			wantError:     []string{`entire.com/et/acme/api already runs checks in the "other" Dagger Cloud organization`},
			wantQueries:   []string{"GetUserRepositories", "OrgByName", "GetOrgDetails", "GetEntireInstallations", "GetSourceRepositories"},
		},
		{
			name:        "organization without Entire",
			features:    `{"org":{"id":"org","name":"example","createdAt":"","subscription":{"status":"","subscriptionID":"","planID":"","hasCaching":false},"features":[{"name":"CLOUD_CHECKS","status":"ACTIVE"},{"name":"CLOUD_MODULES","status":"ACTIVE"}]}}`,
			wantError:   []string{`CLOUD_ENTIRE_FORGE is not enabled for organization "example"`},
			wantQueries: []string{"GetUserRepositories", "OrgByName", "GetOrgDetails"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := startFakeCloudChecks(t, func(operation string, variables map[string]any) string {
				switch operation {
				case "GetUserRepositories":
					if !sameStrings(variables["refs"], []string{"entire.com/et/acme/api"}) {
						t.Errorf("repository refs: %v", variables["refs"])
					}
					return `{"user":{"repositories":[]}}`
				case "OrgByName":
					return `{"org":{"id":"org","name":"example"}}`
				case "GetOrgDetails":
					if tc.features != "" {
						return tc.features
					}
					return featuresActive
				case "GetEntireInstallations":
					return tc.installations
				case "GetSourceRepositories":
					return tc.repositories
				case "GetEntireInstallURL":
					if variables["redirectURI"] != "http://localhost:3000/setup/entire" {
						t.Errorf("redirect: %v", variables["redirectURI"])
					}
					return `{"entireInstallURL":"https://entire.com/plugins/dagger/install?state=signed"}`
				case "UpdateOrgRepoSettings":
					return `{"updateOrgRepoSettings":true}`
				}
				return ""
			})
			cloudOrgFlag = "example"
			out, err := runCloudChecksOn(t, true, repo)
			if len(tc.wantError) > 0 {
				for _, want := range tc.wantError {
					require.ErrorContains(t, err, want)
				}
				require.Empty(t, out)
			} else {
				require.NoError(t, err)
				require.Equal(t, "checks for repo entire.com/et/acme/api enabled\n", out)
			}
			require.Equal(t, tc.wantQueries, fake.operations)
			if tc.wantSetting != nil {
				requireRepoSetting(t, fake, "org", tc.wantSetting)
			}
		})
	}
}

func TestCloudChecksOffReleasesClaim(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo string
		key  string
	}{
		{name: "GitHub", repo: "https://github.com/Example/Project.git", key: "github.com/example/project"},
		{name: "Entire", repo: "entire://aws-us-east-2.entire.io/et/acme/api", key: "entire.com/et/acme/api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := startFakeCloudChecks(t, func(operation string, _ map[string]any) string {
				switch operation {
				case "GetUserRepositories":
					return `{"user":{"repositories":[{"ref":"` + tc.key + `","mappedSource":{"installationId":"installation","org":{"id":"org","name":"example"}}}]}}`
				case "UpdateOrgRepoSettings":
					return `{"updateOrgRepoSettings":true}`
				}
				return ""
			})
			out, err := runCloudChecksOn(t, false, tc.repo)
			require.NoError(t, err)
			require.Equal(t, "checks for repo "+tc.key+" disabled\n", out)
			requireRepoSetting(t, fake, "org", map[string]any{"repo": tc.key, "runChecksOnDefaultBranch": false, "runChecksOnPullRequest": false, "runChecksOnBranchPush": false})
		})
	}
}

func TestCloudCheckRepo(t *testing.T) {
	for _, tc := range []struct {
		ref      string
		provider cloudCheckProvider
		key      string
		err      bool
	}{
		{ref: "github.com/acme/api", provider: cloudCheckGitHub, key: "github.com/acme/api"},
		{ref: "git@github.com:acme/api.git", provider: cloudCheckGitHub, key: "github.com/acme/api"},
		{ref: "entire://aws-us-east-2.entire.io/et/acme/api", provider: cloudCheckEntire, key: "entire.com/et/acme/api"},
		{ref: "entire://aws-us-east-2.entire.io/et/acme/api.git/", provider: cloudCheckEntire, key: "entire.com/et/acme/api"},
		{ref: "entire://aws-us-east-2.entire.io/gh/acme/api", provider: cloudCheckEntire, key: "entire.com/gh/acme/api"},
		{ref: "https://entire.com/et/acme/api", provider: cloudCheckEntire, key: "entire.com/et/acme/api"},
		{ref: "entire.com/et/acme/api", provider: cloudCheckEntire, key: "entire.com/et/acme/api"},
		{ref: "entire://aws-us-east-2.entire.io/et/acme", err: true},
		{ref: "entire://aws-us-east-2.entire.io", err: true},
		{ref: "entire.com/et//api", err: true},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			provider, key, err := cloudCheckRepo(tc.ref)
			if tc.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.provider, provider)
			require.Equal(t, tc.key, key)
		})
	}
}

func sameStrings(value any, expected []string) bool {
	values, ok := value.([]any)
	if !ok || len(values) != len(expected) {
		return false
	}
	for i, value := range values {
		if value != expected[i] {
			return false
		}
	}
	return true
}
