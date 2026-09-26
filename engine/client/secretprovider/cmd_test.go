package secretprovider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrimCommandOutput(t *testing.T) {
	for _, tc := range []struct{ name, output, want string }{
		{"empty", "", ""},
		{"no newline", "token", "token"},
		{"LF", "token\n", "token"},
		{"CRLF", "token\r\n", "token"},
		{"empty LF line", "\n", ""},
		{"empty CRLF line", "\r\n", ""},
		{"trailing blank LF line", "token\n\n", "token\n\n"},
		{"trailing blank CRLF line", "token\r\n\r\n", "token\r\n\r\n"},
		{"trailing blank mixed lines", "token\n\r\n", "token\n\r\n"},
		{"only newlines", "\n\r\n", "\n\r\n"},
		{"spaces", " token \t\n", " token \t"},
		{"embedded LF", "first\nsecond\n", "first\nsecond\n"},
		{"embedded CRLF", "first\r\nsecond\r\n", "first\r\nsecond\r\n"},
		{"embedded mixed newlines", "first\r\nsecond\n", "first\r\nsecond\n"},
		{"multiline without final newline", "first\nsecond", "first\nsecond"},
		{"bare CR", "token\r", "token\r"},
		{"embedded bare CR", "first\rsecond\n", "first\rsecond\n"},
		{"extra CR", "token\r\r\n", "token\r\r\n"},
		{"PEM", "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n", "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, string(trimCommandOutput([]byte(tc.output))))
		})
	}
}

func TestCmdProvider(t *testing.T) {
	secret, err := cmdProvider(t.Context(), "echo token")
	require.NoError(t, err)
	require.Equal(t, "token", string(secret))
	_, err = cmdProvider(t.Context(), "exit 1")
	require.Error(t, err)
}
