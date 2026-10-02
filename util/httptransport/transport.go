// Package httptransport builds HTTP transports with Go's default settings.
package httptransport

import (
	"net"
	"net/http"
	"time"
)

// New returns a new transport with the settings of Go's DefaultTransport and
// its own connection pool, for callers that need to configure a transport or
// keep their connections separate.
//
// Prefer it to cloning http.DefaultTransport: anything may replace that
// variable with a RoundTripper that is not an *http.Transport, which makes the
// usual type assertion panic.
func New() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
