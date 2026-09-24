package terminal

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// fakeSessionServer is an in-memory Terminal_SessionServer. Messages queued
// with push are returned by Recv in order; Recv blocks (until ctx is done)
// once the queue is empty, like a live stream. Sends are recorded.
type fakeSessionServer struct {
	grpc.ServerStream

	ctx    context.Context
	recvCh chan recvResult

	mu   sync.Mutex
	sent []*SessionResponse

	// readyBeforeRecv records whether a Ready was sent before the first Recv.
	firstRecv       sync.Once
	readyBeforeRecv atomic.Bool

	// inSend detects overlapping Send calls, which gRPC does not allow.
	inSend     atomic.Int32
	overlapped atomic.Bool
}

type recvResult struct {
	req *SessionRequest
	err error
}

func newFakeSessionServer(ctx context.Context) *fakeSessionServer {
	return &fakeSessionServer{ctx: ctx, recvCh: make(chan recvResult, 64)}
}

func (f *fakeSessionServer) push(req *SessionRequest) { f.recvCh <- recvResult{req: req} }
func (f *fakeSessionServer) pushErr(err error)        { f.recvCh <- recvResult{err: err} }

func (f *fakeSessionServer) Context() context.Context     { return f.ctx }
func (f *fakeSessionServer) SetHeader(metadata.MD) error  { return nil }
func (f *fakeSessionServer) SendHeader(metadata.MD) error { return nil }
func (f *fakeSessionServer) SetTrailer(metadata.MD)       {}

func (f *fakeSessionServer) Send(res *SessionResponse) error {
	if f.inSend.Add(1) > 1 {
		f.overlapped.Store(true)
	}
	defer f.inSend.Add(-1)
	time.Sleep(time.Millisecond) // widen the window for overlap detection
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, res)
	return nil
}

func (f *fakeSessionServer) Recv() (*SessionRequest, error) {
	f.firstRecv.Do(func() {
		f.readyBeforeRecv.Store(f.hasSent(func(r *SessionResponse) bool { return r.GetReady() != nil }))
	})
	select {
	case r := <-f.recvCh:
		return r.req, r.err
	case <-f.ctx.Done():
		return nil, io.EOF
	}
}

func (f *fakeSessionServer) hasSent(match func(*SessionResponse) bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.sent {
		if match(r) {
			return true
		}
	}
	return false
}

func (f *fakeSessionServer) sentResizes() []*Resize {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Resize
	for _, r := range f.sent {
		if rs := r.GetResize(); rs != nil {
			out = append(out, rs)
		}
	}
	return out
}

func stdout(b []byte) *SessionRequest {
	return &SessionRequest{Msg: &SessionRequest_Stdout{Stdout: b}}
}

func exit(code int32) *SessionRequest {
	return &SessionRequest{Msg: &SessionRequest_Exit{Exit: code}}
}

func runSession(t *testing.T, srv *fakeSessionServer, withTerminal WithTerminalFunc) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewTerminalAttachable(context.Background(), withTerminal).Session(srv)
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("terminal session did not finish (deadlock?)")
		return nil
	}
}

func TestSessionReceivesInfoFirst(t *testing.T) {
	t.Parallel()
	srv := newFakeSessionServer(t.Context())
	srv.push(&SessionRequest{Msg: &SessionRequest_Info{Info: &SessionInfo{
		ContainerId:   "ctr-id",
		Workdir:       "/src",
		FromExecError: true,
		TerminalId:    "term-id",
	}}})
	srv.push(stdout([]byte("hello")))
	srv.push(exit(0))

	var gotInfo *SessionInfo
	var out, errOut bytes.Buffer
	err := runSession(t, srv, func(session *SessionHandle) error {
		gotInfo = session.Info
		return session.Run(strings.NewReader(""), &out, &errOut)
	})
	require.NoError(t, err)

	require.NotNil(t, gotInfo)
	require.Equal(t, "ctr-id", gotInfo.ContainerId)
	require.Equal(t, "/src", gotInfo.Workdir)
	require.True(t, gotInfo.FromExecError)
	require.Equal(t, "term-id", gotInfo.TerminalId)
	// the info message must not leak into the terminal output
	require.Equal(t, "hello", out.String())
}

