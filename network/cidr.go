package network

import (
	"fmt"
	"net"
)

// FreeCIDR returns the first address range, starting at cidr and stepping by
// its size, that overlaps no subnet on this host's interfaces but the
// engine's own bridge. A container network on a subnet the engine is
// attached to cuts the engine off from it: the bridge takes over the
// subnet's addresses, such as the upstream DNS server of an engine nested
// in another engine on the same range, so the engine's lookups are answered
// by its own resolver, which forwards them to itself.
func FreeCIDR(cidr, bridge string) (string, error) {
	_, want, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list interfaces: %w", err)
	}
	var attached []*net.IPNet
	for _, iface := range ifaces {
		if iface.Name == bridge || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return "", fmt.Errorf("addresses of %s: %w", iface.Name, err)
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				attached = append(attached, ipNet)
			}
		}
	}
	free, err := firstFree(want, attached)
	if err != nil {
		return "", err
	}
	if free.String() == want.String() {
		return cidr, nil
	}
	return free.String(), nil
}

// firstFree is the first IPv4 range of want's size, starting at want, that
// overlaps none of attached.
func firstFree(want *net.IPNet, attached []*net.IPNet) (*net.IPNet, error) {
	ip4 := want.IP.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("network %s is not IPv4", want)
	}
	ones, bits := want.Mask.Size()
	size := uint64(1) << (bits - ones)
	start := uint64(ip4[0])<<24 | uint64(ip4[1])<<16 | uint64(ip4[2])<<8 | uint64(ip4[3])
	for base := start; base+size <= 1<<32; base += size {
		candidate := &net.IPNet{
			IP:   net.IPv4(byte(base>>24), byte(base>>16), byte(base>>8), byte(base)).To4(),
			Mask: want.Mask,
		}
		if !overlapsAny(candidate, attached) {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("no range of %s's size from it on overlaps none of this host's networks", want)
}

func overlapsAny(n *net.IPNet, others []*net.IPNet) bool {
	for _, o := range others {
		if n.Contains(o.IP) || o.Contains(n.IP) {
			return true
		}
	}
	return false
}
