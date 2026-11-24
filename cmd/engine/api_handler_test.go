package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/server"
)

// The main listener serves GET /v1/status to the status handler, outside any
// session, gRPC to the control API, and everything else to the sessions.
func TestAPIHandlerServesTheStatusOutsideSessions(t *testing.T) {
	t.Parallel()
	var served []string
	named := func(name string) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = append(served, name) })
	}
	handler := apiHandler(named("grpc"), named("status"), named("sessions"))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, server.StatusPath, nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/query", nil))
	grpcReq := httptest.NewRequest(http.MethodPost, "/moby.buildkit.v1.Control/Session", nil)
	grpcReq.ProtoMajor = 2
	grpcReq.Header.Set("content-type", "application/grpc")
	handler.ServeHTTP(httptest.NewRecorder(), grpcReq)
	require.Equal(t, []string{"status", "sessions", "grpc"}, served)
}
