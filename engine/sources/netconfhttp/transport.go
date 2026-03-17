package netconfhttp

import (
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
