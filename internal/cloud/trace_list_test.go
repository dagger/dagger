package cloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTraceListRequest(t *testing.T) {
	var got struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/query", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"org":{"traces":{
			"pageInfo":{"endCursor":"2026-09-24T10:00:00Z","hasNextPage":false},
			"nodes":[
			{"id":"t1","name":"dagger check","status":{"code":"STATUS_CODE_ERROR","message":""},
			 "timestamp":"2026-09-24T10:00:00Z","endTime":"2026-09-24T10:02:00Z","local":false,
			 "sender":null,"git":null,"ci":{"provider":"github","repository":null,
			 "change":{"id":"42","title":null,"url":null,"branch":null,"headSHA":"abc"}}}
		]}}}}`))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c := &Client{u: u, h: srv.Client()}

	local := false
	traces, err := c.TraceList(t.Context(), "acme", TraceListFilter{
		Repos:  []string{"acme/app"},
		Status: "FAILED",
		Local:  &local,
	}, TraceListSortDuration, 5)
	require.NoError(t, err)

	require.Equal(t, "TraceList", got.OperationName)
	require.Equal(t, "acme", got.Variables["org"])
	require.Equal(t, "DURATION", got.Variables["sort"])
	require.Equal(t, 5.0, got.Variables["first"])
	require.Equal(t, map[string]any{
		"repos":  []any{"acme/app", "https://acme/app", "github.com/acme/app", "https://github.com/acme/app"},
		"status": "FAILED",
		"local":  false,
	}, got.Variables["filter"], "unset filters are not sent; repos are expanded")

	require.Len(t, traces, 1)
	require.Equal(t, TraceStateFailed, traces[0].State())
	require.Equal(t, 2*time.Minute, traces[0].Duration(time.Now()))
	require.Equal(t, "42", traces[0].CI.Change.ID)
}

// Org.traces is a connection, so a listing larger than one page follows the
// cursor. The cursor must be echoed back exactly: it is a nanosecond timestamp,
// and reformatting it would truncate it and skip traces sharing that second.
func TestTraceListPaginates(t *testing.T) {
	const cursor = "2026-09-24T10:00:00.123456789Z"
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests = append(requests, body.Variables)

		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			_, _ = fmt.Fprintf(w, `{"data":{"org":{"traces":{
				"pageInfo":{"endCursor":%q,"hasNextPage":true},
				"nodes":[{"id":"t1","timestamp":"2026-09-24T10:01:00Z"},
				         {"id":"t2","timestamp":"2026-09-24T10:00:00Z"}]}}}}`, cursor)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"org":{"traces":{
			"pageInfo":{"endCursor":null,"hasNextPage":false},
			"nodes":[{"id":"t3","timestamp":"2026-09-24T09:59:00Z"}]}}}}`))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c := &Client{u: u, h: srv.Client()}

	traces, err := c.TraceList(t.Context(), "acme", TraceListFilter{}, TraceListSortStart, 250)
	require.NoError(t, err)

	require.Len(t, requests, 2)
	require.Nil(t, requests[0]["after"], "the first page carries no cursor")
	require.Equal(t, cursor, requests[1]["after"], "the cursor is echoed back unchanged")

	require.Len(t, traces, 3)
	require.Equal(t, []string{"t1", "t2", "t3"}, []string{traces[0].ID, traces[1].ID, traces[2].ID})
}

// The caller's limit wins even when the server keeps offering pages.
func TestTraceListStopsAtLimit(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"org":{"traces":{
			"pageInfo":{"endCursor":"2026-09-24T10:00:00Z","hasNextPage":true},
			"nodes":[{"id":"t1"},{"id":"t2"}]}}}}`))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c := &Client{u: u, h: srv.Client()}

	traces, err := c.TraceList(t.Context(), "acme", TraceListFilter{}, TraceListSortStart, 2)
	require.NoError(t, err)
	require.Len(t, traces, 2)
	require.Equal(t, 1, pages, "no request beyond the limit")
}

// Duration order cannot be paged, so the client must not follow the cursor even
// if the server reports another page.
func TestTraceListDurationDoesNotPaginate(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"org":{"traces":{
			"pageInfo":{"endCursor":"2026-09-24T10:00:00Z","hasNextPage":true},
			"nodes":[{"id":"t1"}]}}}}`))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c := &Client{u: u, h: srv.Client()}

	traces, err := c.TraceList(t.Context(), "acme", TraceListFilter{}, TraceListSortDuration, 100)
	require.NoError(t, err)
	require.Len(t, traces, 1)
	require.Equal(t, 1, pages, "duration order is a single page")
}
