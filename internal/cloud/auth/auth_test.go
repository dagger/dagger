package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestParseDaggerToken(t *testing.T) {
	tc := []struct {
		src      string
		ok       bool
		expected daggerToken
	}{
		{
			src:      "bad",
			ok:       false,
			expected: daggerToken{},
		},
		{
			src:      "dag_org_token",
			ok:       true,
			expected: daggerToken{orgName: "org", token: "token"},
		},
	}

	for _, tc := range tc {
		t.Run(tc.src, func(t *testing.T) {
			res, ok := ParseDaggerToken(tc.src)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.expected, res)
		})
	}
}

func TestWriteDeviceAuthPrompt(t *testing.T) {
	deviceAuth := &oauth2.DeviceAuthResponse{
		VerificationURIComplete: "https://auth.dagger.cloud/activate?user_code=ABCD-EFGH",
		UserCode:                "ABCD-EFGH",
	}

	tests := []struct {
		name    string
		opts    loginOptions
		attempt deviceAuthAttempt
		want    string
	}{
		{
			name:    "login",
			attempt: deviceAuthAttempt{action: "Authenticate", auth: deviceAuth, signup: true},
			want: "Login or sign up: https://auth.dagger.cloud/activate?user_code=ABCD-EFGH\n" +
				"Verification code: ABCD-EFGH\n" +
				"\n" +
				"Waiting for authentication. Press Ctrl-C to cancel.\n",
		},
		{
			name:    "auth gate",
			opts:    loginOptions{authGate: true},
			attempt: deviceAuthAttempt{action: "Authenticate", auth: deviceAuth, signup: true},
			want: "This command requires authentication.\n" +
				"\n" +
				"Login or sign up to continue: https://auth.dagger.cloud/activate?user_code=ABCD-EFGH\n" +
				"Verification code: ABCD-EFGH\n" +
				"\n" +
				"Waiting for authentication. Press Ctrl-C to cancel.\n",
		},
		{
			name:    "switch account",
			attempt: deviceAuthAttempt{action: "Choose an account", auth: deviceAuth},
			want: "Choose an account: https://auth.dagger.cloud/activate?user_code=ABCD-EFGH\n" +
				"Verification code: ABCD-EFGH\n" +
				"\n" +
				"Waiting for authentication. Press Ctrl-C to cancel.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeDeviceAuthPrompt(&buf, tc.attempt, tc.opts)
			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestRefreshTokenForcesGrantAndPersists(t *testing.T) {
	var grants int
	var grantType, refreshToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grants++
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		grantType = r.Form.Get("grant_type")
		refreshToken = r.Form.Get("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)

	oldAuthConfig := authConfig
	oldCredentialsFile := credentialsFile
	authConfig = &oauth2.Config{
		ClientID: "test-client",
		Endpoint: oauth2.Endpoint{
			TokenURL:  srv.URL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	credentialsFile = filepath.Join(t.TempDir(), "credentials.json")
	t.Cleanup(func() {
		authConfig = oldAuthConfig
		credentialsFile = oldCredentialsFile
	})

	refreshed, err := RefreshToken(t.Context(), &oauth2.Token{
		AccessToken:  "server-rejected-but-locally-valid",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	require.Equal(t, "new-access", refreshed.AccessToken)
	require.Equal(t, "new-refresh", refreshed.RefreshToken)
	require.Equal(t, 1, grants)
	require.Equal(t, "refresh_token", grantType)
	require.Equal(t, "old-refresh", refreshToken)

	data, err := os.ReadFile(credentialsFile)
	require.NoError(t, err)
	var persisted oauth2.Token
	require.NoError(t, json.Unmarshal(data, &persisted))
	require.Equal(t, refreshed.AccessToken, persisted.AccessToken)
	require.Equal(t, refreshed.RefreshToken, persisted.RefreshToken)
}

func TestGetCloudAuthAllowsMissingOrgFile(t *testing.T) {
	dir := t.TempDir()
	oldCredentialsFile := credentialsFile
	oldOrgFile := orgFile
	credentialsFile = filepath.Join(dir, "credentials.json")
	orgFile = filepath.Join(dir, "org")
	t.Cleanup(func() {
		credentialsFile = oldCredentialsFile
		orgFile = oldOrgFile
	})

	token := &oauth2.Token{
		AccessToken: "token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(time.Hour),
	}
	data, err := json.Marshal(token)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(credentialsFile, data, 0o600))

	cloud, err := GetCloudAuth(t.Context())
	require.NoError(t, err)
	require.NotNil(t, cloud)
	require.NotNil(t, cloud.Token)
	require.Nil(t, cloud.Org)
}

// The credentials file is replaced atomically: while writers of different
// token lengths race, a reader always parses a complete file.
func TestWriteFileReplacesAtomically(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "credentials.json")
	require.NoError(t, writeFile(path, []byte(`{"access_token":"seed"}`)))
	tokens := []string{
		`{"access_token":"short"}`,
		`{"access_token":"a-much-longer-token-than-the-other-one-written-concurrently"}`,
	}
	stop := make(chan struct{})
	var writers sync.WaitGroup
	for _, token := range tokens {
		writers.Go(func() {
			for range 200 {
				require.NoError(t, writeFile(path, []byte(token)))
			}
		})
	}
	readerDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				readerDone <- nil
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				readerDone <- err
				return
			}
			var parsed map[string]string
			if err := json.Unmarshal(data, &parsed); err != nil {
				readerDone <- fmt.Errorf("partial or interleaved file %q: %w", data, err)
				return
			}
		}
	}()
	writers.Wait()
	close(stop)
	require.NoError(t, <-readerDone)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

// When the rename keeps failing, as on Windows while another process holds
// the file open, the CLI's write falls back to writing it in place; without
// the fallback it fails and leaves the file untouched.
func TestWriteFileFallsBackInPlace(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "credentials.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"access_token":"old"}`), 0o600))
	failing := func(string, string) error { return errors.New("sharing violation") }

	require.Error(t, writeFileWith(path, []byte(`{"access_token":"new"}`), failing, false))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, `{"access_token":"old"}`, string(got), "no fallback: the file is untouched")

	require.NoError(t, writeFileWith(path, []byte(`{"access_token":"new"}`), failing, true))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, `{"access_token":"new"}`, string(got), "the fallback writes in place")
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".tmp", "no temporary file left behind")
	}
}
