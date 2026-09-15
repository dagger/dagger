package daggercmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestCloudSignupNonInteractiveNeedsOrganization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"user":{"id":"user","orgs":[]}}}`)
	}))
	defer server.Close()
	config := map[string]string{
		"credentials.json": `{"access_token":"test-login-token","token_type":"Bearer"}`,
	}
	daggerCloudWithConfig(t, []string{"DAGGER_CLOUD_URL=" + server.URL}, []string{"cloud", "signup"}, config, func(t *testing.T, err error, out, stderr *bytes.Buffer) {
		require.Error(t, err)
		require.Contains(t, stderr.String(), "human action required: create an organization")
		require.NotContains(t, stderr.String(), "Unable to open browser")
		require.Empty(t, out.String())
	})
}
