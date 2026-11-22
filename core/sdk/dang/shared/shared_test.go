package dangshared

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

type embeddedCoreServer interface {
	core.Server
}

type nestedTransportTestServer struct {
	embeddedCoreServer
	registered     atomic.Bool
	registeredWith atomic.Value // string parent client ID
	servedWith     atomic.Value // string caller client ID
	servedOn       atomic.Value // string host the request was sent to
	traceparents   chan string
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
	w http.ResponseWriter,
	req *http.Request,
	_ *engine.NestedClientTransport,
	_ *engine.ClientMetadata,
	callerClientID string,
	_ bool,
	_ dagql.AnyObjectResult,
	_ dagql.Typed,
) {
	srv.served.Add(1)
	srv.servedWith.Store(callerClientID)
	srv.servedOn.Store(req.Host)
	if srv.traceparents != nil {
		srv.traceparents <- req.Header.Get("traceparent")
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":{}}`))
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

// TestWithNestedClientServerKeepsRequestSpan locks in that a request made
// under a span of its own, such as an entrypoint's module build, is served
// under that span, and any other under the invocation's.
func TestWithNestedClientServerKeepsRequestSpan(t *testing.T) {
	t.Parallel()

	spanCtx := func(ctx context.Context, spanID byte) (context.Context, string) {
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     trace.SpanID{spanID},
			TraceFlags: trace.FlagsSampled,
		})
		return trace.ContextWithSpanContext(ctx, sc), "00-" + sc.TraceID().String() + "-" + sc.SpanID().String() + "-01"
	}
	srv := &nestedTransportTestServer{traceparents: make(chan string, 3)}
	query := &core.Query{Server: srv}
	metadata := &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "child",
	}
	ctx, invocation := spanCtx(scopedContext(t, "caller"), 1)
	var own string
	_, err := WithNestedClientServer(
		ctx,
		query,
		metadata,
		false,
		nil,
		dagql.ObjectResult[*core.Module]{},
		func(ctx context.Context, client graphql.Client) ([]byte, error) {
			request := func(ctx context.Context) {
				// Only the request matters, not its response.
				_ = client.MakeRequest(ctx, &graphql.Request{Query: "{ version }"}, &graphql.Response{})
			}
			request(ctx)
			request(trace.ContextWithSpanContext(ctx, trace.SpanContext{}))
			var ownCtx context.Context
			ownCtx, own = spanCtx(ctx, 2)
			request(ownCtx)
			return nil, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, invocation, <-srv.traceparents, "a request under the invocation's span")
	require.Equal(t, invocation, <-srv.traceparents, "a request under no span")
	require.Equal(t, own, <-srv.traceparents, "a request under a span of its own")
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

// TestWithNestedClientServerAnswersLongUnusedConnection locks in that the
// server answers the first request on a connection however long the
// connection waited for it. The client's pool keeps connections it dialed and
// has not sent a request on yet (TestServeNestedClientDoesNotWaitOnUnusedConnection
// builds one), and a connection the server closes unasked fails the request
// the pool hands it at that moment with EOF, which net/http does not retry for
// a POST. The test holds such a connection itself, so the request goes out at
// a fixed time rather than whenever the pool would hand the connection out.
func TestWithNestedClientServerAnswersLongUnusedConnection(t *testing.T) {
	t.Parallel()

	srv := &nestedTransportTestServer{}
	query := &core.Query{Server: srv}
	metadata := &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "child",
	}

	_, err := WithNestedClientServer(
		scopedContext(t, "executable-client"),
		query,
		metadata,
		false,
		nil,
		dagql.ObjectResult[*core.Module]{},
		func(ctx context.Context, gqlClient graphql.Client) ([]byte, error) {
			require.NoError(t, gqlClient.MakeRequest(ctx, &graphql.Request{Query: "{}"}, &graphql.Response{Data: &struct{}{}}))
			host := srv.servedOn.Load().(string)

			conn, err := net.Dial("tcp", host)
			require.NoError(t, err)
			defer conn.Close()
			// Past the 10s header timeout the server used to set, which
			// net/http counts from when the connection is accepted.
			time.Sleep(11 * time.Second)

			body := `{"query":"{}"}`
			_, err = fmt.Fprintf(conn, "POST /query HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", host, len(body), body)
			require.NoError(t, err)
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			require.NoError(t, err, "the server closed a connection before its first request")
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			return nil, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, int32(2), srv.served.Load(), "both requests reached the session")
}
