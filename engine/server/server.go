package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
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
	pipelineLabels []pipeline.Label,
) (*DaggerServer, error) {
	srv := &DaggerServer{
		serverID: serverID,
		bkClient: bkClient,
		worker:   worker,
		doneCh:   make(chan struct{}, 1),
	}

	progrockWriters := progrock.MultiWriter{
		buildkit.ProgrockLogrusWriter{},
	}

	clientConn := caller.Conn()
	progClient := progrock.NewProgressServiceClient(clientConn)
	progUpdates, err := progClient.WriteUpdates(ctx)
	if err != nil {
		return nil, err
	}
	progrockWriters = append(progrockWriters, &progrock.RPCWriter{Conn: clientConn, Updates: progUpdates})

	progSockPath := fmt.Sprintf("/run/dagger/server-progrock-%s.sock", serverID)
	progWriter, progCleanup, err := buildkit.ProgrockForwarder(progSockPath, progrockWriters)
	if err != nil {
		return nil, err
	}
	srv.progCleanup = progCleanup

	pipeline.SetRootLabels(pipelineLabels)
	progrockLabels := []*progrock.Label{}
	for _, label := range pipelineLabels {
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

	apiSchema, err := schema.New(ctx, schema.InitializeArgs{
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

	conn = &withDeadlineConn{conn: nopCloserConn{conn}}
	l := &singleConnListener{conn: conn, closeCh: make(chan struct{})}
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	// NOTE: not sure how inefficient making a new server per-request is, fix if it's meaningful.
	// Maybe we could dynamically mux in more endpoints for each client or something
	handler, handlerDone := srv.HTTPHandlerForClient(clientMetadata, conn, bklog.G(ctx))
	defer func() {
		select {
		case <-handlerDone:
			// TODO:
			bklog.G(ctx).Trace("handler done")
			// case <-ctx.Done():
			// TODO:
			// bklog.G(ctx).Trace("context done instead of handler")
		}
	}()
	httpSrv := http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	defer httpSrv.Close()
	return httpSrv.Serve(l)
}

func (srv *DaggerServer) HTTPHandlerForClient(clientMetadata *engine.ClientMetadata, conn net.Conn, lg *logrus.Entry) (http.Handler, <-chan struct{}) {
	doneCh := make(chan struct{})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer close(doneCh)
		req = req.WithContext(bklog.WithLogger(req.Context(), lg))
		bklog.G(req.Context()).Tracef("http handler for client conn")
		defer bklog.G(req.Context()).Tracef("http handler for client conn done: %s", clientMetadata.ClientID)

		req = req.WithContext(progrock.RecorderToContext(req.Context(), srv.recorder))
		req = req.WithContext(engine.ContextWithClientMetadata(req.Context(), clientMetadata))

		// TODO:
		bklog.G(req.Context()).Debugf("http handler for path %s", req.URL.Path)

		// TODO:
		// req = req.WithContext(context.WithValue(req.Context(), "dumbhack", conn))
		// w = &overrideHijacker{ResponseWriter: w, conn: conn}
		srv.schema.ServeHTTP(w, req)
	}), doneCh
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

// TODO: could also implement this upstream on:
// https://github.com/sipsma/buildkit/blob/fa11bf9e57a68e3b5252386fdf44042dd672949a/session/grpchijack/dial.go#L45-L45
type withDeadlineConn struct {
	conn          net.Conn
	readDeadline  time.Time
	writeDeadline time.Time
}

func (c *withDeadlineConn) Read(b []byte) (n int, err error) {
	// If a deadline is set, create a channel to signal a timeout
	if !c.readDeadline.IsZero() {
		// if it's in the past, error immediately
		if time.Now().After(c.readDeadline) {
			return 0, os.ErrDeadlineExceeded
		}

		timeout := make(chan bool, 1)
		go func() {
			time.Sleep(time.Until(c.readDeadline))
			timeout <- true
		}()

		// Start a goroutine for the actual Read operation
		read := make(chan int, 1)
		go func() {
			n, err = c.conn.Read(b)
			read <- 0
		}()

		// Wait for either Read to complete or the timeout
		select {
		case <-read:
			return n, err
		case <-timeout:
			return 0, os.ErrDeadlineExceeded
		}
	}

	// If no deadline is set, just call the Read method
	return c.conn.Read(b)
}

func (c *withDeadlineConn) Write(b []byte) (n int, err error) {
	// If a deadline is set, create a channel to signal a timeout
	if !c.writeDeadline.IsZero() {
		// if it's in the past, error immediately
		if time.Now().After(c.readDeadline) {
			return 0, os.ErrDeadlineExceeded
		}

		timeout := make(chan bool, 1)
		go func() {
			time.Sleep(time.Until(c.writeDeadline))
			timeout <- true
		}()

		// Start a goroutine for the actual Write operation
		write := make(chan int, 1)
		go func() {
			n, err = c.conn.Write(b)
			write <- 0
		}()

		// Wait for either Write to complete or the timeout
		select {
		case <-write:
			return n, err
		case <-timeout:
			return 0, os.ErrDeadlineExceeded
		}
	}

	// If no deadline is set, just call the Write method
	return c.conn.Write(b)
}

func (c *withDeadlineConn) Close() error {
	return c.conn.Close()
}

func (c *withDeadlineConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *withDeadlineConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *withDeadlineConn) SetDeadline(t time.Time) error {
	c.readDeadline = t
	c.writeDeadline = t
	return nil
}

func (c *withDeadlineConn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

func (c *withDeadlineConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}
