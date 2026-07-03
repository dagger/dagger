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
func WithNestedClientServer(
	ctx context.Context,
	query *core.Query,
	nestedClientMetadata *engine.ClientMetadata,
	callerClientID string,
	hostServiceProxyToCaller bool,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
	envContext dagql.ObjectResult[*core.Env],
	fn func(ctx context.Context, gqlClient graphql.Client) ([]byte, error),
) ([]byte, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	defer l.Close()

	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			telemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
			query.ServeHTTPToNestedClient(resp, req, nestedClientMetadata, callerClientID, hostServiceProxyToCaller, moduleContext, fnCall, envContext)
		}),
	}
	defer func() {
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

	gqlClient := graphql.NewClient(fmt.Sprintf("http://%s/query", l.Addr()), nil)

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

// NewNestedClientMetadata returns the caller's client metadata along with
// fresh metadata for a nested client to evaluate Dang code under.
func NewNestedClientMetadata(ctx context.Context) (*engine.ClientMetadata, *engine.ClientMetadata, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	nestedClientMetadata := &engine.ClientMetadata{
		ClientID:          identity.NewID(),
		ClientSecretToken: identity.NewID(),
		SessionID:         clientMetadata.SessionID,
		ClientStableID:    identity.NewID(),
		ClientVersion:     engine.Version,
		AllowedLLMModules: slices.Clone(clientMetadata.AllowedLLMModules),
		LockMode:          clientMetadata.LockMode,
	}

	return clientMetadata, nestedClientMetadata, nil
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
