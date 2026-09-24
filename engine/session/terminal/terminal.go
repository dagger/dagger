package terminal

import (
	context "context"
	"errors"
	fmt "fmt"
	"io"
	"os"
	"sync"

	"github.com/dagger/dagger/util/grpcutil"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// SessionHandle is handed to WithTerminalFunc implementations to drive one
// terminal session. Run wires the given stdio to the engine and blocks until
// the session ends; Resize reports a size chosen by the client UI (e.g. an
// embedded terminal pane) instead of the process TTY (which is handled
// automatically via SIGWINCH when stdout is a TTY).
type SessionHandle struct {
	// Info describes the object the terminal is attached to. It is nil when
	// the engine predates session info messages.
	Info *SessionInfo

	sender *lockedSessionSender
	run    func(stdin io.Reader, stdout, stderr io.Writer) error
}

// Run wires the given stdio to the terminal session and blocks until it ends.
func (h *SessionHandle) Run(stdin io.Reader, stdout, stderr io.Writer) error {
	return h.run(stdin, stdout, stderr)
}

// Resize reports a terminal size (in cells) chosen by the client UI. It is a
// no-op for a handle that is not attached to a session.
func (h *SessionHandle) Resize(width, height int) error {
	if h == nil || h.sender == nil {
		return nil
	}
	return sendResize(h.sender, width, height)
}

// WithTerminalFunc provides the stdio (and optionally a size source) for a
// terminal session via the given SessionHandle.
type WithTerminalFunc func(session *SessionHandle) error

var _ TerminalServer = &TerminalAttachable{}

type TerminalAttachable struct {
	rootCtx context.Context

	withTerminal WithTerminalFunc

	UnimplementedTerminalServer
}

func NewTerminalAttachable(
	rootCtx context.Context,
	withTerminal WithTerminalFunc,
) TerminalAttachable {
	if withTerminal == nil {
		withTerminal = func(session *SessionHandle) error {
			return session.Run(os.Stdin, os.Stdout, os.Stderr)
		}
	}

	return TerminalAttachable{
		rootCtx:      rootCtx,
		withTerminal: withTerminal,
	}
}

func (s TerminalAttachable) Register(srv *grpc.Server) {
	RegisterTerminalServer(srv, s)
}

// lockedSessionSender serializes Send calls: stdin forwarding, SIGWINCH
// resizes, and embedded-UI resizes may run concurrently, and gRPC streams do
// not allow concurrent SendMsg.
type lockedSessionSender struct {
	mu  sync.Mutex
	srv Terminal_SessionServer
}

func (l *lockedSessionSender) Send(res *SessionResponse) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.srv.Send(res)
}

func (s TerminalAttachable) Session(srv Terminal_SessionServer) error {
	sender := &lockedSessionSender{srv: srv}

	// Send ready (and a best-effort initial size) immediately: the engine
	// waits for the first client message before proceeding, and new engines
	// then send a SessionInfo as their first message. Waiting for that info
	// before signaling readiness would deadlock.
	if err := s.sendReady(sender); err != nil {
		return fmt.Errorf("sending ready: %w", err)
	}
	_ = s.sendSize(sender, os.Stdout) // best-effort; embedded UIs send their own

	// Peek the first engine message. New engines send SessionInfo first;
	// older engines start straight with output, in which case the message is
	// replayed into the session loop.
	var info *SessionInfo
	var pending *SessionRequest
	req, err := srv.Recv()
	switch {
	case err == nil:
		if msg, ok := req.GetMsg().(*SessionRequest_Info); ok {
			info = msg.Info
		} else {
			pending = req
		}
	case isSessionEndErr(err):
		return nil
	default:
		return fmt.Errorf("error reading terminal: %w", err)
	}

	return s.withTerminal(&SessionHandle{
		Info:   info,
		sender: sender,
		run: func(stdin io.Reader, stdout, stderr io.Writer) error {
			return s.session(srv, sender, pending, stdin, stdout, stderr)
		},
	})
}

