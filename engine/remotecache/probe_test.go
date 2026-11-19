package remotecache

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// A connection that is slow but still delivering stays up, however late the
// answers to its probes: the engine judges it by what arrives. Here the
// service sends a message that takes many probe timeouts to cross a slow
// link, then a request, and answers each probe behind them, in order, as the
// service does. Engines have given up on such connections when a ping's pong
// waited behind a merge still arriving.
func TestSlowConnectionStaysUp(t *testing.T) {
	t.Parallel()
	svc := newProbedService(t)
	link := newSlowLink(t, svc.addr, 128<<10)
	served := probedSession(t, link.addr)
	svc.serve(t)

	// Compression can't shrink it much: about 2 s through the link, several
	// probe timeouts.
	random := make([]byte, 128<<10)
	_, err := rand.Read(random)
	require.NoError(t, err)
	filler, err := protocol.EncodeEnvelope(svc.next.Add(1), 1<<40, protocol.TypeMerged, map[string]string{"pad": base64.StdEncoding.EncodeToString(random)})
	require.NoError(t, err)
	id := svc.next.Add(1)
	request, err := protocol.EncodeEnvelope(id, 0, protocol.TypeRestore, protocol.Restore{})
	require.NoError(t, err)
	answered := svc.expect(id)
	svc.writeInOrder(t, filler, request)

	select {
	case reply := <-answered:
		require.Equal(t, protocol.TypeRestored, reply.Type, "the request behind the slow message is answered on the same connection")
	case err := <-served:
		t.Fatalf("the session ended while bytes were still arriving: %v", err)
	case <-time.After(time.Minute):
		t.Fatal("the request behind the slow message was never answered")
	}
	require.GreaterOrEqual(t, svc.probed(), 3, "the engine probed while the message was on its way, and each answer waited behind it")
	select {
	case err := <-served:
		t.Fatalf("the session ended: %v", err)
	default:
	}
}

// A connection on which nothing arrives within a probe's timeout is dead,
// whether or not the engine can still write to it: the session ends, and
// says why.
func TestSilentConnectionEnds(t *testing.T) {
	t.Parallel()
	svc := newProbedService(t)
	link := newSlowLink(t, svc.addr, 0)
	served := probedSession(t, link.addr)
	svc.serve(t)
	require.Eventually(t, func() bool { return svc.probed() > 0 }, time.Minute, 10*time.Millisecond, "the engine probes once welcomed")
	link.hold()

	select {
	case err := <-served:
		require.ErrorContains(t, err, "probe: nothing arrived for 300ms")
	case <-time.After(time.Minute):
		t.Fatal("the session stayed up on a connection on which nothing arrived")
	}
}

// probedSession dials addr as an engine does, and serves a session on the
// connection with a short probe interval and timeout. It returns what the
// session ends with.
func probedSession(t *testing.T, addr string) <-chan error {
	t.Helper()
	cfg := newSettings(Config{URL: "http://" + addr, Token: "token"})
	cfg.probeInterval, cfg.probeTimeout = 200*time.Millisecond, 300*time.Millisecond
	conn, err := cfg.dialService(t.Context())
	require.NoError(t, err)
	s := newSession(t.Context(), conn, newFakeAdapter(), cfg)
	t.Cleanup(s.close)
	served := make(chan error, 1)
	go func() {
		_, err := s.serve()
		served <- err
	}()
	return served
}

// probedService is a service's side of one engine connection, on the
// WebSocket library as the service uses it. It welcomes the engine, answers
// each probe as the service does, and hands replies to the requests that
// wait for them.
type probedService struct {
	addr  string
	conns chan *websocket.Conn
	ws    *websocket.Conn
	next  atomic.Uint64

	mu      sync.Mutex
	probes  int
	waiting map[uint64]chan protocol.Envelope
}

func newProbedService(t *testing.T) *probedService {
	t.Helper()
	svc := &probedService{conns: make(chan *websocket.Conn, 1), waiting: map[uint64]chan protocol.Envelope{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionNoContextTakeover})
		if err != nil {
			return
		}
		ws.SetReadLimit(protocol.MaxMessageBytes)
		svc.conns <- ws
	}))
	t.Cleanup(server.Close)
	svc.addr = strings.TrimPrefix(server.URL, "http://")
	return svc
}

