package cloud

import (
	"encoding/json"
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
		_, _ = w.Write([]byte(`{"data":{"org":{"traceList":[
			{"id":"t1","name":"dagger check","status":{"code":"STATUS_CODE_ERROR","message":""},
			 "timestamp":"2026-09-24T10:00:00Z","endTime":"2026-09-24T10:02:00Z","local":false,
			 "sender":null,"git":null,"ci":{"provider":"github","repository":null,
			 "change":{"id":"42","title":null,"url":null,"branch":null,"headSHA":"abc"}}}
		]}}}`))
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
