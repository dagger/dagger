package session

import (
	context "context"
	"errors"
	fmt "fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/moby/buildkit/util/grpcerrors"
	"golang.org/x/term"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
)

var toto *os.File

func init() {
	var err error
	toto, err = os.Create("/tmp/toto.log")
	if err != nil {
		panic(fmt.Sprintf("🧠 |%+v|", err))
	}
}

type WithTerminalFunc func(func(stdin io.Reader, stdout, stderr io.Writer) error) error

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
		withTerminal = func(fn func(stdin io.Reader, stdout, stderr io.Writer) error) error {
			return fn(os.Stdin, os.Stdout, os.Stderr)
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

func (s TerminalAttachable) Session(srv Terminal_SessionServer) error {
	return s.withTerminal(func(stdin io.Reader, stdout, stderr io.Writer) error {
		return s.session(srv, stdin, stdout, stderr)
	})
}

func (s TerminalAttachable) session(srv Terminal_SessionServer, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("terminal session finished"))

	if err := s.sendSize(srv, stdout); err != nil {
		return fmt.Errorf("sending initial size: %w", err)
	}
	go s.listenForResize(ctx, srv, stdout)
	go s.forwardStdin(ctx, srv, stdin)

	for {
		req, err := srv.Recv()
		if err != nil {
			if errors.Is(err, context.Canceled) || grpcerrors.Code(err) == codes.Canceled {
				// canceled
				return nil
			}

			if errors.Is(err, io.EOF) {
				// stopped
				return nil
			}

			if grpcerrors.Code(err) == codes.Unavailable {
				// client disconnected (i.e. quitting Dagger out)
				return nil
			}

			return fmt.Errorf("error reading terminal: %w", err)
		}
		switch msg := req.GetMsg().(type) {
		case *SessionRequest_Stdout:
			_, err := stdout.Write(msg.Stdout)
			if err != nil {
				return fmt.Errorf("terminal write stdout: %w", err)
			}
		case *SessionRequest_Stderr:
			_, err := stderr.Write(msg.Stderr)
			if err != nil {
				return fmt.Errorf("terminal write stderr: %w", err)
			}
		case *SessionRequest_Exit:
			fmt.Fprintf(stderr, "exit %d\n", msg.Exit)
			return nil
		}
	}
}

func (s TerminalAttachable) sendSize(srv Terminal_SessionServer, stdout io.Writer) error {
	f, ok := stdout.(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return errors.New("stdin is not a terminal; cannot get terminal size")
	}

	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return fmt.Errorf("get terminal size: %w", err)
	}

	fmt.Fprintf(toto, "😈 resize\n")

	return srv.Send(&SessionResponse{
		Msg: &SessionResponse_Resize{
			Resize: &Resize{
				Width:  int32(w),
				Height: int32(h),
			},
		},
	})
}

func (s TerminalAttachable) forwardStdin(ctx context.Context, srv Terminal_SessionServer, stdin io.Reader) {
	if stdin == nil {
		return
	}
	fmt.Fprintf(toto, "😈 resize |%#v|\n", stdin)
	fmt.Fprintf(toto, "😈 resize |%#v|\n", os.Stdin)
	fmt.Fprintf(toto, "😈😈 resize |%+v|\n", os.Stdin)
	fmt.Fprintf(toto, "😈😈 resize |%+v|\n", stdin)

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
		fmt.Fprintf(toto, "😈 pipe read\n")
		n, err := pipeR.Read(b)
		fmt.Fprintf(toto, "😈 pipe read passed |%+v|%+v|\n", n, err)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				return
			}
			fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
			return
		}

		err = srv.Send(&SessionResponse{
			Msg: &SessionResponse_Stdin{
				Stdin: b[:n],
			},
		})
		fmt.Fprintf(toto, "😈 we sent session response |%+v|%+v|\n", n, err)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forward stdin: %v\n", err)
			return
		}
	}
}
