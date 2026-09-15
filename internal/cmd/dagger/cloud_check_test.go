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
		for _, subcommand := range []string{"on", "off", "status", "list"} {
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
		require.Equal(t, "Complete this prerequisite?\n\n"+command+"\n\n", setupCommandPromptText("Complete this prerequisite?", command))
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

func TestCloudChecksOnPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name        string
		noSource    bool
		enabled     bool
		wantError   string
		wantQueries []string
	}{
		{name: "GitHub required", noSource: true, wantError: "dagger cloud integration create github", wantQueries: []string{"GetUserRepositories", "GetSources"}},
		{name: "already enabled", enabled: true, wantQueries: []string{"GetUserRepositories"}},
		{name: "enable mapped repository", wantQueries: []string{"GetUserRepositories", "GetSources", "GetOrgMappedSources", "ConfigureSource"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					OperationName string         `json:"operationName"`
					Variables     map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				operation := request.OperationName
				if operation == "" {
					operation = "User"
				}
				queries = append(queries, operation)
				var data string
				switch operation {
				case "GetUserRepositories":
					data = `{"user":{"repositories":[]}}`
					if tc.enabled {
						data = `{"user":{"repositories":[{"ref":"github.com/example/project","mappedSource":{"installationId":"installation","mode":"SELECTED","repositories":["github.com/example/project"]}}]}}`
					}
				case "GetSources":
					data = `{"sources":[{"id":"installation","name":"example","orgName":"example"}]}`
					if tc.noSource {
						data = `{"sources":[]}`
					}
				case "GetOrgMappedSources":
					data = `{"org":{"mappedSources":[{"installationId":"installation","mode":"SELECTED","repositories":["github.com/example/other"]}]}}`
				case "ConfigureSource":
					// Enabling this repository must preserve other selections.
					if got := request.Variables["repositories"]; !sameStrings(got, []string{"github.com/example/other", "github.com/example/project"}) {
						t.Errorf("configured repositories: %v", got)
					}
					data = `{"configureSource":{"installationId":"installation","mode":"SELECTED"}}`
				default:
					t.Errorf("unexpected Cloud operation: %s", operation)
					http.Error(w, "unexpected operation", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":%s}`, data)
			}))
			defer server.Close()
			t.Setenv("DAGGER_CLOUD_URL", server.URL)
			t.Setenv("DAGGER_CLOUD_TOKEN", "test-token")
			oldTTY, oldWorkspace, oldOrg := stdinIsTTY, workspaceRef, cloudOrgFlag
			stdinIsTTY, workspaceRef, cloudOrgFlag = false, "", ""
			t.Cleanup(func() { stdinIsTTY, workspaceRef, cloudOrgFlag = oldTTY, oldWorkspace, oldOrg })
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			var out bytes.Buffer
			cmd.SetOut(&out)
			err := runCloudCheckSet(true)(cmd, []string{"github.com/example/project"})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.ErrorContains(t, err, "dagger cloud checks on github.com/example/project")
				require.Empty(t, out.String())
			} else {
				require.NoError(t, err)
				require.Equal(t, "on\n", out.String())
			}
			require.Equal(t, tc.wantQueries, queries)
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
