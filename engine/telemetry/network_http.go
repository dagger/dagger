package telemetry

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// NetworkResponseTransport records HTTP response bodies before decompression.
// An explicit Accept-Encoding belongs to the caller: its decoder remains in
// charge. Otherwise this wrapper provides net/http's transparent gzip behavior.
// The base transport and its connection pool are shared unchanged.
func NetworkResponseTransport(base http.RoundTripper, record func(context.Context, int64)) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &networkResponseTransport{base: base, record: record}
}

type networkResponseTransport struct {
	base   http.RoundTripper
	record func(context.Context, int64)
}

func (t *networkResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	autoGzip := req.Header.Get("Accept-Encoding") == "" &&
		req.Header.Get("Range") == "" && req.Method != http.MethodHead
	if transport, ok := t.base.(*http.Transport); ok && transport.DisableCompression {
		autoGzip = false
	}
	if autoGzip {
		req = req.Clone(req.Context())
		// Explicit negotiation prevents Go from decoding before we count.
		req.Header.Set("Accept-Encoding", "gzip")
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	// Explicit encoding negotiation should leave decoding to this wrapper.
	// Fail if a custom transport violates that contract rather than silently
	// omitting the estimate or counting expanded bytes as transmitted bytes.
	if resp.Uncompressed {
		return nil, errors.Join(
			errors.New("cannot account for network bytes: underlying transport decompressed the response"),
			resp.Body.Close(),
		)
	}
	resp.Body = &networkResponseBody{ReadCloser: resp.Body, ctx: req.Context(), record: t.record}
	if autoGzip && strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		resp.Body = &networkGzipBody{body: resp.Body}
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
		resp.Uncompressed = true
	}
	return resp, nil
}

type networkResponseBody struct {
	io.ReadCloser
	ctx    context.Context
	record func(context.Context, int64)
}

func (r *networkResponseBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.record(r.ctx, int64(n))
	}
	return n, err
}

// Initialize lazily, as net/http does, so malformed gzip fails on Read.
type networkGzipBody struct {
	body io.ReadCloser
	zr   *gzip.Reader
	err  error
}

func (r *networkGzipBody) Read(p []byte) (int, error) {
	if r.zr == nil && r.err == nil {
		r.zr, r.err = gzip.NewReader(r.body)
	}
	if r.err != nil {
		return 0, r.err
	}
	return r.zr.Read(p)
}

func (r *networkGzipBody) Close() error {
	if r.zr != nil {
		_ = r.zr.Close()
	}
	return r.body.Close()
}
