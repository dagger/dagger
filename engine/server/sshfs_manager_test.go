package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSSHEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		want     parsedSSHEndpoint
	}{
		{
			name:     "ssh:// form with explicit port",
			endpoint: "ssh://root@gitserver:2222/root/repo",
			want:     parsedSSHEndpoint{user: "root", host: "gitserver", port: "2222", path: "/root/repo"},
		},
		{
			name:     "ssh:// form defaults missing port to 22",
			endpoint: "ssh://root@gitserver/root/repo",
			want:     parsedSSHEndpoint{user: "root", host: "gitserver", port: "22", path: "/root/repo"},
		},
		{
			name:     "ssh:// form defaults empty path to /",
			endpoint: "ssh://root@gitserver",
			want:     parsedSSHEndpoint{user: "root", host: "gitserver", port: "22", path: "/"},
		},
		{
			name:     "scp-style with explicit port",
			endpoint: "root@gitserver:2222/root/repo",
			want:     parsedSSHEndpoint{user: "root", host: "gitserver", port: "2222", path: "/root/repo"},
		},
		{
			name:     "scp-style without port defaults to 22",
			endpoint: "root@gitserver/root/repo",
			want:     parsedSSHEndpoint{user: "root", host: "gitserver", port: "22", path: "/root/repo"},
		},
		{
			name:     "scp-style without path defaults path to /",
			endpoint: "root@gitserver",
			want:     parsedSSHEndpoint{user: "root", host: "gitserver", port: "22", path: "/"},
		},
		{
			name:     "scp-style non-numeric port is ignored and falls back to 22",
			endpoint: "root@gitserver:not-a-port/root/repo",
			want:     parsedSSHEndpoint{user: "root", host: "gitserver", port: "22", path: "/root/repo"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSSHEndpoint(tc.endpoint)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("ssh:// form rejects missing user", func(t *testing.T) {
		_, err := parseSSHEndpoint("ssh://gitserver/root/repo")
		require.Error(t, err)
	})
	t.Run("scp-style rejects missing @", func(t *testing.T) {
		_, err := parseSSHEndpoint("gitserver:2222/root/repo")
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing '@'")
	})
	t.Run("scp-style rejects empty user", func(t *testing.T) {
		_, err := parseSSHEndpoint("@gitserver/root/repo")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty user")
	})
	t.Run("scp-style rejects empty host", func(t *testing.T) {
		_, err := parseSSHEndpoint("root@/root/repo")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty host")
	})
}
