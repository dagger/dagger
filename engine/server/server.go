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

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/gqlerrors"
	bkclient "github.com/moby/buildkit/client"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/sirupsen/logrus"
	"github.com/vito/progrock"
)

type DaggerServer struct {
	serverID string
	bkClient *buildkit.Client
	worker   bkworker.Worker

	schema      *schema.MergedSchemas
	recorder    *progrock.Recorder
	progCleanup func() error

	doneCh    chan struct{}
	closeOnce sync.Once

	connectedClients int
	clientMu         sync.RWMutex
}

func NewDaggerServer(
	ctx context.Context,
	bkClient *buildkit.Client,
	worker bkworker.Worker,
	caller bksession.Caller,
	serverID string,
	secretStore *core.SecretStore,
	authProvider *auth.RegistryAuthProvider,
	rootLabels []pipeline.Label,
) (*DaggerServer, error) {
	srv := &DaggerServer{
		serverID: serverID,
		bkClient: bkClient,
		worker:   worker,
		doneCh:   make(chan struct{}, 1),
	}

	clientConn := caller.Conn()
	progClient := progrock.NewProgressServiceClient(clientConn)
	progUpdates, err := progClient.WriteUpdates(ctx)
	if err != nil {
		return nil, err
	}

	progSockPath := fmt.Sprintf("/run/dagger/server-progrock-%s.sock", serverID)
	progWriter, progCleanup, err := buildkit.ProgrockForwarder(progSockPath, progrock.MultiWriter{
		&progrock.RPCWriter{Conn: clientConn, Updates: progUpdates},
		buildkit.ProgrockLogrusWriter{},
	})
	if err != nil {
		return nil, err
	}
	srv.progCleanup = progCleanup

	progrockLabels := []*progrock.Label{}
	for _, label := range rootLabels {
		progrockLabels = append(progrockLabels, &progrock.Label{
			Name:  label.Name,
			Value: label.Value,
		})
	}
	srv.recorder = progrock.NewRecorder(progWriter, progrock.WithLabels(progrockLabels...))

	statusCh := make(chan *bkclient.SolveStatus, 8)
	go func() {
		// NOTE: context.Background is used because if the provided context is canceled, buildkit can
		// leave internal progress contexts open and leak goroutines.
		err := bkClient.WriteStatusesTo(context.Background(), statusCh)
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
			err := srv.recorder.Record(buildkit.BK2Progrock(status))
			if err != nil {
				bklog.G(ctx).WithError(err).Error("failed to record status update")
				return
			}
		}
	}()

	apiSchema, err := schema.New(schema.InitializeArgs{
		BuildkitClient: srv.bkClient,
		Platform:       srv.worker.Platforms(true)[0],
		ProgSockPath:   progSockPath,
		OCIStore:       srv.worker.ContentStore(),
		LeaseManager:   srv.worker.LeaseManager(),
		Secrets:        secretStore,
		Auth:           authProvider,
	})
	if err != nil {
		return nil, err
	}
	srv.schema = apiSchema
	return srv, nil
}

func (srv *DaggerServer) LogMetrics(l *logrus.Entry) *logrus.Entry {
	srv.clientMu.RLock()
	defer srv.clientMu.RUnlock()
	return l.WithField(fmt.Sprintf("server-%s-client-count", srv.serverID), srv.connectedClients)
}

func (srv *DaggerServer) Close() {
	defer srv.closeOnce.Do(func() {
		close(srv.doneCh)
	})

	// mark all groups completed
	srv.recorder.Complete()
	// close the recorder so the UI exits
	srv.recorder.Close()

	srv.progCleanup()
}

func (srv *DaggerServer) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-srv.doneCh:
		return nil
	}
}

func (srv *DaggerServer) ServeClientConn(
	ctx context.Context,
	clientMetadata *engine.ClientMetadata,
	conn net.Conn,
) error {
	bklog.G(ctx).Trace("serve client conn")
	defer bklog.G(ctx).Trace("done serving client conn")
	srv.clientMu.Lock()
	srv.connectedClients++
	defer func() {
		srv.clientMu.Lock()
		srv.connectedClients--
		srv.clientMu.Unlock()
	}()
	srv.clientMu.Unlock()

	l := &singleConnListener{conn: nopCloserConn{conn}, closeCh: make(chan struct{})}
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	// NOTE: not sure how inefficient making a new server per-request is, fix if it's meaningful.
	// Maybe we could dynamically mux in more endpoints for each client or something
	httpSrv := http.Server{
		Handler:           srv.HTTPHandlerForClient(clientMetadata),
		ReadHeaderTimeout: 30 * time.Second,
	}
	defer httpSrv.Close()
	return httpSrv.Serve(l)
}

func (srv *DaggerServer) HTTPHandlerForClient(clientMetadata *engine.ClientMetadata) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		bklog.G(req.Context()).Tracef("http handler for client conn")
		defer bklog.G(req.Context()).Tracef("http handler for client conn done: %s", clientMetadata.ClientID)

		w.Header().Add(engine.EngineVersionMetaKey, engine.Version)

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

		req = req.WithContext(progrock.ToContext(req.Context(), srv.recorder))
		req = req.WithContext(engine.ContextWithClientMetadata(req.Context(), clientMetadata))

		mux := http.NewServeMux()
		mux.Handle("/query", NewHandler(&HandlerConfig{
			Schema: srv.schema.Schema(),
		}))
		mux.ServeHTTP(w, req)
	})
}

// converts a pre-existing net.Conn into a net.Listener that returns the conn and then blocks
type singleConnListener struct {
	conn      net.Conn
	l         sync.Mutex
	closeCh   chan struct{}
	closeOnce sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.l.Lock()
	if l.conn == nil {
		l.l.Unlock()
		<-l.closeCh
		return nil, io.ErrClosedPipe
	}
	defer l.l.Unlock()

	c := l.conn
	l.conn = nil
	return c, nil
}

func (l *singleConnListener) Addr() net.Addr {
	return nil
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closeCh)
	})
	return nil
}

type nopCloserConn struct {
	net.Conn
}

func (nopCloserConn) Close() error {
	return nil
}
