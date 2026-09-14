package daggercmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// featureTestServer answers GetOrgDetails with the given checks status and
// records startFeatureTrial calls.
func featureTestServer(t *testing.T, checksStatus string, trialCalls *atomic.Int32) *cloudapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(body), "StartFeatureTrial"):
			trialCalls.Add(1)
			require.Contains(t, string(body), `"org":"org-1"`)
			require.Contains(t, string(body), `"feature":"CLOUD_CHECKS"`)
			require.Contains(t, string(body), `"durationDays":14`)
			_, _ = io.WriteString(w, `{"data":{"startFeatureTrial":true}}`)
		case strings.Contains(string(body), "GetOrgDetails"):
			_, _ = io.WriteString(w, `{"data":{"org":{"id":"org-1","name":"myorg","createdAt":"","subscription":{"status":"","subscriptionID":"","planID":"","hasCaching":false},"features":[{"name":"CLOUD_CHECKS","status":"`+checksStatus+`"}]}}}`)
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

func featureTestCmd(features ...cloudFeature) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	requireCloudFeatures(cmd, features...)
	return cmd, out
}

func TestEnsureCommandCloudFeatures(t *testing.T) {
	setAutoApply := func(t *testing.T, v bool) {
		prev := autoApply
		autoApply = v
		t.Cleanup(func() { autoApply = prev })
	}

	t.Run("no declared features is a no-op", func(t *testing.T) {
		var trials atomic.Int32
		client := featureTestServer(t, "UNUSED", &trials)
		cmd, _ := featureTestCmd()
		require.NoError(t, ensureCommandCloudFeatures(cmd, client, "myorg"))
		require.Zero(t, trials.Load())
	})

	t.Run("feature enabled is a no-op", func(t *testing.T) {
		var trials atomic.Int32
		client := featureTestServer(t, "ACTIVE", &trials)
		cmd, _ := featureTestCmd(featureCloudChecks)
		require.NoError(t, ensureCommandCloudFeatures(cmd, client, "myorg"))
		require.Zero(t, trials.Load())
	})

	t.Run("missing feature with auto-apply starts a trial", func(t *testing.T) {
		setAutoApply(t, true)
		var trials atomic.Int32
		client := featureTestServer(t, "UNUSED", &trials)
		cmd, out := featureTestCmd(featureCloudChecks)
		require.NoError(t, ensureCommandCloudFeatures(cmd, client, "myorg"))
		require.Equal(t, int32(1), trials.Load())
		require.Contains(t, out.String(), "Started a 14-day CLOUD_CHECKS trial")
	})

	t.Run("missing feature declined fails with actionable error", func(t *testing.T) {
		setAutoApply(t, false)
		prevTTY := stdinIsTTY
		stdinIsTTY = false // non-interactive -> the prompt declines
		t.Cleanup(func() { stdinIsTTY = prevTTY })
		var trials atomic.Int32
		client := featureTestServer(t, "TRIAL_EXPIRED", &trials)
		cmd, _ := featureTestCmd(featureCloudChecks)
		err := ensureCommandCloudFeatures(cmd, client, "myorg")
		require.Error(t, err)
		require.Contains(t, err.Error(), "CLOUD_CHECKS is not enabled")
		require.Contains(t, err.Error(), `organization "myorg"`)
		require.Zero(t, trials.Load())
	})
}
