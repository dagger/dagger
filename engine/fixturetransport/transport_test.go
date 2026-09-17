package fixturetransport

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type delegate struct{ calls int }

func (d *delegate) RoundTrip(req *http.Request) (*http.Response, error) {
	d.calls++
	return &http.Response{StatusCode: http.StatusTeapot, Body: http.NoBody, Request: req}, nil
}

func get(t *testing.T, rt http.RoundTripper, url string, header map[string]string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	for key, value := range header {
		req.Header.Set(key, value)
	}
	return rt.RoundTrip(req)
}

func TestFixtureTransport(t *testing.T) {
	current.Store(nil)
	t.Cleanup(func() { current.Store(nil) })
	base := &delegate{}
	require.Same(t, http.RoundTripper(base), Wrap(base), "off-gate the original transport is returned unchanged")
	require.Nil(t, Current())

	_, err := Enable("relative")
	require.Error(t, err)
	root := t.TempDir()
	d, err := Enable(root)
	require.NoError(t, err)
	again, err := Enable(t.TempDir())
	require.NoError(t, err)
	require.Same(t, d, again, "one dispatcher per process")
	rt := Wrap(base)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "origins"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "origins", "data.json"), []byte("0123456789"), 0600))
	_, err = d.SetScript(Script{Responses: []Response{{URL: "https://origin.remote-cache.invalid/x", BodyFile: "../escape"}}})
	require.ErrorContains(t, err, "not contained")
	_, err = d.SetScript(Script{Responses: []Response{{URL: "https://origin.remote-cache.invalid/x", Fault: "anything"}}})
	require.ErrorContains(t, err, "unknown fault")
	generation, err := d.SetScript(Script{Responses: []Response{
		{URL: "https://origin.remote-cache.invalid/data.json", BodyFile: "origins/data.json", Headers: map[string]string{"ETag": `"v1"`}},
		{URL: "https://content.remote-cache.invalid/blob", BodyFile: "origins/data.json", Rangeable: true},
		{URL: "https://content.remote-cache.invalid/short", BodyFile: "origins/data.json", Fault: "truncate", TruncateAt: 4},
		{URL: "https://content.remote-cache.invalid/down", Fault: "transport"},
		{Method: http.MethodGet, URL: "https://origin.remote-cache.invalid/gone", Status: http.StatusNotFound},
	}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), generation)

	resp, err := get(t, rt, "https://origin.remote-cache.invalid/data.json", map[string]string{"If-None-Match": `"v0"`, "Authorization": "Bearer secret"})
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "0123456789", string(body))
	require.Equal(t, `"v1"`, resp.Header.Get("ETag"))

	resp, err = get(t, rt, "https://content.remote-cache.invalid/blob", map[string]string{"Range": "bytes=2-5"})
	require.NoError(t, err)
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	require.Equal(t, "bytes 2-5/10", resp.Header.Get("Content-Range"))
	body, _ = io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "2345", string(body))

	resp, err = get(t, rt, "https://content.remote-cache.invalid/short", nil)
	require.NoError(t, err)
	body, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, "0123", string(body), "the bytes before the truncation are real")
	require.NoError(t, resp.Body.Close())

	_, err = get(t, rt, "https://content.remote-cache.invalid/down", nil)
	require.ErrorIs(t, err, ErrFault)
	resp, err = get(t, rt, "https://origin.remote-cache.invalid/gone", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	_, err = get(t, rt, "https://origin.remote-cache.invalid/unknown", nil)
	require.ErrorIs(t, err, ErrUnscripted, "an unknown fixture URL fails explicitly")
	require.Zero(t, base.calls, "no fixture-host request ever reaches the delegate")

	resp, err = get(t, rt, "https://registry.example.com/v2/", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusTeapot, resp.StatusCode, "every other host uses the original transport")
	require.Equal(t, 1, base.calls)

	report := d.Report()
	require.Equal(t, uint64(1), report.Delegated)
	require.Equal(t, uint64(6), report.FixtureHosts)
	require.Len(t, report.Requests, 6)
	first := report.Requests[0]
	require.Equal(t, `"v0"`, first.IfNoneMatch)
	require.True(t, first.Authorization, "only the presence of credentials is recorded")
	require.Equal(t, int64(10), first.BodyBytesRead)
	require.True(t, first.Closed)
	require.Equal(t, "bytes=2-5", report.Requests[1].Range)
	require.Equal(t, int64(4), report.Requests[2].BodyBytesRead)
	require.NotEmpty(t, report.Requests[5].Error)

	d.SetObservationCap(1)
	_, _ = get(t, rt, "https://origin.remote-cache.invalid/gone", nil)
	_, _ = get(t, rt, "https://origin.remote-cache.invalid/gone", nil)
	report = d.Report()
	require.Len(t, report.Requests, 1)
	require.True(t, report.Overflowed, "overflow is reported, never silent")
}
