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

	// NB: we only want IPv4 addresses because some resolvconfs include IPv6
	// addresses with a 'zone' affixed (e.g. fe80::1%2) which a) the --dns flag
	// can't handle and b) might be referring to an interface unreachable by the
	// container anyway.
	for _, ns := range resolvconf.GetNameservers(rc.Content, resolvconf.IPv4) {
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
