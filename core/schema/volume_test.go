package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithSSHFSRunningHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		host     string
		want     string
	}{
		{
			name:     "ssh:// form preserves explicit port and path",
			endpoint: "ssh://root@gitserver:2222/root/repo",
			host:     "10.0.0.5",
			want:     "ssh://root@10.0.0.5:2222/root/repo",
		},
		{
			name:     "ssh:// form defaults missing port to 22",
			endpoint: "ssh://root@gitserver/root/repo",
			host:     "10.0.0.5",
			want:     "ssh://root@10.0.0.5:22/root/repo",
		},
		{
			name:     "scp-style preserves explicit port and path",
			endpoint: "root@gitserver:2222/root/repo",
			host:     "10.0.0.5",
			want:     "root@10.0.0.5:2222/root/repo",
		},
		{
			name:     "scp-style without port rewrites host only",
			endpoint: "root@gitserver/root/repo",
			host:     "10.0.0.5",
			want:     "root@10.0.0.5/root/repo",
		},
		{
			name:     "scp-style without path defaults path to /",
			endpoint: "root@gitserver",
			host:     "10.0.0.5",
			want:     "root@10.0.0.5/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withSSHFSRunningHost(tc.endpoint, tc.host)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("scp-style missing @ is rejected", func(t *testing.T) {
		_, err := withSSHFSRunningHost("gitserver:2222/root/repo", "10.0.0.5")
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing @")
	})
}
