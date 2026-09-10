package daggercmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestCloudLoginExplicitAuthentication(t *testing.T) {
	for _, token := range []string{"", "dag_testorg_testtoken"} {
		t.Run("cloud token="+token, func(t *testing.T) {
			t.Setenv("DAGGER_CLOUD_TOKEN", token)
			oldProgress, oldSwitch := progress, loginSwitchAccount
			t.Cleanup(func() { progress, loginSwitchAccount = oldProgress, oldSwitch })
			cmd := newLoginCmd(false)
			progress = "report"
			require.NoError(t, cmd.Flags().Set("switch-account", "true"))

			// Stop authentication at the HTTP request, before a browser can open.
			// Neither report output nor an engine token may block explicit login.
			requested := false
			httpClient := &http.Client{Transport: cloudLoginRoundTripper(func(req *http.Request) (*http.Response, error) {
				requested = true
				require.Equal(t, "/oauth/device/code", req.URL.Path)
				require.NoError(t, req.ParseForm())
				require.Equal(t, "login", req.Form.Get("prompt"))
				return nil, errors.New("test stopped authentication before opening a browser")
			})}
			cmd.SetContext(context.WithValue(t.Context(), oauth2.HTTPClient, httpClient))
			require.ErrorContains(t, cmd.RunE(cmd, nil), "test stopped authentication")
			require.True(t, requested)
		})
	}
}

func TestCloudLoginStoredCredentials(t *testing.T) {
	for _, command := range []string{"login", "cloud login", "cloud signup"} {
		for _, org := range []string{"", "team"} {
			t.Run(command+"/org="+org, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if got := r.Header.Get("Authorization"); got != "Bearer test-login-token" {
						t.Errorf("expected saved login credentials, got %q", got)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"data":{"user":{"id":"user","orgs":[{"id":"team-id","name":"team"},{"id":"other-id","name":"other"}]}}}`)
				}))
				defer server.Close()

				env := []string{"DAGGER_CLOUD_URL=" + server.URL, "DAGGER_PROGRESS=report"}
				if command != "cloud signup" {
					// Login must still select an organization using saved OAuth
					// credentials, even when an engine token is also configured.
					env = append(env, "DAGGER_CLOUD_TOKEN=dag_engineorg_enginetoken")
				}
				args := strings.Fields(command)
				if org != "" {
					args = append(args, org)
				}
				config := map[string]string{
					"credentials.json": `{"access_token":"test-login-token","token_type":"Bearer"}`,
				}
				daggerCloudWithConfig(t, env, args, config, func(t *testing.T, err error, out, stderr *bytes.Buffer) {
					if org == "" {
						require.Error(t, err)
						require.Contains(t, stderr.String(), "Please select one with `dagger login <org>`")
					} else {
						require.NoError(t, err, stderr.String())
						require.Equal(t, "Success.\n", out.String())
					}
				})
				require.EqualValues(t, 1, requests.Load())
			})
		}
	}
}

type cloudLoginRoundTripper func(*http.Request) (*http.Response, error)

func (f cloudLoginRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
