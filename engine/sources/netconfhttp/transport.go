package netconfhttp

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/dagger/dagger/engine/session/networks"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

// NewTransport returns a http.RoundTripper that will respect the settings in
// the given network configuration as a logical override to the host's
// /etc/hosts and/or /etc/resolv.conf.
func NewTransport(rt http.RoundTripper, netConfig *networks.Config) http.RoundTripper {
	hostsMap := map[string]string{}
	for _, ipHosts := range netConfig.IpHosts {
		for _, host := range ipHosts.Hosts {
			hostsMap[host] = ipHosts.Ip
		}
	}

	var domains []string
	var resolver *net.Resolver
	if netConfig.Dns == nil {
		resolver = net.DefaultResolver
	} else {
		domains = netConfig.Dns.SearchDomains

		dialer := net.Dialer{}
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if len(netConfig.Dns.Nameservers) == 0 {
					return nil, errors.New("no nameservers configured")
				}

				var errs error
				for _, ns := range netConfig.Dns.Nameservers {
					conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
					if err != nil {
						errs = multierror.Append(errs, err)
						continue
					}

					return conn, nil
				}

				return nil, errs
			},
		}
	}

	return &transport{
		rt:            rt,
		resolver:      resolver,
		hosts:         hostsMap,
		searchDomains: domains,
	}
}

type transport struct {
	rt http.RoundTripper

	resolver      *net.Resolver
	hosts         map[string]string
	searchDomains []string
}

func (h *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var remapped bool
	if len(h.hosts) > 0 {
		if host, port, err := net.SplitHostPort(req.URL.Host); err == nil {
			remap, found := h.hosts[host]
			if found {
				req.URL.Host = net.JoinHostPort(remap, port)
				remapped = true
			}
		} else {
			remap, found := h.hosts[req.URL.Host]
			if found {
				req.URL.Host = remap
				remapped = true
			}
		}
	}

	if !remapped && strings.Count(req.URL.Host, ".") == 0 && len(h.searchDomains) > 0 {
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
	var errs error
	for _, domain := range append([]string{""}, h.searchDomains...) {
		qualified := target

		if domain != "" {
			qualified += "." + domain
		}

		ips, err := h.resolver.LookupIPAddr(ctx, qualified)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}

		if len(ips) > 0 {
			return ips[0].IP, nil
		}
	}
	if errs != nil {
		return nil, errs
	}

	return nil, errors.Errorf("no IPs found for %s", target)
}
