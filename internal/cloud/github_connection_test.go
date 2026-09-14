package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/internal/cloud/auth"
)

func testClient(t *testing.T, url string) *Client {
	t.Helper()
	t.Setenv("DAGGER_CLOUD_URL", url)
	c, err := NewClient(context.Background(), &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "tok", TokenType: "Basic"},
	})
	require.NoError(t, err)
	return c
}

func TestGitHubConnection(t *testing.T) {
	t.Run("returns connection when connected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			require.Contains(t, string(body), "githubConnection")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"githubConnection":{"githubLogin":"octocat","connectedAt":"2026-01-01T00:00:00Z"}}}`)
		}))
		defer srv.Close()

		conn, err := testClient(t, srv.URL).GitHubConnection(context.Background())
		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Equal(t, "octocat", conn.GitHubLogin)
		require.Equal(t, "2026-01-01T00:00:00Z", conn.ConnectedAt)
	})

	t.Run("returns nil when not connected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"githubConnection":null}}`)
		}))
		defer srv.Close()

		conn, err := testClient(t, srv.URL).GitHubConnection(context.Background())
		require.NoError(t, err)
		require.Nil(t, conn)
	})

	t.Run("propagates API errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"errors":[{"message":"unauthorized"}]}`)
		}))
		defer srv.Close()

		conn, err := testClient(t, srv.URL).GitHubConnection(context.Background())
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "unauthorized"))
		require.Nil(t, conn)
	})
}