func TestSessionOldEngineWithoutInfoReplaysFirstMessage(t *testing.T) {
	t.Parallel()
	// Engines predating SessionInfo start straight with output; the peeked
	// message must be replayed, not dropped.
	srv := newFakeSessionServer(t.Context())
	srv.push(stdout([]byte("first")))
	srv.push(stdout([]byte(" second")))
	srv.push(exit(0))

	var gotInfo *SessionInfo
	var out, errOut bytes.Buffer
	err := runSession(t, srv, func(session *SessionHandle) error {
		gotInfo = session.Info
		return session.Run(strings.NewReader(""), &out, &errOut)
	})
	require.NoError(t, err)
	require.Nil(t, gotInfo)
	require.Equal(t, "first second", out.String())
}

func TestSessionSendsReadyBeforeWaitingForEngine(t *testing.T) {
	t.Parallel()
	// The engine only sends its first message (SessionInfo) after receiving
	// the client's Ready, so waiting for info before signaling readiness
	// would deadlock.
	srv := newFakeSessionServer(t.Context())
	srv.push(stdout(nil))
	srv.push(exit(0))

	err := runSession(t, srv, func(session *SessionHandle) error {
		return session.Run(strings.NewReader(""), io.Discard, io.Discard)
	})
	require.NoError(t, err)
	require.True(t, srv.readyBeforeRecv.Load(), "Ready must be sent before the first Recv")
}

func TestSessionEndsCleanlyWhenEngineClosesEarly(t *testing.T) {
	t.Parallel()
	srv := newFakeSessionServer(t.Context())
	srv.pushErr(io.EOF)

	called := false
	err := runSession(t, srv, func(session *SessionHandle) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	require.False(t, called, "no terminal should be opened for a session that ended before starting")
}

func TestSessionExitCodeWrittenToStderr(t *testing.T) {
	t.Parallel()
	srv := newFakeSessionServer(t.Context())
	srv.push(exit(3))

	var errOut bytes.Buffer
	err := runSession(t, srv, func(session *SessionHandle) error {
		return session.Run(strings.NewReader(""), io.Discard, &errOut)
	})
	require.NoError(t, err)
	require.Equal(t, "exit 3\n", errOut.String())
}

func TestSessionHandleResize(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := newFakeSessionServer(ctx)
	srv.push(stdout(nil))

	err := runSession(t, srv, func(session *SessionHandle) error {
		require.NoError(t, session.Resize(80, 24))
		require.NoError(t, session.Resize(100, 30))
		srv.push(exit(0))
		return session.Run(strings.NewReader(""), io.Discard, io.Discard)
	})
	require.NoError(t, err)

	resizes := srv.sentResizes()
	require.Len(t, resizes, 2)
	require.Equal(t, int32(80), resizes[0].Width)
	require.Equal(t, int32(24), resizes[0].Height)
	require.Equal(t, int32(100), resizes[1].Width)
	require.Equal(t, int32(30), resizes[1].Height)
}

func TestSessionForwardsStdinAndSerializesSends(t *testing.T) {
	t.Parallel()
	srv := newFakeSessionServer(t.Context())
	srv.push(stdout(nil))

	stdinR, stdinW := io.Pipe()
	err := runSession(t, srv, func(session *SessionHandle) error {
		// Race stdin forwarding against UI-driven resizes: gRPC forbids
		// concurrent Send, so they must be serialized.
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = session.Resize(80+i, 24)
			}()
		}
		go func() {
			for range 20 {
				_, _ = stdinW.Write([]byte("k"))
			}
			wg.Wait()
			// let the forwarded input drain, then end the session
			time.Sleep(50 * time.Millisecond)
			srv.push(exit(0))
		}()
		return session.Run(stdinR, io.Discard, io.Discard)
	})
	require.NoError(t, err)
	require.False(t, srv.overlapped.Load(), "Send must never be called concurrently")

	var stdin strings.Builder
	srv.mu.Lock()
	for _, r := range srv.sent {
		stdin.Write(r.GetStdin())
	}
	srv.mu.Unlock()
	require.Equal(t, strings.Repeat("k", 20), stdin.String())
}
