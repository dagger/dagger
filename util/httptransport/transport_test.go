package httptransport

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// New must keep tracking Go's DefaultTransport settings.
func TestNewMatchesDefaultTransport(t *testing.T) {
	def, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok, "http.DefaultTransport is %T", http.DefaultTransport)
	tr := New()

	require.NotNil(t, tr.Proxy)
	require.NotNil(t, tr.DialContext)
	require.Equal(t, def.ForceAttemptHTTP2, tr.ForceAttemptHTTP2)
	require.Equal(t, def.MaxIdleConns, tr.MaxIdleConns)
	require.Equal(t, def.MaxIdleConnsPerHost, tr.MaxIdleConnsPerHost)
	require.Equal(t, def.MaxConnsPerHost, tr.MaxConnsPerHost)
	require.Equal(t, def.IdleConnTimeout, tr.IdleConnTimeout)
	require.Equal(t, def.TLSHandshakeTimeout, tr.TLSHandshakeTimeout)
	require.Equal(t, def.ExpectContinueTimeout, tr.ExpectContinueTimeout)
	require.Equal(t, def.ResponseHeaderTimeout, tr.ResponseHeaderTimeout)
	require.Equal(t, def.DisableKeepAlives, tr.DisableKeepAlives)
	require.Equal(t, def.DisableCompression, tr.DisableCompression)

	require.NotSame(t, New(), tr, "each call gets its own connection pool")
}
