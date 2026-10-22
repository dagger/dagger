package server

import (
	"bufio"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// pipeListener serves an http.Server over in-memory connections.
type pipeListener struct {
	accept chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.accept:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (*pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// closeNotingConn reports when the engine closes its end of a connection.
type closeNotingConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *closeNotingConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// A nested exec's session helper starts alongside the exec's command (/.init),
// so the exec can end, closing the helper's connection, while the engine is in
// the middle of the attachables handshake. The handshake then fails: it must
// end the request without a panic, close the hijacked connection, and leave no
// attachables registered.
func TestSessionAttachablesHandshakeWithstandsPeerClosing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// engineClosesFirst closes the engine's end before the handler runs,
		// as exec cleanup's server Close does.
		engineClosesFirst bool
		// peer acts as the helper once its request is sent, then closes its end.
		peer func(t *testing.T, conn net.Conn)
	}{
		{
			// The engine fails clearing the deadline.
			name:              "closed by the engine",
			engineClosesFirst: true,
			peer:              func(*testing.T, net.Conn) {},
		},
		{
			// The engine fails clearing the deadline or writing the response,
			// depending on which comes first.
			name: "before the response",
			peer: func(*testing.T, net.Conn) {},
		},
		{
			// The response's status line and headers are separate writes.
			name: "during the response",
			peer: func(t *testing.T, conn net.Conn) {
				_, err := conn.Read(make([]byte, 1))
				require.NoError(t, err)
			},
		},
		{
			name: "before the ack",
			peer: func(t *testing.T, conn net.Conn) {
				resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
				require.NoError(t, err)
				require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
				require.NoError(t, resp.Body.Close())
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sess := &daggerSession{sessionID: "session", attachables: newSessionAttachableManager()}
			record := &clientRecord{clientID: "nested", daggerSession: sess}
			handler := httpHandlerFunc((&Server{}).serveSessionAttachables, record)
			engineConn, helperConn := net.Pipe()
			engineEnd := &closeNotingConn{Conn: engineConn, closed: make(chan struct{})}
			panicked := make(chan any, 1)
			httpSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() { panicked <- recover() }()
				if tc.engineClosesFirst {
					_ = engineEnd.Close()
				}
				handler(w, r)
			})}
			listener := &pipeListener{accept: make(chan net.Conn), closed: make(chan struct{})}
			go func() { _ = httpSrv.Serve(listener) }()
			t.Cleanup(func() { _ = httpSrv.Close() })

			listener.accept <- engineEnd
			// As the helper sends it (engine/client: ConnectSessionAttachables).
			req, err := http.NewRequest(http.MethodGet, "http://dagger"+engine.SessionAttachablesEndpoint, nil)
			require.NoError(t, err)
			require.NoError(t, req.Write(helperConn))
			tc.peer(t, helperConn)
			require.NoError(t, helperConn.Close())

			select {
			case p := <-panicked:
				require.Nil(t, p, "the handshake panicked")
			case <-time.After(10 * time.Second):
				t.Fatal("the handshake did not end")
			}
			select {
			case <-engineEnd.closed:
			case <-time.After(10 * time.Second):
				t.Fatal("the engine left the hijacked connection open")
			}
			_, ok := sess.attachables.Lookup(record.clientID)
			require.False(t, ok)
		})
	}
}
