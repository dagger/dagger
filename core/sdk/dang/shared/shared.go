// Package dangshared holds the plumbing of the Dang SDK that does not depend
// on the Dang library (core/sdk/dang/v2): the nested-client server, client
// metadata, error conversion and naming.
//
// Nothing in this package may import github.com/vito/dang (any major), so a
// future Dang major can be linked beside the current one without a copy of
// it. See core/sdk/dang/README.md.
package dangshared

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/wcprof"
	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// WithNestedClientServer serves the Dagger API for a nested client on a local
// listener and calls fn with a GraphQL client pointed at it. The server is
// shut down when fn returns; any error it hit while serving is returned.
// In-engine SDK clients pass inertAttachables to reject implicit access to the
// caller's host while still allowing graph values explicitly passed to them.
//
// The nested client's parent is the client whose scope ctx holds, exactly as
// for container execs: workspace operations run under a scope other than the
// client their request metadata names, and the session only accepts a child
// registered by its held parent scope.
func WithNestedClientServer(
	ctx context.Context,
	query *core.Query,
	nestedClientMetadata *engine.ClientMetadata,
	inertAttachables bool,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
	fn func(ctx context.Context, gqlClient graphql.Client) ([]byte, error),
) ([]byte, error) {
	parentClientID, err := engine.NestedClientParentID(ctx, nestedClientMetadata.SessionID)
	if err != nil {
		return nil, err
	}

	transport, err := query.RegisterNestedClientTransport(ctx, nestedClientMetadata, parentClientID)
	if err != nil {
		return nil, fmt.Errorf("register nested client transport: %w", err)
	}
	defer transport.Close()
	wcprof.Link(ctx, wcprof.LinkKindNestedClient, 0, 0, nestedClientMetadata.ClientID, 0)

	httpSrv := &http.Server{
		// NOTE: no ReadHeaderTimeout (gosec G112). net/http arms it on a
		// connection as soon as it is accepted, before its first request, and
		// closes the connection without a reply once it passes. The client's
		// pool keeps connections it dialed and has not used yet (see
		// serveNestedClient), so it can hand a request one just as the server
		// closes it, and the request fails with EOF: net/http does not retry a
		// POST. The server's only peer is this engine's own client, over
		// loopback.
		Handler: http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			// A request made under a span of its own, such as an entrypoint's
			// module build, keeps it (see spanTransport); any other is the
			// invocation's.
			if req.Header.Get(traceparentHeader) == "" {
				telemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
			}
			// The code makes its calls on behalf of the resolver running under
			// ctx, so what they read counts toward what its result stands for.
			req = req.WithContext(dagql.WithCallScopesOf(req.Context(), ctx))
			query.ServeHTTPToNestedClient(resp, req, transport, nestedClientMetadata, parentClientID, inertAttachables, moduleContext, fnCall)
		}),
	}
	// Give the invocation its own connection pool rather than sharing the
	// process-wide http.DefaultTransport, so serveNestedClient can drain it.
	return serveNestedClient(ctx, httpSrv, http.DefaultTransport.(*http.Transport).Clone(), fn)
}

// serveNestedClient serves httpSrv on a loopback listener, calls fn with a
// GraphQL client that talks to it over httpTransport, then drains that
// transport and shuts the server down.
func serveNestedClient(
	ctx context.Context,
	httpSrv *http.Server,
	httpTransport *http.Transport,
	fn func(ctx context.Context, gqlClient graphql.Client) ([]byte, error),
) ([]byte, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	defer l.Close()

	defer func() {
		// A request that arrives while the pool's connection is busy starts a
		// second dial. If the busy request finishes first, the waiting one
		// reuses the freed connection and the new dial lands with nothing to
		// carry. That connection sits in StateNew on the server, and
		// http.Server.Shutdown only counts StateNew as idle after 5s, so the
		// invocation would hang there. Closing the pool's idle connections
		// first lets Shutdown return at once; requests still in flight are
		// drained by Shutdown as before.
		httpTransport.CloseIdleConnections()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer shutdownCancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	srvErrCh := make(chan error, 1)
	go func() {
		err := httpSrv.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			srvErrCh <- err
		}
		close(srvErrCh)
	}()

	gqlClient := graphql.NewClient(fmt.Sprintf("http://%s/query", l.Addr()), &http.Client{Transport: spanTransport{
		RoundTripper: httpTransport,
		serving:      trace.SpanContextFromContext(ctx),
	}})

	out, err := fn(ctx, gqlClient)
	if err != nil {
		return nil, err
	}

	if err := checkServerError(srvErrCh); err != nil {
		return nil, err
	}

	return out, nil
}

const traceparentHeader = "traceparent"

// spanTransport sends a request made under a span other than the one the
// nested client serves under with that span's traceparent, so the API calls
// it makes are that span's children: an entrypoint's module build is one
// span over the calls that build the module. A request under the same span
// sends none, and the server gives it the invocation's.
type spanTransport struct {
	http.RoundTripper
	// serving is the span the nested client serves under.
	serving trace.SpanContext
}

func (t spanTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	span := trace.SpanContextFromContext(req.Context())
	if !span.IsValid() || span.Equal(t.serving) {
		return t.RoundTripper.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	telemetry.Propagator.Inject(req.Context(), propagation.HeaderCarrier(req.Header))
	return t.RoundTripper.RoundTrip(req)
}

func checkServerError(srvErrCh <-chan error) error {
	select {
	case serveErr, ok := <-srvErrCh:
		if ok && serveErr != nil {
			return fmt.Errorf("serve nested client: %w", serveErr)
		}
	default:
	}
	return nil
}

// NewNestedClientMetadata returns fresh metadata for a nested client to
// evaluate Dang code under, in the caller's session.
func NewNestedClientMetadata(ctx context.Context) (*engine.ClientMetadata, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	nestedClientMetadata := &engine.ClientMetadata{
		ClientID:          rand.Text(),
		ClientSecretToken: rand.Text(),
		SessionID:         clientMetadata.SessionID,
		ClientStableID:    rand.Text(),
		ClientVersion:     engine.Version,
		AllowedLLMModules: slices.Clone(clientMetadata.AllowedLLMModules),
	}

	return nestedClientMetadata, nil
}

// ConvertError converts an error from Dang evaluation into a *core.Error,
// preserving GraphQL error extensions as error values.
func ConvertError(rerr error) *core.Error {
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		dagErr := core.NewError(gqlErr.Message)
		if gqlErr.Extensions != nil {
			keys := make([]string, 0, len(gqlErr.Extensions))
			for k := range gqlErr.Extensions {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				val, err := json.Marshal(gqlErr.Extensions[k])
				if err != nil {
					fmt.Println("failed to marshal error value:", err)
				}
				dagErr = dagErr.WithValue(k, core.JSON(val))
			}
		}
		return dagErr
	}
	return core.NewError(rerr.Error())
}
