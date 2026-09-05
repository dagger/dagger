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
	registered     atomic.Bool
	registeredWith atomic.Value // string parent client ID
	servedWith     atomic.Value // string caller client ID
	closed         atomic.Int32
	served         atomic.Int32
}

func (srv *nestedTransportTestServer) RegisterNestedClientTransport(
	_ context.Context,
	_ *engine.ClientMetadata,
	parentClientID string,
) (*engine.NestedClientTransport, error) {
	srv.registered.Store(true)
	srv.registeredWith.Store(parentClientID)
	return engine.NewNestedClientTransport(func() { srv.closed.Add(1) }), nil
}

func (srv *nestedTransportTestServer) ServeHTTPToNestedClient(
	_ http.ResponseWriter,
	_ *http.Request,
	_ *engine.NestedClientTransport,
	_ *engine.ClientMetadata,
	callerClientID string,
	_ bool,
	_ dagql.AnyObjectResult,
	_ dagql.Typed,
) {
	srv.served.Add(1)
	srv.servedWith.Store(callerClientID)
}

// scopedContext returns a context holding a request scope for the given client
// in the "session" session, as an in-engine SDK call runs under.
func scopedContext(t *testing.T, clientID string) context.Context {
	t.Helper()
	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, clientID, func() {}, nil)
	t.Cleanup(lease.Release)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{
		SessionID: "session",
		ClientID:  clientID,
	}, lease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	return ctx
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
		scopedContext(t, "executable-client"),
		query,
		metadata,
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

// TestWithNestedClientServerRegistersUnderHeldScope locks in that the nested
// client's parent is the client whose scope the context holds, not any client
// its own metadata names. Workspace operations such as local-dependency codegen
// run under a scope other than the client their request metadata names, and the
// session rejects a child registered under the wrong parent.
func TestWithNestedClientServerRegistersUnderHeldScope(t *testing.T) {
	t.Parallel()

	srv := &nestedTransportTestServer{}
	query := &core.Query{Server: srv}
	metadata := &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "child",
	}

	_, err := WithNestedClientServer(
		scopedContext(t, "workspace-owner"),
		query,
		metadata,
		false,
		nil,
		dagql.ObjectResult[*core.Module]{},
		func(context.Context, graphql.Client) ([]byte, error) { return nil, nil },
	)
	require.NoError(t, err)
	require.Equal(t, "workspace-owner", srv.registeredWith.Load(), "parent must come from the held scope")
}

// TestWithNestedClientServerRequiresHeldScope locks in that a nested client
// cannot be created without a held parent scope, rather than falling back to an
// implicit parent from request metadata.
func TestWithNestedClientServerRequiresHeldScope(t *testing.T) {
	t.Parallel()

	srv := &nestedTransportTestServer{}
	query := &core.Query{Server: srv}
	metadata := &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "child",
	}

	_, err := WithNestedClientServer(
		context.Background(),
		query,
		metadata,
		false,
		nil,
		dagql.ObjectResult[*core.Module]{},
		func(context.Context, graphql.Client) ([]byte, error) { return nil, nil },
	)
	require.Error(t, err)
	require.False(t, srv.registered.Load(), "no transport may be registered without a held scope")
}
