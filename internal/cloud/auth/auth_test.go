package auth

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
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
