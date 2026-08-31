package engineutil

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/stretchr/testify/require"
)

type nestedTransportSessionHandler struct {
	registered atomic.Int32
	closed     atomic.Int32
	served     atomic.Int32
}

func (handler *nestedTransportSessionHandler) RegisterNestedClientTransport(
	context.Context,
	*engine.ClientMetadata,
	string,
) (*engine.NestedClientTransport, error) {
	handler.registered.Add(1)
	return engine.NewNestedClientTransport(func() { handler.closed.Add(1) }), nil
}

func (handler *nestedTransportSessionHandler) ServeHTTPToNestedClient(
	http.ResponseWriter,
	*http.Request,
	*engine.NestedClientTransport,
	*engine.ClientMetadata,
	string,
	bool,
	dagql.AnyObjectResult,
	dagql.Typed,
) {
	handler.served.Add(1)
}

func (handler *nestedTransportSessionHandler) ServeExecHTTP(string, string, http.ResponseWriter, *http.Request) {
	handler.served.Add(1)
}

func TestContainerNestedTransportClosesWithoutRequest(t *testing.T) {
	t.Parallel()

	handler := &nestedTransportSessionHandler{}
	client := &Client{Opts: &Opts{SessionHandler: handler}}
	state := &execState{
		callerClientID: "parent",
		nestedClientMetadata: &engine.ClientMetadata{
			SessionID: "session",
			ClientID:  "child",
		},
		cleanups: &cleanups.Cleanups{},
	}

	transport, err := client.registerNestedClientTransport(t.Context(), state)
	require.NoError(t, err)
	require.False(t, transport.Closed())
	require.Equal(t, int32(1), handler.registered.Load())
	require.Zero(t, handler.served.Load())

	require.NoError(t, state.cleanups.Run())
	require.True(t, transport.Closed())
	require.Equal(t, int32(1), handler.closed.Load())
	require.NoError(t, state.cleanups.Run())
	require.Equal(t, int32(1), handler.closed.Load(), "cleanup and handle close must both be idempotent")
}
