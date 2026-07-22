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

func TestParseEngineVolumeAddress(t *testing.T) {
	for _, tc := range []struct {
		address   string
		name      string
		subdir    string
		hasSubdir bool
	}{
		{address: "engine-volume://datasets", name: "datasets"},
		{address: "engine-volume://datasets/models", name: "datasets/models"},
		{
			address:   "engine-volume://datasets/models?subdir=llama%2Fweights",
			name:      "datasets/models",
			subdir:    "llama/weights",
			hasSubdir: true,
		},
		{
			address:   "engine-volume://datasets/models?subdir=path%20with%20spaces",
			name:      "datasets/models",
			subdir:    "path with spaces",
			hasSubdir: true,
		},
	} {
		t.Run(tc.address, func(t *testing.T) {
			parsed, err := parseEngineVolumeAddress(tc.address)
			require.NoError(t, err)
			require.Equal(t, tc.name, parsed.Name)
			require.Equal(t, tc.subdir, parsed.Subdir)
			require.Equal(t, tc.hasSubdir, parsed.HasSubdir)
		})
	}
}

func TestParseEngineVolumeAddressErrors(t *testing.T) {
	for _, tc := range []string{
		"sshfs://datasets/models",
		"engine-volume:datasets/models",
		"engine-volume:///datasets/models",
		"engine-volume://user@datasets/models",
		"engine-volume://datasets/models/",
		"engine-volume://datasets//models",
		"engine-volume://datasets/%6dodels",
		"engine-volume://datasets/fs",
		"engine-volume://datasets/models?",
		"engine-volume://datasets/models?subdir=",
		"engine-volume://datasets/models?subdir=one&subdir=two",
		"engine-volume://datasets/models?subdir=../escape",
		"engine-volume://datasets/models?unknown=x",
		"engine-volume://datasets/models#fragment",
	} {
		t.Run(tc, func(t *testing.T) {
			_, err := parseEngineVolumeAddress(tc)
			require.Error(t, err)
		})
	}
}
