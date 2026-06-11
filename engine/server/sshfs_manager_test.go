package server

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// TestMountIDKeyIsolation locks in the security property that an sshfs mount's
// cache id is bound to possession of the private key: reproducing an id requires
// supplying the same private key.
func TestMountIDKeyIsolation(t *testing.T) {
	const endpoint = "ssh://root@gitserver:2222/root/repo"

	victimPriv := digest.FromBytes([]byte("victim-private-key")).String()
	victimPub := digest.FromBytes([]byte("victim-public-key")).String()
	attackerPriv := digest.FromBytes([]byte("attacker-private-key")).String()

	victimID := mountID(endpoint, victimPriv)

	// Deterministic reuse: same endpoint + same private key digest => same id, so
	// a caller that holds the private key reuses the existing mount.
	require.Equal(t, victimID, mountID(endpoint, victimPriv),
		"same endpoint + private key must reuse the same mount id")

	// The bypass is closed: an attacker who knows the endpoint but supplies a
	// different private key gets a different id, so they cannot collide on the
	// victim's cached mount. (The victim's public key is not even an input here —
	// it has no influence on the id.)
	require.NotEqual(t, victimID, mountID(endpoint, attackerPriv),
		"a different private key must not collide on the victim's mount id")

	// Sanity: knowing the victim's public key is irrelevant — only the private
	// key digest drives the id, and the attacker cannot produce the victim's.
	require.NotEqual(t, mountID(endpoint, victimPub), victimID,
		"the public key digest must not reproduce the private-key-derived id")

	// A different endpoint with the same key is also a distinct mount.
	require.NotEqual(t, victimID, mountID("ssh://root@other:2222/root/repo", victimPriv),
		"a different endpoint must yield a different mount id")
}

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
