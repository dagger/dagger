// Package dangshared holds the Dang-version-agnostic plumbing shared by every
// supported major version of the Dang runtime (core/sdk/dang/v1, v2, ...).
//
// Nothing in this package may import github.com/vito/dang (any major); that
// keeps the per-version packages free to be pure copies of each other with
// only their dang import paths rewritten. See core/sdk/dang/README.md.
package dangshared

import (
	"context"
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
	"github.com/dagger/dagger/internal/buildkit/identity"
	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/propagation"
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

	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			telemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
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

	gqlClient := graphql.NewClient(fmt.Sprintf("http://%s/query", l.Addr()), &http.Client{Transport: httpTransport})

	out, err := fn(ctx, gqlClient)
	if err != nil {
		return nil, err
	}

	if err := checkServerError(srvErrCh); err != nil {
		return nil, err
	}

	return out, nil
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
		ClientID:          identity.NewID(),
		ClientSecretToken: identity.NewID(),
		SessionID:         clientMetadata.SessionID,
		ClientStableID:    identity.NewID(),
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
