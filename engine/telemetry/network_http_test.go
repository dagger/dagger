package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type networkTestTransport func(*http.Request) (*http.Response, error)

func (f networkTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type networkTestBody struct {
	io.Reader
	closed   bool
	closeErr error
}

func (b *networkTestBody) Close() error {
	b.closed = true
	return b.closeErr
}

func TestNetworkResponseTransportDecodedBody(t *testing.T) {
	closeErr := errors.New("close failed")
	for _, tc := range []struct {
		name     string
		decoded  bool
		closeErr error
	}{
		{name: "plain response"},
		{name: "already decoded", decoded: true},
		{name: "already decoded with close error", decoded: true, closeErr: closeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &networkTestBody{Reader: strings.NewReader("plain body"), closeErr: tc.closeErr}
			var counted int64
			transport := NetworkResponseTransport(networkTestTransport(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "gzip", req.Header.Get("Accept-Encoding"))
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Uncompressed: tc.decoded}, nil
			}), func(_ context.Context, n int64) { counted += n })
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
			require.NoError(t, err)
			resp, err := transport.RoundTrip(req)
			if tc.decoded {
				require.ErrorContains(t, err, "underlying transport decompressed the response")
				if tc.closeErr != nil {
					require.ErrorIs(t, err, tc.closeErr)
				}
				require.Nil(t, resp)
				require.True(t, body.closed)
				require.Zero(t, counted)
				return
			}
			require.NoError(t, err)
			require.False(t, resp.Uncompressed)
			got, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "plain body", string(got))
			require.EqualValues(t, len(got), counted)
			require.NoError(t, resp.Body.Close())
		})
	}
}

func TestNetworkResponseTransport(t *testing.T) {
	payload := strings.Repeat("data: streamed response\n\n", 1024)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := io.WriteString(zw, payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	for _, explicit := range []bool{false, true} {
		name := "automatic gzip"
		if explicit {
			name = "caller decodes gzip"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "gzip", r.Header.Get("Accept-Encoding"))
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				_, _ = w.Write(compressed.Bytes())
			}))
			defer server.Close()
			var counted int64
			transport := NetworkResponseTransport(server.Client().Transport, func(_ context.Context, n int64) {
				counted += n
			})
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			if explicit {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			require.Zero(t, counted, "RoundTrip must not eagerly read streaming bodies")
			var body io.Reader = resp.Body
			if explicit {
				require.False(t, resp.Uncompressed)
				require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
				body, err = gzip.NewReader(body)
				require.NoError(t, err)
			} else {
				require.Empty(t, req.Header.Get("Accept-Encoding"), "leave caller's request unchanged")
				require.True(t, resp.Uncompressed)
				require.Empty(t, resp.Header.Get("Content-Encoding"))
			}
			got, err := io.ReadAll(body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, payload, string(got))
			require.EqualValues(t, compressed.Len(), counted)
		})
	}
}
