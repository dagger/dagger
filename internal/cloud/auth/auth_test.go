package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
