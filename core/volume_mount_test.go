package core

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSHFSCommandArgsSecure(t *testing.T) {
	t.Parallel()

	args := sshfsCommandArgs("git@example.com:/srv/repo", "/tmp/mnt", sshfsCommandConfig{
		Port:           "2222",
		PrivateKeyPath: "/tmp/key",
		KnownHostsPath: "/tmp/known_hosts",
		HostKeyAlias:   "[example.com]:2222",
		AllowOther:     true,
		Readonly:       true,
	})

	require.Equal(t, []string{
		"git@example.com:/srv/repo",
		"/tmp/mnt",
		"-p", "2222",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityFile=/tmp/key",
		"-o", "allow_other",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=/tmp/known_hosts",
		"-o", "HostKeyAlias=[example.com]:2222",
		"-o", "ro",
	}, args)
}

func TestSSHFSCommandArgsInsecure(t *testing.T) {
	t.Parallel()

	args := sshfsCommandArgs("git@example.com:/srv/repo", "/tmp/mnt", sshfsCommandConfig{
		PrivateKeyPath:           "/tmp/key",
		KnownHostsPath:           "/tmp/known_hosts",
		HostKeyAlias:             "example.com",
		InsecureSkipHostKeyCheck: true,
		AllowOther:               true,
	})

	require.Equal(t, []string{
		"git@example.com:/srv/repo",
		"/tmp/mnt",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityFile=/tmp/key",
		"-o", "allow_other",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}, args)
}

func TestSSHFSCommandArgsWithoutAllowOther(t *testing.T) {
	t.Parallel()

	args := sshfsCommandArgs("git@example.com:/srv/repo", "/tmp/mnt", sshfsCommandConfig{
		PrivateKeyPath: "/tmp/key",
	})

	require.NotContains(t, args, "allow_other")
}

func TestFuseConfAllowsOther(t *testing.T) {
	t.Parallel()

	require.True(t, fuseConfAllowsOther([]byte(`
# comment
user_allow_other
`)))
	require.True(t, fuseConfAllowsOther([]byte(" user_allow_other # trailing comment\n")))
	require.False(t, fuseConfAllowsOther([]byte("# user_allow_other\n")))
	require.False(t, fuseConfAllowsOther([]byte("mount_max = 1000\n")))
}

func TestSSHFSServiceHostPort(t *testing.T) {
	t.Parallel()

	port, err := sshfsServiceHostPort(&RunningService{
		Ports: []Port{{Port: 2222, Protocol: NetworkProtocolTCP}},
	}, "")
	require.NoError(t, err)
	require.Equal(t, "2222", port)

	port, err = sshfsServiceHostPort(&RunningService{
		Ports: []Port{
			{Port: 2222, Protocol: NetworkProtocolTCP},
			{Port: 8080, Protocol: NetworkProtocolTCP},
		},
	}, "2222")
	require.NoError(t, err)
	require.Equal(t, "2222", port)
}

func TestSSHFSServiceHostPortRejectsAmbiguousService(t *testing.T) {
	t.Parallel()

	_, err := sshfsServiceHostPort(&RunningService{}, "")
	require.ErrorContains(t, err, "exposes no ports")

	_, err = sshfsServiceHostPort(&RunningService{
		Ports: []Port{
			{Port: 2222, Protocol: NetworkProtocolTCP},
			{Port: 8080, Protocol: NetworkProtocolTCP},
		},
	}, "")
	require.ErrorContains(t, err, "multiple TCP ports")

	_, err = sshfsServiceHostPort(&RunningService{
		Ports: []Port{{Port: 2222, Protocol: NetworkProtocolTCP}},
	}, "2022")
	require.ErrorContains(t, err, "does not expose endpoint port 2022")
}

func TestSSHFSSourceForURL(t *testing.T) {
	t.Parallel()

	endpoint, err := url.Parse("sshfs://git@example.com/srv/repo")
	require.NoError(t, err)
	source, err := sshfsSourceForURL(endpoint, endpoint.Hostname())
	require.NoError(t, err)
	require.Equal(t, "git@example.com:/srv/repo", source)

	endpoint, err = url.Parse("sshfs://git@[::1]/srv/repo")
	require.NoError(t, err)
	source, err = sshfsSourceForURL(endpoint, endpoint.Hostname())
	require.NoError(t, err)
	require.Equal(t, "git@[::1]:/srv/repo", source)
}

func TestSSHFSSourceForURLRejectsInvalidEndpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		endpoint string
		host     string
		wantErr  string
	}{
		{
			name:     "missing user",
			endpoint: "sshfs://example.com/srv/repo",
			host:     "example.com",
			wantErr:  "missing user",
		},
		{
			name:     "missing host",
			endpoint: "sshfs://git@example.com/srv/repo",
			wantErr:  "missing host",
		},
		{
			name:     "missing path",
			endpoint: "sshfs://git@example.com",
			host:     "example.com",
			wantErr:  "missing absolute path",
		},
		{
			name:     "leading dash host",
			endpoint: "sshfs://git@example.com/srv/repo",
			host:     "-example.com",
			wantErr:  "must not start with '-'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint, err := url.Parse(tc.endpoint)
			require.NoError(t, err)
			_, err = sshfsSourceForURL(endpoint, tc.host)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
