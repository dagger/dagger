package netconfhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/moby/buildkit/executor/oci"
)

// NewTransport returns a http.RoundTripper that will respect the settings in
// the given network configuration as a logical override to the host's
// /etc/hosts and/or /etc/resolv.conf.
func NewTransport(rt http.RoundTripper, dns *oci.DNSConfig) http.RoundTripper {
	var domains []string
	var resolver *net.Resolver
	if dns == nil {
		resolver = net.DefaultResolver
	} else {
		domains = dns.SearchDomains

		dialer := net.Dialer{}
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if len(dns.Nameservers) == 0 {
					return nil, errors.New("no nameservers configured")
				}

				var errs []error
				for _, ns := range dns.Nameservers {
					conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
					if err != nil {
						errs = append(errs, err)
						continue
					}

					return conn, nil
				}

				return nil, errors.Join(errs...)
			},
		}
	}

	return &transport{
		rt:            rt,
		resolver:      resolver,
		searchDomains: domains,
	}
}

type transport struct {
	rt http.RoundTripper

	resolver      *net.Resolver
	searchDomains []string
}

func (h *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Count(req.URL.Host, ".") == 0 && len(h.searchDomains) > 0 {
		if host, port, err := net.SplitHostPort(req.URL.Host); err == nil {
			ip, err := h.lookup(req.Context(), host)
			if err != nil {
				return nil, err
			}

			req.URL.Host = net.JoinHostPort(ip.String(), port)
		} else {
			ip, err := h.lookup(req.Context(), req.URL.Host)
			if err != nil {
				return nil, err
			}

			req.URL.Host = ip.String()
		}
	}

	return h.rt.RoundTrip(req)
}

func (h *transport) lookup(ctx context.Context, target string) (net.IP, error) {
	var errs []error
	for _, domain := range append([]string{""}, h.searchDomains...) {
		qualified := target

		if domain != "" {
			qualified += "." + domain
		}

		ips, err := h.resolver.LookupIPAddr(ctx, qualified)
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
