package server

import (
	"bytes"
	"context"
	"errors"
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

	schema      *schema.APIServer
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

	progWriter, progCleanup, err := buildkit.ProgrockForwarder(bkClient.ProgSockPath, progrock.MultiWriter{
		progrock.NewRPCWriter(clientConn, progUpdates),
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

	// NOTE: context.Background is used because if the provided context is canceled, buildkit can
	// leave internal progress contexts open and leak goroutines.
	bkClient.WriteStatusesTo(context.Background(), srv.recorder)

	apiSchema, err := schema.New(ctx, schema.InitializeArgs{
		BuildkitClient: srv.bkClient,
		Platform:       srv.worker.Platforms(true)[0],
		ProgSockPath:   bkClient.ProgSockPath,
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

	conn = newLogicalDeadlineConn(nopCloserConn{conn})
	l := &singleConnListener{conn: conn, closeCh: make(chan struct{})}
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	// NOTE: not sure how inefficient making a new server per-request is, fix if it's meaningful.
	// Maybe we could dynamically mux in more endpoints for each client or something
	handler, handlerDone, err := srv.HTTPHandlerForClient(clientMetadata, conn, bklog.G(ctx))
	if err != nil {
		return fmt.Errorf("failed to create http handler: %w", err)
	}
	defer func() {
		<-handlerDone
		bklog.G(ctx).Trace("handler done")
	}()
	httpSrv := http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	defer httpSrv.Close()
	return httpSrv.Serve(l)
}

func (srv *DaggerServer) HTTPHandlerForClient(clientMetadata *engine.ClientMetadata, conn net.Conn, lg *logrus.Entry) (http.Handler, <-chan struct{}, error) {
	doneCh := make(chan struct{})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer close(doneCh)
		req = req.WithContext(bklog.WithLogger(req.Context(), lg))
		bklog.G(req.Context()).Debugf("http handler for client conn to path %s", req.URL.Path)
		defer bklog.G(req.Context()).Debugf("http handler for client conn done: %s", clientMetadata.ClientID)

		req = req.WithContext(progrock.ToContext(req.Context(), srv.recorder))
		req = req.WithContext(engine.ContextWithClientMetadata(req.Context(), clientMetadata))

		srv.schema.ServeHTTP(w, req)
	}), doneCh, nil
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
	readers       []func()
	readBuf       *bytes.Buffer
	readEOF       bool
	readCond      *sync.Cond
	writeDeadline time.Time
	writers       []func()
	writersL      sync.Mutex
}

func newLogicalDeadlineConn(inner net.Conn) net.Conn {
	c := &withDeadlineConn{
		conn:     inner,
		readBuf:  new(bytes.Buffer),
		readCond: sync.NewCond(new(sync.Mutex)),
	}

	go func() {
		for {
			buf := make([]byte, 32*1024)
			n, err := inner.Read(buf)
			if err != nil {
				c.readCond.L.Lock()
				c.readEOF = true
				c.readCond.L.Unlock()
				c.readCond.Broadcast()
				return
			}

			c.readCond.L.Lock()
			c.readBuf.Write(buf[0:n])
			c.readCond.Broadcast()
			c.readCond.L.Unlock()
		}
	}()

	return c
}

func (c *withDeadlineConn) Read(b []byte) (n int, err error) {
	c.readCond.L.Lock()

	if c.readEOF {
		c.readCond.L.Unlock()
		return 0, io.EOF
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !c.readDeadline.IsZero() {
		if time.Now().After(c.readDeadline) {
			c.readCond.L.Unlock()
			// return early without calling inner Read
			return 0, os.ErrDeadlineExceeded
		}

		go func() {
			dt := time.Until(c.readDeadline)
			if dt > 0 {
				time.Sleep(dt)
			}

			cancel()
		}()
	}

	// Keep track of the reader so a future SetReadDeadline can interrupt it.
	c.readers = append(c.readers, cancel)

	c.readCond.L.Unlock()

	// Start a goroutine for the actual Read operation
	read := make(chan struct{})
	var rN int
	var rerr error
	go func() {
		defer close(read)

		c.readCond.L.Lock()
		defer c.readCond.L.Unlock()

		for ctx.Err() == nil {
			if c.readEOF {
				rerr = io.EOF
				break
			}

			n, _ := c.readBuf.Read(b) // ignore EOF here
			if n > 0 {
				rN = n
				break
			}

			c.readCond.Wait()
		}
	}()

	// Wait for either Read to complete or the timeout
	select {
	case <-read:
		return rN, rerr
	case <-ctx.Done():
		return 0, os.ErrDeadlineExceeded
	}
}

func (c *withDeadlineConn) Write(b []byte) (n int, err error) {
	c.writersL.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !c.writeDeadline.IsZero() {
		if time.Now().After(c.writeDeadline) {
			c.writersL.Unlock()
			// return early without calling inner Write
			return 0, os.ErrDeadlineExceeded
		}

		go func() {
			dt := time.Until(c.writeDeadline)
			if dt > 0 {
				time.Sleep(dt)
			}

			cancel()
		}()
	}

	// Keep track of the writer so a future SetWriteDeadline can interrupt it.
	c.writers = append(c.writers, cancel)
	c.writersL.Unlock()

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
	case <-ctx.Done():
		return 0, os.ErrDeadlineExceeded
	}
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
	return errors.Join(
		c.SetReadDeadline(t),
		c.SetWriteDeadline(t),
	)
}

func (c *withDeadlineConn) SetReadDeadline(t time.Time) error {
	c.readCond.L.Lock()
	c.readDeadline = t
	readers := c.readers
	c.readCond.L.Unlock()

	if len(readers) > 0 && !t.IsZero() {
		go func() {
			dt := time.Until(c.readDeadline)
			if dt > 0 {
				time.Sleep(dt)
			}

			for _, cancel := range readers {
				cancel()
			}
		}()
	}

	return nil
}

func (c *withDeadlineConn) SetWriteDeadline(t time.Time) error {
	c.writersL.Lock()
	c.writeDeadline = t
	writers := c.writers
	c.writersL.Unlock()

	if len(writers) > 0 && !t.IsZero() {
		go func() {
			dt := time.Until(c.writeDeadline)
			if dt > 0 {
				time.Sleep(dt)
			}

			for _, cancel := range writers {
				cancel()
			}
		}()
	}

	return nil
}
