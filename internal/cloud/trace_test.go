package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamSpansRequestsRootByDefault(t *testing.T) {
	var got graphqlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/query", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("event: complete\n\n"))
		require.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	clientURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	client := &Client{u: clientURL, h: srv.Client()}
	err = client.StreamSpans(context.Background(), "org-1", "trace-1", "", func([]SpanData) {})
	require.NoError(t, err)

	require.Equal(t, "GetSpanUpdates", got.OpName)
	require.Equal(t, "org-1", got.Variables["orgID"])
	require.Equal(t, "trace-1", got.Variables["traceID"])
	require.Equal(t, true, got.Variables["root"])
	require.Nil(t, got.Variables["listen"])
}

func TestStreamSpansListensToSelectedSpan(t *testing.T) {
	var got graphqlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/query", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("event: complete\n\n"))
		require.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	clientURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	client := &Client{u: clientURL, h: srv.Client()}
	err = client.StreamSpans(context.Background(), "org-1", "trace-1", "1112131415161718", func([]SpanData) {})
	require.NoError(t, err)

	require.Equal(t, "GetSpanUpdates", got.OpName)
	require.Equal(t, "org-1", got.Variables["orgID"])
	require.Equal(t, "trace-1", got.Variables["traceID"])
	require.Equal(t, false, got.Variables["root"])
	require.Equal(t, []any{"1112131415161718"}, got.Variables["listen"])
}

func TestGetSpanRequestsSelectedSpan(t *testing.T) {
	var got graphqlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/query", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
			"data": {
				"trace": {
					"spans": [
						{
							"id": "0102030405060708",
							"traceId": "0102030405060708090a0b0c0d0e0f10",
							"name": "other"
						},
						{
							"id": "1112131415161718",
							"traceId": "0102030405060708090a0b0c0d0e0f10",
							"name": "selected"
						}
					]
				}
			}
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	clientURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	client := &Client{u: clientURL, h: srv.Client()}
	span, err := client.GetSpan(context.Background(), "org-1", "trace-1", "1112131415161718")
	require.NoError(t, err)

	require.Equal(t, "GetSpan", got.OpName)
	require.Contains(t, got.Query, "trace(org: $orgID, id: $traceID)")
	require.Contains(t, got.Query, "spans {")
	require.Equal(t, "org-1", got.Variables["orgID"])
	require.Equal(t, "trace-1", got.Variables["traceID"])
	require.NotContains(t, got.Variables, "spanID")
	require.Equal(t, "1112131415161718", span.ID)
	require.Equal(t, "selected", span.Name)
}