func (s TerminalAttachable) session(
	srv Terminal_SessionServer,
	sender *lockedSessionSender,
	pending *SessionRequest,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("terminal session finished"))

	// Re-send the size against the session's real stdout in case it differs
	// from the process stdout probed at readiness time.
	_ = s.sendSize(sender, stdout)
	go s.listenForResize(ctx, sender, stdout)
	go s.forwardStdin(ctx, sender, stdin)

	handle := func(req *SessionRequest) (done bool, _ error) {
		switch msg := req.GetMsg().(type) {
		case *SessionRequest_Stdout:
			_, err := stdout.Write(msg.Stdout)
			if err != nil {
				return true, fmt.Errorf("terminal write stdout: %w", err)
			}
		case *SessionRequest_Stderr:
			_, err := stderr.Write(msg.Stderr)
			if err != nil {
				return true, fmt.Errorf("terminal write stderr: %w", err)
			}
		case *SessionRequest_Exit:
			fmt.Fprintf(stderr, "exit %d\n", msg.Exit)
			return true, nil
		}
		return false, nil
	}

	if pending != nil {
		if done, err := handle(pending); done {
			return err
		}
	}

	for {
		req, err := srv.Recv()
		if err != nil {
			if isSessionEndErr(err) {
				return nil
			}
			return fmt.Errorf("error reading terminal: %w", err)
		}
		if done, err := handle(req); done {
			return err
		}
	}
}

func isSessionEndErr(err error) bool {
	switch {
	case errors.Is(err, context.Canceled), status.Code(err) == codes.Canceled:
		// canceled
		return true
	case errors.Is(err, io.EOF):
		// stopped
		return true
	case status.Code(err) == codes.Unavailable:
		// client disconnected (i.e. quitting Dagger out)
		return true
	}
	return false
}

// sendResize reports a terminal size to the engine.
func sendResize(sender *lockedSessionSender, w, h int) error {
	return sender.Send(&SessionResponse{
		Msg: &SessionResponse_Resize{
			Resize: &Resize{
				Width:  int32(w),
				Height: int32(h),
			},
		},
	})
}

func (s TerminalAttachable) sendSize(sender *lockedSessionSender, stdout io.Writer) error {
	f, ok := stdout.(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return errors.New("stdout is not a terminal; cannot get terminal size")
	}

	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return fmt.Errorf("get terminal size: %w", err)
	}

	return sendResize(sender, w, h)
}

func (s TerminalAttachable) sendReady(sender *lockedSessionSender) error {
	return sender.Send(&SessionResponse{
		Msg: &SessionResponse_Ready{
			Ready: &Ready{},
		},
	})
}

func (s TerminalAttachable) forwardStdin(ctx context.Context, sender *lockedSessionSender, stdin io.Reader) {
	if stdin == nil {
		return
	}

	// In order to stop reading from stdin when the context is cancelled,
	// we proxy the reads through a Pipe which we can close without closing
	// the underlying stdin.
	pipeR, pipeW := io.Pipe()
	close := func() {
		pipeR.Close()
		pipeW.Close()
	}
	defer close()
	go io.Copy(pipeW, stdin)
	go func() {
		<-ctx.Done()
		close()
	}()

	b := make([]byte, 512)
	for {
		n, err := pipeR.Read(b)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				return
			}
			fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
			return
		}

		err = sender.Send(&SessionResponse{
			Msg: &SessionResponse_Stdin{
				Stdin: b[:n],
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "forward stdin: %v\n", err)
			return
		}
	}
}

type TerminalProxy struct {
	client TerminalClient
}

func NewTerminalProxy(client TerminalClient) TerminalProxy {
	return TerminalProxy{
		client: client,
	}
}

func (p TerminalProxy) Register(srv *grpc.Server) {
	RegisterTerminalServer(srv, p)
}

func (p TerminalProxy) Session(stream Terminal_SessionServer) error {
	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.Session(grpcutil.IncomingToOutgoingContext(ctx))
	if err != nil {
		return fmt.Errorf("starting client terminal session: %w", err)
	}
	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, stream)
}
