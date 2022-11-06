package dagger

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"dagger.io/dagger/filesend"
	"dagger.io/dagger/internal/engineconn"
	"github.com/dagger/dagger/engine/filesync"
	"github.com/docker/docker/api/types"
	"github.com/oklog/ulid/v2"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const (
	headerSessionID        = "X-Docker-Expose-Session-Uuid"
	headerSessionName      = "X-Docker-Expose-Session-Name"
	headerSessionSharedKey = "X-Docker-Expose-Session-Sharedkey"
	headerSessionMethod    = "X-Docker-Expose-Session-Grpc-Method"
)

type Session struct {
	ID string

	conn net.Conn
	done chan struct{}
}

func openSession(ctx context.Context, dialer engineconn.Dialer) (*Session, error) {
	srv := grpc.NewServer()

	filesync.NewFSSyncProvider(AnyDirSource{}).Register(srv)

	filesend.NewReceiver().Register(srv)

	// TODO(vito): blocked on https://github.com/mwitkow/grpc-proxy/pull/62
	// authprovider.NewDockerAuthProvider(os.Stderr).Register(srv)

	// TODO(vito): configurable secret store
	// secretsprovider.NewSecretProvider(secretStore).Register(srv)

	// TODO(vito): move engine secret store that resolves SecretID by calling
	// Plaintext()?
	//
	// or just redo secrets?

	grpc_health_v1.RegisterHealthServer(srv, health.NewServer())

	req, err := http.NewRequest(http.MethodPost, "http://dagger/session", nil)
	if err != nil {
		return nil, err
	}

	sid := ulid.Make().String()

	req.Header.Set(headerSessionID, sid)
	req.Header.Set(headerSessionName, "dagger")
	req.Header.Set(headerSessionSharedKey, "")

	for name, svc := range srv.GetServiceInfo() {
		for _, method := range svc.Methods {
			req.Header.Add(headerSessionMethod, svcMethodURL(name, method.Name))
		}
	}

	conn, err := hijackConn(ctx, req, "h2c", dialer)
	if err != nil {
		return nil, err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		(&http2.Server{}).ServeConn(conn, &http2.ServeConnOpts{Handler: srv})
	}()

	return &Session{
		ID:   sid,
		conn: conn,
		done: done,
	}, nil
}

func (session *Session) Close() error {
	return session.conn.Close()
}

func hijackConn(ctx context.Context, req *http.Request, proto string, dialer engineconn.Dialer) (net.Conn, error) {
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", proto)

	conn, err := dialer(ctx)
	if err != nil {
		return nil, err
	}

	// When we set up a TCP connection for hijack, there could be long periods
	// of inactivity (a long running command with no output) that in certain
	// network setups may cause ECONNTIMEOUT, leaving the client in an unknown
	// state. Setting TCP KeepAlive on the socket connection will prohibit
	// ECONNTIMEOUT unless the socket connection truly is broken
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	clientconn := httputil.NewClientConn(conn, nil)
	defer clientconn.Close()

	// Server hijacks the connection, error 'connection closed' expected
	resp, err := clientconn.Do(req)

	//nolint:staticcheck // ignore SA1019 for connecting to old (pre go1.8) daemons
	if err != httputil.ErrPersistEOF {
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusSwitchingProtocols {
			resp.Body.Close()
			return nil, fmt.Errorf("unable to upgrade to %s, received %d", proto, resp.StatusCode)
		}
	}

	c, br := clientconn.Hijack()
	if br.Buffered() > 0 {
		// If there is buffered content, wrap the connection.  We return an
		// object that implements CloseWrite if the underlying connection
		// implements it.
		if _, ok := c.(types.CloseWriter); ok {
			c = &hijackedConnCloseWriter{&hijackedConn{c, br}}
		} else {
			c = &hijackedConn{c, br}
		}
	} else {
		br.Reset(nil)
	}

	return c, nil
}

type singleConnListener struct {
	conn net.Conn
}

func (l singleConnListener) Accept() (net.Conn, error) {
	return l.conn, nil
}

func (l singleConnListener) Close() error {
	// probably makes more sense to just close wherever the conn is connected
	return nil
}

func (l singleConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

// hijackedConn wraps a net.Conn and is returned by setupHijackConn in the case
// that a) there was already buffered data in the http layer when Hijack() was
// called, and b) the underlying net.Conn does *not* implement CloseWrite().
// hijackedConn does not implement CloseWrite() either.
type hijackedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *hijackedConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

// hijackedConnCloseWriter is a hijackedConn which additionally implements
// CloseWrite().  It is returned by setupHijackConn in the case that a) there
// was already buffered data in the http layer when Hijack() was called, and b)
// the underlying net.Conn *does* implement CloseWrite().
type hijackedConnCloseWriter struct {
	*hijackedConn
}

var _ types.CloseWriter = &hijackedConnCloseWriter{}

func (c *hijackedConnCloseWriter) CloseWrite() error {
	conn := c.Conn.(types.CloseWriter)
	return conn.CloseWrite()
}

type AnyDirSource struct{}

func (AnyDirSource) LookupDir(name string) (filesync.SyncedDir, bool) {
	return filesync.SyncedDir{
		Dir: name,
		Map: func(p string, st *fstypes.Stat) bool {
			st.Uid = 0
			st.Gid = 0
			return true
		},
	}, true
}

// svcMethodURL returns a gRPC method URL for service and method name
func svcMethodURL(s, m string) string {
	return "/" + s + "/" + m
}
