package netconfhttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/executor/oci"
)

type dnsConfigKey struct{}

// WithDNSConfig adds DNS configuration to a context
func WithDNSConfig(ctx context.Context, dns *oci.DNSConfig) context.Context {
	if dns == nil {
		return ctx
	}
	return context.WithValue(ctx, dnsConfigKey{}, dns)
}

// NewInjectableTransport returns a http.RoundTripper that extracts DNS configuration
// from each request's context to determine the appropriate resolver.
func NewInjectableTransport(rt http.RoundTripper) http.RoundTripper {
	return &injectableTransport{
		rt: rt,
	}
}

type injectableTransport struct {
	rt http.RoundTripper
}

func (t *injectableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var dnsConfig *oci.DNSConfig
	if v := req.Context().Value(dnsConfigKey{}); v != nil {
		dnsConfig = v.(*oci.DNSConfig)
	}
	resolver, searchDomains := createResolver(dnsConfig)

	if strings.Count(req.URL.Host, ".") == 0 && len(searchDomains) > 0 {
		var err error
		req = req.Clone(req.Context())
		req.URL.Host, err = resolveHost(req.Context(), req.URL.Host, resolver, searchDomains)
		if err != nil {
			return nil, err
		}
	}

	return t.rt.RoundTrip(req)
}
