package oci

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/moby/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

const defaultResolvConf = `
nameserver 8.8.8.8
nameserver 8.8.4.4
nameserver 2001:4860:4860::8888
nameserver 2001:4860:4860::8844`

const dnsOption = `
options ndots:0`

const localDNSResolvConf = `
nameserver 127.0.0.11
options ndots:0`

const regularResolvConf = `
# DNS requests are forwarded to the host. DHCP DNS options are ignored.
nameserver 192.168.65.5`

// TestResolvConf modifies a global variable
// It must not run in parallel.
func TestResolvConf(t *testing.T) {
	cases := []struct {
		name        string
		dt          []byte
		execution   int
		networkMode []pb.NetMode
		expected    []string
	}{
		{
			name:        "TestResolvConfNotExist",
			dt:          nil,
			execution:   1,
			networkMode: []pb.NetMode{pb.NetMode_UNSET},
			expected:    []string{defaultResolvConf},
		},
		{
			name:        "TestNetModeIsHostResolvConfNotExist",
			dt:          nil,
			execution:   1,
			networkMode: []pb.NetMode{pb.NetMode_HOST},
			expected:    []string{defaultResolvConf},
		},
		{
			name:        "TestNetModeIsHostWithoutLocalDNS",
			dt:          []byte(regularResolvConf),
			execution:   1,
			networkMode: []pb.NetMode{pb.NetMode_HOST},
			expected:    []string{regularResolvConf},
		},
		{
			name:        "TestNetModeIsHostWithLocalDNS",
			dt:          []byte(localDNSResolvConf),
			execution:   1,
			networkMode: []pb.NetMode{pb.NetMode_HOST},
			expected:    []string{localDNSResolvConf},
		},
		{
			name:        "TestNetModeNotHostWithoutLocalDNS",
			dt:          []byte(regularResolvConf),
			execution:   1,
			networkMode: []pb.NetMode{pb.NetMode_UNSET},
			expected:    []string{regularResolvConf},
		},
		{
			name:        "TestNetModeNotHostWithLocalDNS",
			dt:          []byte(localDNSResolvConf),
			execution:   1,
			networkMode: []pb.NetMode{pb.NetMode_UNSET},
			expected:    []string{fmt.Sprintf("%s%s", dnsOption, defaultResolvConf)},
		},
		{
			name:        "TestRegenerateResolvconfToRemoveLocalDNS",
			dt:          []byte(localDNSResolvConf),
			execution:   2,
			networkMode: []pb.NetMode{pb.NetMode_HOST, pb.NetMode_UNSET},
			expected: []string{
				localDNSResolvConf,
				fmt.Sprintf("%s%s", dnsOption, defaultResolvConf),
			},
		},
		{
			name:        "TestRegenerateResolvconfToAddLocalDNS",
			dt:          []byte(localDNSResolvConf),
			execution:   2,
			networkMode: []pb.NetMode{pb.NetMode_UNSET, pb.NetMode_HOST},
			expected: []string{
				fmt.Sprintf("%s%s", dnsOption, defaultResolvConf),
				localDNSResolvConf,
			},
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			tempDir := t.TempDir()
			oldResolvconfPath := resolvconfPath
			t.Cleanup(func() {
				resolvconfPath = oldResolvconfPath
			})
			resolvconfPath = func() string {
				if tt.dt == nil {
					return "no-such-file"
				}
				rpath := path.Join(t.TempDir(), "resolv.conf")
				require.NoError(t, os.WriteFile(rpath, tt.dt, 0600))
				return rpath
			}
			for i := 0; i < tt.execution; i++ {
				if i > 0 {
					time.Sleep(100 * time.Millisecond)
				}
				p, err := GetResolvConf(ctx, tempDir, nil, nil, tt.networkMode[i])
				require.NoError(t, err)
				b, err := os.ReadFile(p)
				require.NoError(t, err)
				require.Equal(t, tt.expected[i], string(b))
			}
		})
	}
}
