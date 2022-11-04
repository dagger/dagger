package dagger

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"time"

	"dagger.io/dagger/internal/engineconn"
	"github.com/dagger/dagger/engine/filesync"
	"github.com/docker/docker/api/types"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	fstypes "github.com/tonistiigi/fsutil/types"
	"go.uber.org/atomic"
)

const (
	headerSessionID        = "X-Docker-Expose-Session-Uuid"
	headerSessionName      = "X-Docker-Expose-Session-Name"
	headerSessionSharedKey = "X-Docker-Expose-Session-Sharedkey"
	headerSessionMethod    = "X-Docker-Expose-Session-Grpc-Method"
)

func openSession(ctx context.Context, dialer engineconn.Dialer) (*session.Session, error) {
	sess, err := session.NewSession(ctx, "dagger", "")
	if err != nil {
		return nil, err
	}

	for _, attachable := range []session.Attachable{
		// TODO(vito): configurable secret store
		// secretsprovider.NewSecretProvider(secretStore),
		authprovider.NewDockerAuthProvider(os.Stderr),
		filesync.NewFSSyncProvider(AnyDirSource{}),
		// TODO(vito): move engine secret store that resolves SecretID by calling
		// Plaintext()?
		//
		// or just redo secrets?
	} {
		sess.Allow(attachable)
	}

	done := make(chan error, 1)
	go func() {
		err := sess.Run(context.Background(), func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			req, err := http.NewRequest(http.MethodPost, "http://dagger/session", nil)
			if err != nil {
				return nil, err
			}

			for h, vs := range meta {
				for _, v := range vs {
					req.Header.Add(h, v)
				}
			}

			conn, err := hijackConn(ctx, req, proto, dialer)
			if err != nil {
				return nil, err
			}

			return conn, nil
		})
		done <- err
	}()

	return sess, nil
}

//func openSession(ctx context.Context, dialer engineconn.Dialer) (*Session, error) {
//	grpc.EnableTracing = true
//	grpclog.SetLoggerV2(grpclog.NewLoggerV2(os.Stdout, os.Stdout, os.Stdout))

//	srv := grpc.NewServer()

//	for _, sess := range []session.Attachable{
//		// TODO(vito): configurable secret store
//		// secretsprovider.NewSecretProvider(secretStore),
//		authprovider.NewDockerAuthProvider(os.Stderr),
//		filesync.NewFSSyncProvider(AnyDirSource{}),
//		// TODO(vito): move engine secret store that resolves SecretID by calling
//		// Plaintext()?
//		//
//		// or just redo secrets?
//	} {
//		sess.Register(srv)
//	}

//	grpc_health_v1.RegisterHealthServer(srv, health.NewServer())

//	req, err := http.NewRequest(http.MethodPost, "http://dagger/session", nil)
//	if err != nil {
//		return nil, err
//	}

//	sid := identity.NewID()

//	req.Header.Set(headerSessionID, sid)
//	req.Header.Set(headerSessionName, "dagger")
//	req.Header.Set(headerSessionSharedKey, "")

//	for name, svc := range srv.GetServiceInfo() {
//		for _, method := range svc.Methods {
//			req.Header.Add(headerSessionMethod, session.MethodURL(name, method.Name))
//		}
//	}

//	req.Header.Write(os.Stderr)

//	conn, err := hijackConn(ctx, req, dialer)
//	if err != nil {
//		return nil, err
//	}

//	done := make(chan error, 1)
//	go func() {
//		log.Println("serving?")

//		// (&http2.Server{}).ServeConn(conn, &http2.ServeConnOpts{Handler: srv})
//		err := srv.Serve(&singleConnListener{ctx: ctx, conn: conn})
//		done <- err
//		log.Println("served", err)
//	}()

//	return &Session{
//		ID:   sid,
//		conn: conn,
//		done: done,
//	}, nil
//}

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
	ctx      context.Context
	conn     net.Conn
	accepted atomic.Bool
}

func (l singleConnListener) Accept() (net.Conn, error) {
	if l.accepted.CAS(false, true) {
		return l.conn, nil
	} else {
		<-l.ctx.Done()
		return nil, l.ctx.Err()
	}
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
