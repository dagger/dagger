package network

import "net"

func BridgeFromCIDR(subnet string) (net.IP, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, err
	}

	bridge := make(net.IP, 4)
	copy(bridge, ipNet.IP)
	bridge[3] = 1

	return bridge, nil
}
