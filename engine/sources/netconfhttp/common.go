package netconfhttp

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/dagger/dagger/internal/buildkit/executor/oci"
)

func createResolver(dns *oci.DNSConfig) (*net.Resolver, []string) {
	if dns == nil {
		return net.DefaultResolver, nil
	}
	dialer := net.Dialer{}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialNameservers(ctx, network, dns.Nameservers, &dialer)
		},
	}
	return resolver, dns.SearchDomains
}

func dialNameservers(ctx context.Context, network string, nameservers []string, dialer *net.Dialer) (net.Conn, error) {
	if len(nameservers) == 0 {
		return nil, errors.New("no nameservers configured")
	}

	var errs []error
	for _, ns := range nameservers {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return conn, nil
	}

	return nil, errors.Join(errs...)
}

func resolveHost(ctx context.Context, target string, resolver *net.Resolver, searchDomains []string) (string, error) {
	if host, port, err := net.SplitHostPort(target); err == nil {
		ip, err := lookup(ctx, host, resolver, searchDomains)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(ip.String(), port), nil
	} else {
		ip, err := lookup(ctx, target, resolver, searchDomains)
		if err != nil {
			return "", err
		}
		return ip.String(), nil
	}
}

func lookup(ctx context.Context, target string, resolver *net.Resolver, searchDomains []string) (net.IP, error) {
	var errs []error
	for _, domain := range append([]string{""}, searchDomains...) {
		qualified := target
		if domain != "" {
			qualified += "." + domain
		}

		ips, err := resolver.LookupIPAddr(ctx, qualified)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if len(ips) > 0 {
			return ips[0].IP, nil
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return nil, fmt.Errorf("no IPs found for %s", target)
}
