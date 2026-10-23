package daggercmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/client"
	"github.com/stretchr/testify/require"
)

func TestSessionServerRefusesCLIClients(t *testing.T) {
	srv := sessionServer(context.Background(), "dagger run", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"version":"test"}}`))
	}))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(l)
	defer srv.Close()
	port := l.Addr().(*net.TCPAddr).Port

	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/query", port), "application/json", strings.NewReader(`{"query":"{version}"}`))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	t.Setenv("DAGGER_SESSION_PORT", strconv.Itoa(port))
	t.Setenv("DAGGER_SESSION_TOKEN", "child-token")
	_, err = client.Connect(context.Background(), client.Params{})
	require.ErrorContains(t, err, "connecting a Dagger CLI to the session served by `dagger run` is not supported yet")
}
