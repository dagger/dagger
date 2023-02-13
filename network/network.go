package network

import (
	"fmt"

	"github.com/docker/docker/libnetwork/resolvconf"
)

const (
	DNSDomain = "dns.dagger"
	CIDR      = "10.87.0.0/16"
	Bridge    = "10.87.0.1"
)

func DockerDNSFlags() ([]string, error) {
	rc, err := resolvconf.Get()
	if err != nil {
		return nil, fmt.Errorf("get resolv.conf: %w", err)
	}

	flags := []string{
		"--dns", Bridge,
		"--dns-search", DNSDomain,
	}

	for _, ns := range resolvconf.GetNameservers(rc.Content, resolvconf.IP) {
		flags = append(flags, "--dns", ns)
	}

	for _, domain := range resolvconf.GetSearchDomains(rc.Content) {
		flags = append(flags, "--dns-search", domain)
	}

	for _, opt := range resolvconf.GetOptions(rc.Content) {
		flags = append(flags, "--dns-option", opt)
	}

	return flags, nil
}
