package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMonthUsage(t *testing.T) {
	t.Run("returns telemetry usage", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			require.Contains(t, string(body), "monthUsage")
			require.Contains(t, string(body), "2026-09-01")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"monthUsage":{"usage":1200,"cap":5000}}}`)
		}))
		defer srv.Close()

		u, err := testClient(t, srv.URL).MonthUsage(context.Background(), "org-1", "2026-09-01")
		require.NoError(t, err)
		require.NotNil(t, u)
		require.Equal(t, 1200, u.Usage)
		require.Equal(t, 5000, u.Cap)
	})

	t.Run("propagates API errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"errors":[{"message":"unauthorized"}]}`)
		}))
		defer srv.Close()

		_, err := testClient(t, srv.URL).MonthUsage(context.Background(), "org-1", "2026-09-01")
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "unauthorized"))
	})
}

func TestOrgComputeMinutes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "orgComputeMinutes")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"orgComputeMinutes":137}}`)
	}))
	defer srv.Close()

	minutes, err := testClient(t, srv.URL).OrgComputeMinutes(context.Background(), "org-1")
	require.NoError(t, err)
	require.Equal(t, 137, minutes)
}
