package netconfhttp

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/executor/oci"
)

// NewTransport returns a http.RoundTripper that will respect the settings in
// the given network configuration as a logical override to the host's
// /etc/hosts and/or /etc/resolv.conf.
func NewTransport(rt http.RoundTripper, dns *oci.DNSConfig) http.RoundTripper {
	resolver, domains := createResolver(dns)
	return &transport{
		rt:            rt,
		resolver:      resolver,
		searchDomains: domains,
	}
}

// NewDialTransportWithHostAliases returns a clone of rt that dials bound
// service aliases through Dagger DNS while leaving the request host unchanged.
func NewDialTransportWithHostAliases(rt *http.Transport, dns *oci.DNSConfig, hostAliases map[string]string) *http.Transport {
	resolver, domains := createResolver(dns)

	// Registry transports may be reused across resolves. Clone before installing
	// a service-specific DialContext so one binding cannot leak into another.
	cloned := rt.Clone()
	dialContext := cloned.DialContext
	if dialContext == nil {
		dialer := net.Dialer{}
		dialContext = dialer.DialContext
	}
	cloned.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err == nil {
			if alias, ok := hostAliases[host]; ok {
				// Keep the request host and TLS SNI as the registry name; only
				// the TCP dial target follows the bound service through Dagger DNS.
				addr, err = resolveHost(ctx, net.JoinHostPort(alias, port), resolver, domains)
				if err != nil {
					return nil, err
				}
			}
		}
		return dialContext(ctx, network, addr)
	}
	// A custom DialTLSContext would bypass DialContext for HTTPS requests.
	cloned.DialTLSContext = nil
	return cloned
}

type transport struct {
	rt http.RoundTripper

	resolver      *net.Resolver
	searchDomains []string
}

func (h *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if strings.Count(req.URL.Host, ".") == 0 && len(h.searchDomains) > 0 {
		var err error
		req.URL.Host, err = resolveHost(req.Context(), req.URL.Host, h.resolver, h.searchDomains)
		if err != nil {
			return nil, err
		}
	}
	return h.rt.RoundTrip(req)
}
