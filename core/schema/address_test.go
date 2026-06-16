package schema

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSSHFSVolumeAddress(t *testing.T) {
	escapedKnownHosts := url.QueryEscape("op://vault/item?section=host&field=known_hosts")
	parsed, err := parseSSHFSVolumeAddress("sshfs://git@example.com:2222/srv/repo?privateKey=env://SSH_KEY&knownHosts=" + escapedKnownHosts + "&cacheKey=shared&insecureSkipHostKeyCheck=true")
	require.NoError(t, err)
	require.Equal(t, "sshfs://git@example.com:2222/srv/repo", parsed.Endpoint)
	require.Equal(t, "env://SSH_KEY", parsed.PrivateKeyAddr)
	require.Equal(t, "op://vault/item?section=host&field=known_hosts", parsed.KnownHostsAddr)
	require.Equal(t, "shared", parsed.CacheKey)
	require.True(t, parsed.InsecureSkipHostKeyCheck)
}

func TestParseSSHFSVolumeAddressErrors(t *testing.T) {
	for _, tc := range []string{
		"ssh://git@example.com/srv/repo?privateKey=env://SSH_KEY",
		"sshfs://git@example.com/srv/repo",
		"sshfs://git@example.com/srv/repo?privateKey=env://SSH_KEY&insecureSkipHostKeyCheck=maybe",
		"sshfs://git@example.com/srv/repo?privateKey=env://SSH_KEY&unknown=x",
		"sshfs://git@example.com/srv/repo?privateKey=env://SSH_KEY#fragment",
	} {
		_, err := parseSSHFSVolumeAddress(tc)
		require.Error(t, err, tc)
	}
}
