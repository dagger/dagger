package network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFirstFree(t *testing.T) {
	parse := func(s string) *net.IPNet {
		ip, ipNet, err := net.ParseCIDR(s)
		require.NoError(t, err)
		ipNet.IP = ip // an interface's address, not its network's
		return ipNet
	}
	for _, tc := range []struct {
		want     string
		attached []string
		free     string
	}{
		{"10.87.0.0/16", []string{"172.17.0.2/16"}, "10.87.0.0/16"},
		{"10.91.0.0/16", []string{"10.91.0.7/16"}, "10.92.0.0/16"},
		{"10.91.0.0/16", []string{"10.91.0.7/16", "10.92.3.4/24"}, "10.93.0.0/16"},
		{"10.89.3.0/24", []string{"10.89.0.5/16"}, "10.90.0.0/24"},
		{"10.89.3.0/24", []string{"10.89.3.9/24"}, "10.89.4.0/24"},
		{"10.0.0.0/16", []string{"10.0.0.0/8"}, "11.0.0.0/16"},
	} {
		var attached []*net.IPNet
		for _, a := range tc.attached {
			attached = append(attached, parse(a))
		}
		_, want, err := net.ParseCIDR(tc.want)
		require.NoError(t, err)
		free, err := firstFree(want, attached)
		require.NoError(t, err)
		require.Equal(t, tc.free, free.String(), "%s with %v attached", tc.want, tc.attached)
	}

	_, want, err := net.ParseCIDR("255.255.0.0/16")
	require.NoError(t, err)
	_, err = firstFree(want, []*net.IPNet{parse("255.255.1.1/16")})
	require.Error(t, err)
}

func TestBridgeFromCIDR(t *testing.T) {
	for cidr, bridge := range map[string]string{
		"10.87.0.0/16":   "10.87.0.1",
		"10.89.3.0/24":   "10.89.3.1",
		"10.87.0.128/25": "10.87.0.129",
	} {
		got, err := BridgeFromCIDR(cidr)
		require.NoError(t, err)
		require.Equal(t, bridge, got.String(), cidr)
	}
}
