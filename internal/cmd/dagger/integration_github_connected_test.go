package daggercmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// fakeCloudClient builds a cloud client pointed at a test server that answers
// the githubConnection and sources queries per the provided responders.
func fakeCloudClient(t *testing.T, githubConnectionJSON, sourcesJSON string) *cloudapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(body), "githubConnection"):
			_, _ = io.WriteString(w, githubConnectionJSON)
		case strings.Contains(string(body), "GetSources"), strings.Contains(string(body), "sources"):
			_, _ = io.WriteString(w, sourcesJSON)
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

func TestGithubConnected(t *testing.T) {
	cli := &CloudCLI{}

	t.Run("stored connection returns login", func(t *testing.T) {
		client := fakeCloudClient(t,
			`{"data":{"githubConnection":{"githubLogin":"octocat","connectedAt":"2026-01-01T00:00:00Z"}}}`,
			`{"data":{"sources":[]}}`,
		)
		connected, login := cli.githubConnected(context.Background(), client)
		require.True(t, connected)
		require.Equal(t, "octocat", login)
	})

	t.Run("no stored connection but sources listable (Auth0 identity)", func(t *testing.T) {
		client := fakeCloudClient(t,
			`{"data":{"githubConnection":null}}`,
			`{"data":{"sources":[{"name":"acme","id":"1","type":"Organization","configuredAt":"","orgName":null,"configUrl":""}]}}`,
		)
		connected, login := cli.githubConnected(context.Background(), client)
		require.True(t, connected)
		require.Empty(t, login)
	})

	t.Run("no connection and no GitHub identity", func(t *testing.T) {
		client := fakeCloudClient(t,
			`{"data":{"githubConnection":null}}`,
			`{"errors":[{"message":"no GitHub identity found"}]}`,
		)
		connected, login := cli.githubConnected(context.Background(), client)
		require.False(t, connected)
		require.Empty(t, login)
	})
}