// serve takes the engine's connection, welcomes its hello, and reads it
// until it ends.
func (svc *probedService) serve(t *testing.T) {
	t.Helper()
	select {
	case svc.ws = <-svc.conns:
	case <-time.After(time.Minute):
		t.Fatal("the engine never connected")
	}
	t.Cleanup(func() { _ = svc.ws.CloseNow() })
	_, data, err := svc.ws.Read(t.Context())
	require.NoError(t, err)
	var hello protocol.Envelope
	require.NoError(t, json.Unmarshal(data, &hello))
	require.Equal(t, protocol.TypeHello, hello.Type)
	welcome, err := protocol.EncodeEnvelope(svc.next.Add(1), hello.ID, protocol.TypeWelcome, protocol.Welcome{})
	require.NoError(t, err)
	require.NoError(t, svc.ws.Write(t.Context(), websocket.MessageText, welcome))
	go func() {
		for {
			_, data, err := svc.ws.Read(t.Context())
			if err != nil {
				return
			}
			var env protocol.Envelope
			if json.Unmarshal(data, &env) != nil {
				continue
			}
			svc.mu.Lock()
			waiter := svc.waiting[env.Re]
			delete(svc.waiting, env.Re)
			if env.Type == protocol.TypeProbe {
				svc.probes++
			}
			svc.mu.Unlock()
			switch {
			case env.Type == protocol.TypeProbe:
				// On the connection itself, in order behind what is being
				// sent, with no deadline of its own.
				go func() {
					if probed, err := protocol.EncodeEnvelope(svc.next.Add(1), env.ID, protocol.TypeProbed, protocol.Probed{}); err == nil {
						_ = svc.ws.Write(t.Context(), websocket.MessageText, probed)
					}
				}()
			case waiter != nil:
				waiter <- env
			}
		}
	}()
}

func (svc *probedService) expect(id uint64) <-chan protocol.Envelope {
	waiter := make(chan protocol.Envelope, 1)
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.waiting[id] = waiter
	return waiter
}

// writeInOrder writes the messages one after another, in the background.
func (svc *probedService) writeInOrder(t *testing.T, messages ...[]byte) {
	go func() {
		for _, data := range messages {
			if svc.ws.Write(t.Context(), websocket.MessageText, data) != nil {
				return
			}
		}
	}()
}

func (svc *probedService) probed() int {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	return svc.probes
}

// slowLink is a TCP proxy whose way from the service to the engine carries at
// most rate bytes a second (any, at 0), and nothing while it is held. The
// way from the engine to the service is never slowed.
type slowLink struct {
	addr string

	mu    sync.Mutex
	held  bool
	freed *sync.Cond
}

func newSlowLink(t *testing.T, server string, rate int) *slowLink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	link := &slowLink{addr: ln.Addr().String()}
	link.freed = sync.NewCond(&link.mu)
	var pumps sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		link.release()
		pumps.Wait()
	})
	go func() {
		for {
			engine, err := ln.Accept()
			if err != nil {
				return
			}
			service, err := net.Dial("tcp", server)
			if err != nil {
				engine.Close()
				continue
			}
			const buffer = 16 << 10
			_ = service.(*net.TCPConn).SetReadBuffer(buffer)
			closeBoth := func() {
				engine.Close()
				service.Close()
			}
			pumps.Add(2)
			go func() {
				defer pumps.Done()
				_, _ = io.Copy(service, engine)
				closeBoth()
			}()
			go func() {
				defer pumps.Done()
				defer closeBoth()
				buf := make([]byte, buffer)
				for {
					link.wait()
					n, err := service.Read(buf)
					if n > 0 {
						if _, err := engine.Write(buf[:n]); err != nil {
							return
						}
						if rate > 0 {
							time.Sleep(time.Duration(n) * time.Second / time.Duration(rate))
						}
					}
					if err != nil {
						return
					}
				}
			}()
			done := t.Context().Done()
			go func() {
				<-done
				closeBoth()
			}()
		}
	}()
	return link
}

func (l *slowLink) hold() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = true
}

func (l *slowLink) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = false
	l.freed.Broadcast()
}

func (l *slowLink) wait() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.held {
		l.freed.Wait()
	}
}
