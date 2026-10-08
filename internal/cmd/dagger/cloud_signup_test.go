package daggercmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

func TestCloudSignupUsesConfiguredToken(t *testing.T) {
	t.Setenv("DAGGER_CLOUD_TOKEN", "dag_testorg_testtoken")
	previousProgress, previousSwitch, previousOrg := progress, loginSwitchAccount, cloudOrgFlag
	progress, loginSwitchAccount, cloudOrgFlag = "report", false, ""
	t.Cleanup(func() { progress, loginSwitchAccount, cloudOrgFlag = previousProgress, previousSwitch, previousOrg })
	cmd := newSignupCmd()
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	require.NoError(t, cmd.RunE(cmd, nil))
	require.Contains(t, out.String(), "configured with DAGGER_CLOUD_TOKEN")
	require.ErrorContains(t, cmd.RunE(cmd, []string{"otherorg"}), "does not select organization")
}

func TestCloudSignupCommand(t *testing.T) {
	cmd, _, err := testRootCommand().Find([]string{"cloud", "signup"})
	require.NoError(t, err)
	require.Same(t, cloudSignupCmd, cmd)
	require.False(t, cmd.Hidden)
	require.NotNil(t, cmd.Flags().Lookup("switch-account"))
}

func TestCloudSignupNonInteractiveNeedsAccount(t *testing.T) {
	// Any unexpected authentication request must fail locally.
	env := []string{"HTTPS_PROXY=http://127.0.0.1:1"}
	daggerCloudWithEnv(t, env, []string{"cloud", "signup"}, func(t *testing.T, err error, out, stderr *bytes.Buffer) {
		require.Error(t, err)
		require.Contains(t, stderr.String(), "human action required: run dagger cloud signup in a terminal")
		require.Empty(t, out.String())
	})
}

func TestCloudSignupNonInteractiveCreatesOrganization(t *testing.T) {
	// Organization creation is CLI-only (createQuickstartOrg), so signup no
	// longer needs a browser step even without a terminal.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "createQuickstartOrg") {
			fmt.Fprint(w, `{"data":{"createQuickstartOrg":{"id":"org-1","name":"my-org"}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"user":{"id":"user","orgs":[]}}}`)
	}))
	defer server.Close()
	config := map[string]string{
		"credentials.json": `{"access_token":"test-login-token","token_type":"Bearer"}`,
	}
	daggerCloudWithConfig(t, []string{"DAGGER_CLOUD_URL=" + server.URL}, []string{"cloud", "signup"}, config, func(t *testing.T, err error, out, stderr *bytes.Buffer) {
		require.NoError(t, err, stderr.String())
		require.Contains(t, stderr.String(), `Creating a new organization "my-org"`)
		require.Contains(t, stderr.String(), `Started a free 7-day Individual plan trial for organization "my-org"`)
		require.NotContains(t, stderr.String(), "Unable to open browser")
		require.Contains(t, out.String(), "Success.")
	})
}

func TestCreateNewOrgStartsTrial(t *testing.T) {
	newServer := func(t *testing.T, created *int) *cloud.Client {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(string(body), "createQuickstartOrg") {
				*created++
				// The mutation takes only the name: Cloud picks the plan and
				// the trial's features.
				require.Contains(t, string(body), `"variables":{"name":"my-org"}`)
				fmt.Fprint(w, `{"data":{"createQuickstartOrg":{"id":"org-1","name":"my-org"}}}`)
				return
			}
			t.Errorf("unexpected request: %s", body)
		}))
		t.Cleanup(server.Close)
		t.Setenv("DAGGER_CLOUD_URL", server.URL)
		client, err := cloud.NewClient(t.Context(), &auth.Cloud{Token: &oauth2.Token{AccessToken: "tok", TokenType: "Basic"}})
		require.NoError(t, err)
		return client
	}
	answer := func(t *testing.T, start bool) *string {
		t.Helper()
		asked := new(string)
		prev := confirmTrial
		confirmTrial = func(cmd *cobra.Command, orgName string) (bool, error) {
			*asked = orgName
			return start, nil
		}
		t.Cleanup(func() { confirmTrial = prev })
		return asked
	}
	newCmd := func(t *testing.T) (*cobra.Command, *bytes.Buffer) {
		cmd := &cobra.Command{}
		cmd.SetContext(t.Context())
		var stderr bytes.Buffer
		cmd.SetErr(&stderr)
		return cmd, &stderr
	}

	t.Run("the trial is offered before the organization is created", func(t *testing.T) {
		var created int
		client := newServer(t, &created)
		asked := answer(t, true)
		cmd, stderr := newCmd(t)
		org, err := createNewOrg(cmd, client, nil, "my-org")
		require.NoError(t, err)
		require.Equal(t, "my-org", *asked)
		require.Equal(t, 1, created)
		require.Equal(t, "my-org", org.Name)
		require.Contains(t, stderr.String(), `Started a free 7-day Individual plan trial for organization "my-org"`)
	})

	t.Run("declining creates nothing", func(t *testing.T) {
		var created int
		client := newServer(t, &created)
		answer(t, false)
		cmd, stderr := newCmd(t)
		_, err := createNewOrg(cmd, client, nil, "my-org")
		require.ErrorIs(t, err, errTrialDeclined)
		require.Zero(t, created)
		require.NotContains(t, stderr.String(), "Creating a new organization")
	})
}

func TestConfirmTrialWithoutATerminalProceeds(t *testing.T) {
	prevTTY, prevApply := stdinIsTTY, autoApply
	t.Cleanup(func() { stdinIsTTY, autoApply = prevTTY, prevApply })
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())

	stdinIsTTY, autoApply = false, false
	start, err := confirmTrial(cmd, "my-org")
	require.NoError(t, err)
	require.True(t, start)

	stdinIsTTY, autoApply = true, true
	start, err = confirmTrial(cmd, "my-org")
	require.NoError(t, err)
	require.True(t, start)
}
