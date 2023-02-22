package main

import (
	"net"

	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/pkg/errors"
)

// getIPs iterates a result and returns all the IP addresses
// associated with it
func getIPs(r *current.Result) ([]*net.IPNet, error) {
	var (
		ips []*net.IPNet
	)
	if len(r.IPs) < 1 {
		return nil, ErrNoIPAddressFound
	}
	if len(r.IPs) == 1 {
		return append(ips, &r.IPs[0].Address), nil
	}
	for _, ip := range r.IPs {
		if ip.Address.IP != nil && ip.Interface != nil {
			if isInterfaceIndexSandox(*ip.Interface, r) {
				ips = append(ips, &ip.Address)
			} else {
				return nil, errors.Errorf("unable to check if interface has a sandbox due to index being out of range")
			}
		}
	}
	if len(ips) < 1 {
		return nil, ErrNoIPAddressFound
	}
	return ips, nil
}

// isInterfaceIndexSandox determines if the given interface index has the sandbox
// attribute and the value is greater than 0
func isInterfaceIndexSandox(idx int, r *current.Result) bool {
	if idx >= 0 && idx < len(r.Interfaces) {
		return len(r.Interfaces[idx].Sandbox) > 0
	}
	return false
}

// getInterfaceAddresses gets all globalunicast IP addresses for a given
// interface
func getInterfaceAddresses(nameConf dnsNameFile) ([]string, error) {
	var nameservers []string
	nic, err := net.InterfaceByName(nameConf.NetworkInterface)
	if err != nil {
		return nil, err
	}
	addrs, err := nic.Addrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			return nil, err
		}
		if ip.IsGlobalUnicast() {
			nameservers = append(nameservers, ip.String())
		}
	}
	return nameservers, nil
}
