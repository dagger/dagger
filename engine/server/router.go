package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/internal/handler"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/gqlerrors"
	bkclient "github.com/moby/buildkit/client"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/vito/progrock"
)

// TODO: now I don't like the name router again...
type Router struct {
	bkClient *buildkit.Client
	worker   bkworker.Worker

	schema      *schema.MergedSchemas
	recorder    *progrock.Recorder
	progCleanup func() error

	doneCh    chan struct{}
	doneErr   error // TODO: actually set this
	closeOnce sync.Once
}

func NewRouter(
	ctx context.Context,
	bkClient *buildkit.Client,
	worker bkworker.Worker,
	caller bksession.Caller,
	routerID string,
	secretStore *core.SecretStore,
) (*Router, error) {
	rtr := &Router{
		bkClient: bkClient,
		worker:   worker,
		doneCh:   make(chan struct{}),
	}

	clientConn := caller.Conn()
	progClient := progrock.NewProgressServiceClient(clientConn)
	progUpdates, err := progClient.WriteUpdates(ctx)
	if err != nil {
		return nil, err
	}

	progSockPath := fmt.Sprintf("/run/dagger/router-progrock-%s.sock", routerID)
	progWriter, progCleanup, err := progrockForwarder(progSockPath, progrock.MultiWriter{
		&progrock.RPCWriter{Conn: clientConn, Updates: progUpdates},
		progrockLogrusWriter{},
	})
	if err != nil {
		return nil, err
	}
	rtr.progCleanup = progCleanup

	// TODO: correct progrock labels
	go pipeline.LoadRootLabels("/", "da-engine")
	// rtr.recorder = progrock.NewRecorder(progrockLogrusWriter{}, progrock.WithLabels(labels...))
	rtr.recorder = progrock.NewRecorder(progWriter)
	ctx = progrock.RecorderToContext(ctx, rtr.recorder)

	// TODO: ensure clean flush+shutdown+error-handling
	statusCh := make(chan *bkclient.SolveStatus, 8)
	go func() {
		err := bkClient.WriteStatusesTo(ctx, statusCh)
		if err != nil {
			bklog.G(ctx).WithError(err).Error("failed to write status updates")
		}
	}()
	go func() {
		defer func() {
			// drain channel on error
			for range statusCh {
			}
		}()
		for {
			status, ok := <-statusCh
			if !ok {
				return
			}
			err := rtr.recorder.Record(bk2progrock(status))
			if err != nil {
				bklog.G(ctx).WithError(err).Error("failed to record status update")
				return
			}
		}
	}()

	apiSchema, err := schema.New(schema.InitializeArgs{
		BuildkitClient: rtr.bkClient,
		Platform:       rtr.worker.Platforms(true)[0],
		ProgSockPath:   progSockPath,
		OCIStore:       rtr.worker.ContentStore(),
		LeaseManager:   rtr.worker.LeaseManager(),
		Secrets:        secretStore,
		/* TODO:
		Auth     *auth.RegistryAuthProvider
		*/
	})
	if err != nil {
		return nil, err
	}
	rtr.schema = apiSchema
	return rtr, nil
}

func (rtr *Router) Close() {
	defer rtr.closeOnce.Do(func() {
		close(rtr.doneCh)
	})

	// mark all groups completed
	rtr.recorder.Complete()
	// close the recorder so the UI exits
	rtr.recorder.Close()

	rtr.progCleanup()
}

func (rtr *Router) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-rtr.doneCh:
		return fmt.Errorf("router closed: %w", rtr.doneErr)
	}
}

func (rtr *Router) ServeClientConn(
	ctx context.Context,
	clientMetadata *engine.ClientMetadata,
	conn net.Conn,
) error {
	// TODO:
	bklog.G(ctx).Debugf("serve client conn: %s", clientMetadata.ClientID)

	l := &singleConnListener{conn: conn}
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	// TODO: not sure how inefficient making a new server per-request is, fix if it's meaningful
	// maybe you could dynamically mux in more endpoints for each client or something?
	srv := http.Server{
		Handler:           rtr.HTTPHandlerForClient(clientMetadata),
		ReadHeaderTimeout: 30 * time.Second,
	}
	err := srv.Serve(l)
	// if error is "use of closed network connection", it's from the context being canceled
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
		srv.Close()
		return err
	}
	return srv.Shutdown(ctx)
}

func (rtr *Router) HTTPHandlerForClient(clientMetadata *engine.ClientMetadata) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// TODO:
		bklog.G(req.Context()).Debugf("http handler for client conn: %s", clientMetadata.ClientID)
		defer bklog.G(req.Context()).Debugf("http handler for client conn done: %s", clientMetadata.ClientID)

		w.Header().Add("x-dagger-engine", engine.Version)

		/* TODO: re-add session token, here or in an upper caller
		if rtr.sessionToken != "" {
			username, _, ok := req.BasicAuth()
			if !ok || username != rtr.sessionToken {
				w.Header().Set("WWW-Authenticate", `Basic realm="Access to the Dagger engine session"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		*/

		defer func() {
			if v := recover(); v != nil {
				msg := "Internal Server Error"
				code := http.StatusInternalServerError
				switch v := v.(type) {
				case error:
					msg = v.Error()
					if errors.As(v, &schema.InvalidInputError{}) {
						// panics can happen on invalid input in scalar serde
						code = http.StatusBadRequest
					}
				case string:
					msg = v
				}
				res := graphql.Result{
					Errors: []gqlerrors.FormattedError{
						gqlerrors.NewFormattedError(msg),
					},
				}
				bytes, err := json.Marshal(res)
				if err != nil {
					panic(err)
				}
				http.Error(w, string(bytes), code)
			}
		}()

		req = req.WithContext(progrock.RecorderToContext(req.Context(), rtr.recorder))
		req = req.WithContext(engine.ContextWithClientMetadata(req.Context(), clientMetadata))

		mux := http.NewServeMux()
		mux.Handle("/query", handler.New(&handler.Config{
			Schema: rtr.schema.Schema(),
		}))
		mux.ServeHTTP(w, req)
	})
}

// converts a pre-existing net.Conn into a net.Listener that returns the conn and then blocks
type singleConnListener struct {
	conn net.Conn
	l    sync.Mutex
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.l.Lock()
	defer l.l.Unlock()

	if l.conn == nil {
		return nil, io.ErrClosedPipe
	}
	c := l.conn
	l.conn = nil
	return c, nil
}

func (l *singleConnListener) Addr() net.Addr {
	return nil
}

func (l *singleConnListener) Close() error {
	return nil
}
