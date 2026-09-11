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
		{"trailing blank lines", "token\n\r\n", "token"},
		{"only newlines", "\n\r\n", ""},
		{"spaces", " token \t\n", " token \t"},
		{"embedded newlines", "first\r\nsecond\n", "first\r\nsecond"},
		{"bare CR", "token\r", "token\r"},
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
