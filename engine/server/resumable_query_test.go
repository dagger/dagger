package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResumableQueryOutlivesItsConnection(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runs.Add(1)
		body, _ := io.ReadAll(r.Body)
		select {
		case <-release:
			w.Write(body)
		case <-r.Context().Done():
		}
	})
	var qs resumableQueries
	send := func(ctx context.Context, key string, resend bool) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/query", strings.NewReader("q"))
		qs.serve(rec, req, key, resend, time.Minute, handler)
		return rec
	}

	ctx, drop := context.WithCancel(context.Background())
	dropped := make(chan struct{})
	go func() {
		send(ctx, "id", false)
		close(dropped)
	}()
	require.Eventually(t, func() bool { return runs.Load() == 1 }, 5*time.Second, time.Millisecond)
	drop()
	<-dropped

	close(release)
	require.Equal(t, "q", send(context.Background(), "id", true).Body.String())
	require.EqualValues(t, 1, runs.Load())

	// a released query, or one the engine never had, is not run again
	qs.release("id")
	require.Equal(t, http.StatusGone, send(context.Background(), "id", true).Code)
	require.Equal(t, http.StatusGone, send(context.Background(), "other", true).Code)
	require.EqualValues(t, 1, runs.Load())
}
