package dangshared

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type embeddedCoreServer interface {
	core.Server
}

type nestedTransportTestServer struct {
	embeddedCoreServer
	registered atomic.Bool
	closed     atomic.Int32
	served     atomic.Int32
}

func (srv *nestedTransportTestServer) RegisterNestedClientTransport(
	context.Context,
	*engine.ClientMetadata,
	string,
) (*engine.NestedClientTransport, error) {
	srv.registered.Store(true)
	return engine.NewNestedClientTransport(func() { srv.closed.Add(1) }), nil
}

func (srv *nestedTransportTestServer) ServeHTTPToNestedClient(
	http.ResponseWriter,
	*http.Request,
	*engine.NestedClientTransport,
	*engine.ClientMetadata,
	string,
	bool,
	dagql.AnyObjectResult,
	dagql.Typed,
) {
	srv.served.Add(1)
}

func TestWithNestedClientServerClosesRegisteredTransportWithoutRequest(t *testing.T) {
	t.Parallel()

	srv := &nestedTransportTestServer{}
	query := &core.Query{Server: srv}
	metadata := &engine.ClientMetadata{
		SessionID:         "session",
		ClientID:          "child",
		ClientSecretToken: "secret",
	}

	out, err := WithNestedClientServer(
		t.Context(),
		query,
		metadata,
		"parent",
		false,
		nil,
		dagql.ObjectResult[*core.Module]{},
		func(context.Context, graphql.Client) ([]byte, error) { return []byte("ok"), nil },
	)
	require.NoError(t, err)
	require.Equal(t, []byte("ok"), out)
	require.True(t, srv.registered.Load(), "registration must happen before user code can exit")
	require.Zero(t, srv.served.Load(), "the no-request path must not initialize through HTTP")
	require.Equal(t, int32(1), srv.closed.Load(), "proxy exit must close transport exactly once")
}
