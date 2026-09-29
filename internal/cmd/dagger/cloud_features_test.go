package daggercmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestRequireCloudFeatures(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		cmd := &cobra.Command{}
		requireCloudFeatures(cmd, featureCloudChecks, cloudFeature("CLOUD_ENGINES"))
		require.Equal(t, []cloudFeature{"CLOUD_CHECKS", "CLOUD_ENGINES"}, commandCloudFeatures(cmd))
	})

	t.Run("unannotated command has none", func(t *testing.T) {
		require.Empty(t, commandCloudFeatures(&cobra.Command{}))
	})
}

func TestOrgFeatureEnabled(t *testing.T) {
	details := &cloudapi.OrgDetails{Features: []cloudapi.Feature{
		{Name: "CLOUD_CHECKS", Status: "ACTIVE"},
		{Name: "CLOUD_ENGINES", Status: "IN_TRIAL"},
		{Name: "CLOUD_MODULES", Status: "TRIAL_EXPIRED"},
		{Name: "CLOUD_INSIGHTS", Status: "UNUSED"},
	}}
	require.True(t, orgFeatureEnabled(details, "CLOUD_CHECKS"))
	require.True(t, orgFeatureEnabled(details, "CLOUD_ENGINES"))
	require.False(t, orgFeatureEnabled(details, "CLOUD_MODULES"))
	require.False(t, orgFeatureEnabled(details, "CLOUD_INSIGHTS"))
	require.False(t, orgFeatureEnabled(details, "CLOUD_SMARTCHECKS"))
}

// featureTestServer answers GetOrgDetails with the given features and fails
// the test if any trial is started: the gate only checks features.
func featureTestServer(t *testing.T, features string) *cloudapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(body), "startFeatureTrial"):
			t.Errorf("the feature gate must not start trials")
			_, _ = io.WriteString(w, `{"data":{"startFeatureTrial":true}}`)
		case strings.Contains(string(body), "GetOrgDetails"):
			_, _ = io.WriteString(w, `{"data":{"org":{"id":"org-1","name":"myorg","createdAt":"","subscription":{"status":"","subscriptionID":"","planID":"","hasCaching":false},"features":[`+features+`]}}}`)
		default:
			_, _ = io.WriteString(w, `{"data":{}}`)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DAGGER_CLOUD_URL", srv.URL)
	c, err := cloudapi.NewClient(context.Background(), &cloudauth.Cloud{
		Token: &oauth2.Token{AccessToken: "tok", TokenType: "Basic"},
	})
	require.NoError(t, err)
	return c
}

func featureTestCmd(features ...cloudFeature) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	requireCloudFeatures(cmd, features...)
	return cmd
}

func TestEnsureCommandCloudFeatures(t *testing.T) {
	const (
		checksActive  = `{"name":"CLOUD_CHECKS","status":"ACTIVE"}`
		checksTrial   = `{"name":"CLOUD_CHECKS","status":"IN_TRIAL"}`
		checksExpired = `{"name":"CLOUD_CHECKS","status":"TRIAL_EXPIRED"}`
		checksUnused  = `{"name":"CLOUD_CHECKS","status":"UNUSED"}`
		modulesTrial  = `{"name":"CLOUD_MODULES","status":"IN_TRIAL"}`
	)

	t.Run("no declared features is a no-op", func(t *testing.T) {
		require.NoError(t, ensureCommandCloudFeatures(featureTestCmd(), featureTestServer(t, checksUnused), "myorg"))
	})

	t.Run("enabled or in trial passes", func(t *testing.T) {
		for _, features := range []string{checksActive + "," + modulesTrial, checksTrial + "," + modulesTrial} {
			cmd := featureTestCmd(cloudChecksRequiredFeatures...)
			require.NoError(t, ensureCommandCloudFeatures(cmd, featureTestServer(t, features), "myorg"))
		}
	})

	t.Run("an ended trial asks for payment details", func(t *testing.T) {
		cmd := featureTestCmd(cloudChecksRequiredFeatures...)
		err := ensureCommandCloudFeatures(cmd, featureTestServer(t, checksExpired+`,{"name":"CLOUD_MODULES","status":"TRIAL_EXPIRED"}`), "myorg")
		require.Error(t, err)
		require.Contains(t, err.Error(), `the free trial of organization "myorg" has ended`)
		require.Contains(t, err.Error(), "CLOUD_CHECKS + CLOUD_MODULES are no longer enabled")
		require.Contains(t, err.Error(), "dagger cloud billing payment")
	})

	t.Run("never enabled points at the settings", func(t *testing.T) {
		cmd := featureTestCmd(cloudChecksRequiredFeatures...)
		err := ensureCommandCloudFeatures(cmd, featureTestServer(t, checksUnused+","+modulesTrial), "myorg")
		require.Error(t, err)
		require.Contains(t, err.Error(), `CLOUD_CHECKS is not enabled for organization "myorg"`)
		require.Contains(t, err.Error(), "https://dagger.cloud/myorg/settings")
	})

	t.Run("auto-apply does not start a trial", func(t *testing.T) {
		prev := autoApply
		autoApply = true
		t.Cleanup(func() { autoApply = prev })
		cmd := featureTestCmd(cloudChecksRequiredFeatures...)
		require.Error(t, ensureCommandCloudFeatures(cmd, featureTestServer(t, checksUnused), "myorg"))
	})
}

func TestCloudChecksRequiredFeatures(t *testing.T) {
	// The features Cloud enables for organizations created from the CLI.
	require.Equal(t, []cloudFeature{"CLOUD_CHECKS", "CLOUD_MODULES"}, cloudChecksRequiredFeatures)
}
