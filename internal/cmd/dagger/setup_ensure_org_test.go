package daggercmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// backupCloudConfig saves and restores the on-disk org/credentials files so the
// test's SetCurrentOrg writes don't clobber the developer's real Cloud config.
func backupCloudConfig(t *testing.T) {
	t.Helper()
	for _, name := range []string{"org", "credentials.json"} {
		path := filepath.Join(xdg.ConfigHome, "dagger", name)
		orig, err := os.ReadFile(path)
		existed := err == nil
		t.Cleanup(func() {
			if existed {
				_ = os.WriteFile(path, orig, 0o600)
			} else {
				_ = os.Remove(path)
			}
		})
	}
}

func TestSetupEnsureOrg(t *testing.T) {
	t.Run("creates org when account has none", func(t *testing.T) {
		backupCloudConfig(t)
		var createCalled bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(string(body), "createQuickstartOrg") {
				createCalled = true
				_, _ = io.WriteString(w, `{"data":{"createQuickstartOrg":{"id":"org-123","name":"tester"}}}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":{"user":{"id":"u1","nickname":"tester","email":"t@example.com","orgs":[]}}}`)
		}))
		defer srv.Close()
		t.Setenv("DAGGER_CLOUD_URL", srv.URL)
		t.Setenv("DAGGER_CLOUD_TOKEN", "")

		auth := &cloudauth.Cloud{Token: &oauth2.Token{AccessToken: "tok", TokenType: "Basic"}}
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())
		var out bytes.Buffer
		cmd.SetErr(&out)

		err := setupEnsureOrg(context.Background(), cmd, nil, auth)
		require.NoError(t, err)
		require.True(t, createCalled, "expected createQuickstartOrg to be called")

		org, err := cloudauth.CurrentOrg()
		require.NoError(t, err)
		require.Equal(t, "org-123", org.ID)
		require.Equal(t, "tester", org.Name)
	})

	t.Run("adopts first org when account already has one", func(t *testing.T) {
		backupCloudConfig(t)
		var createCalled bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(string(body), "createQuickstartOrg") {
				createCalled = true
			}
			_, _ = io.WriteString(w, `{"data":{"user":{"id":"u1","nickname":"tester","email":"t@example.com","orgs":[{"id":"org-existing","name":"acme"}]}}}`)
		}))
		defer srv.Close()
		t.Setenv("DAGGER_CLOUD_URL", srv.URL)
		t.Setenv("DAGGER_CLOUD_TOKEN", "")

		auth := &cloudauth.Cloud{Token: &oauth2.Token{AccessToken: "tok", TokenType: "Basic"}}
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())

		err := setupEnsureOrg(context.Background(), cmd, nil, auth)
		require.NoError(t, err)
		require.False(t, createCalled, "should not create an org when one exists")

		org, err := cloudauth.CurrentOrg()
		require.NoError(t, err)
		require.Equal(t, "org-existing", org.ID)
	})

	t.Run("no-op when a current org is already selected", func(t *testing.T) {
		backupCloudConfig(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected Cloud request: setupEnsureOrg should have short-circuited")
		}))
		defer srv.Close()
		t.Setenv("DAGGER_CLOUD_URL", srv.URL)
		t.Setenv("DAGGER_CLOUD_TOKEN", "")

		auth := &cloudauth.Cloud{
			Token: &oauth2.Token{AccessToken: "tok", TokenType: "Basic"},
			Org:   &cloudauth.Org{ID: "org-current", Name: "current"},
		}
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())

		require.NoError(t, setupEnsureOrg(context.Background(), cmd, nil, auth))
	})
}
