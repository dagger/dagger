package network

import (
	"fmt"
	"net"
)

// BridgeFromCIDR is the address of the bridge of a container network on
// subnet, the subnet's first address, which CNI's host-local IPAM gives the
// gateway.
func BridgeFromCIDR(subnet string) (net.IP, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, err
	}
	ip4 := ipNet.IP.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("subnet %s is not IPv4", subnet)
	}

	bridge := make(net.IP, 4)
	copy(bridge, ip4)
	bridge[3]++

	return bridge, nil
}
