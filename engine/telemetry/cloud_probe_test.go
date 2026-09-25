package telemetry

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Any HTTP answer from the Cloud URL means the engine reaches it: the probe
// is an unauthenticated HEAD of the traces endpoint and follows no redirect.
func TestProbeCloudURLAnyAnswerIsReachable(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusMethodNotAllowed, http.StatusUnauthorized, http.StatusInternalServerError, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			requests := make(chan *http.Request, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r
				if status == http.StatusFound {
					w.Header().Set("Location", "http://unreachable.invalid/")
				}
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)

			require.NoError(t, ProbeCloudURL(context.Background(), srv.URL))
			req := <-requests
			require.Equal(t, http.MethodHead, req.Method)
			require.Equal(t, "/v1/traces", req.URL.Path)
			require.Empty(t, req.Header.Get("Authorization"))
		})
	}
}

// Without an answer the probe fails, and never outlasts its context.
func TestProbeCloudURLUnreachable(t *testing.T) {
	t.Parallel()
	refused := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())
		return "http://" + addr
	}()
	stall := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-stall }))
	t.Cleanup(func() {
		close(stall)
		slow.Close()
	})

	for name, cloudURL := range map[string]string{
		"unresolvable": "http://unreachable.invalid",
		"refused":      refused,
		"no answer":    slow.URL,
		"bad URL":      "http://[::1]:namedport",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			const bound = 500 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), bound)
			defer cancel()
			start := time.Now()
			require.Error(t, ProbeCloudURL(ctx, cloudURL))
			require.Less(t, time.Since(start), bound+time.Second)
		})
	}
}

func TestResolveCloudURL(t *testing.T) {
	t.Setenv("DAGGER_CLOUD_URL", "")
	require.Equal(t, "https://api.dagger.cloud", ResolveCloudURL(""))
	require.Equal(t, "http://cloud:8080", ResolveCloudURL("http://cloud:8080"))
	t.Setenv("DAGGER_CLOUD_URL", "http://engine-env")
	require.Equal(t, "http://engine-env", ResolveCloudURL(""))
	require.Equal(t, "http://cloud:8080", ResolveCloudURL("http://cloud:8080"))
}
