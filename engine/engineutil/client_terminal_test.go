package engineutil

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/session/terminal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// The client stops reading at the exit message, so all output must be sent
// before it, even when sending the output is slow.
func TestTerminalClientSendsOutputBeforeExit(t *testing.T) {
	stream := newFakeTerminalStream()
	term, err := newTerminalClient(context.Background(), stream)
	require.NoError(t, err)

	_, err = term.Stdout.Write([]byte("out"))
	require.NoError(t, err)
	_, err = term.Stderr.Write([]byte("err"))
	require.NoError(t, err)
	require.NoError(t, term.Close(3))

	sent := stream.sentMessages()
	require.Len(t, sent, 3)
	require.ElementsMatch(t, []string{"stdout:out", "stderr:err"}, sent[:2])
	require.Equal(t, "exit:3", sent[2])
}

// Closing must wait for the client to process the session: once it returns,
// the terminal call can complete, and a client that sees it complete shuts
// down and drops whatever it has not processed yet.
func TestTerminalClientCloseWaitsForClient(t *testing.T) {
	stream := newFakeTerminalStream()
	stream.clientEnd = make(chan struct{})
	term, err := newTerminalClient(context.Background(), stream)
	require.NoError(t, err)

	closed := make(chan error, 1)
	go func() { closed <- term.Close(0) }()
	select {
	case <-closed:
		t.Fatal("Close returned before the client ended the session")
	case <-time.After(200 * time.Millisecond):
	}

	close(stream.clientEnd)
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the client ended the session")
	}
}

// Input the process no longer reads must not end the wait early or stall it:
// the session is over only when the client ends it.
func TestTerminalClientCloseWaitsForClientDespiteInput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		closedStdin bool
	}{
		{name: "stdin closed", closedStdin: true},
		{name: "stdin unread", closedStdin: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := newFakeTerminalStream()
			stream.clientEnd = make(chan struct{})
			stream.queued = []*terminal.SessionResponse{
				{Msg: &terminal.SessionResponse_Stdin{Stdin: []byte("typed")}},
			}
			term, err := newTerminalClient(context.Background(), stream)
			require.NoError(t, err)
			if tc.closedStdin {
				// as the executor does once the process exits
				require.NoError(t, term.Stdin.Close())
			}

			closed := make(chan error, 1)
			go func() { closed <- term.Close(0) }()
			select {
			case <-closed:
				t.Fatal("Close returned before the client ended the session")
			case <-time.After(200 * time.Millisecond):
			}

			close(stream.clientEnd)
			select {
			case err := <-closed:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("Close did not return promptly after the client ended the session")
			}
		})
	}
}

type fakeTerminalStream struct {
	grpc.ClientStream

	// clientEnd, if set, is when the client ends the session; otherwise it
	// ends as soon as the stream is closed for sending.
	clientEnd chan struct{}
	// queued is what the client sends after Ready, before it ends.
	queued    []*terminal.SessionResponse
	closed    chan struct{}
	closeOnce sync.Once
	readySent bool

	mu   sync.Mutex
	sent []string
}

var _ terminal.Terminal_SessionClient = (*fakeTerminalStream)(nil)

func newFakeTerminalStream() *fakeTerminalStream {
	return &fakeTerminalStream{closed: make(chan struct{})}
}

func (s *fakeTerminalStream) Send(req *terminal.SessionRequest) error {
	var msg string
	switch m := req.GetMsg().(type) {
	case *terminal.SessionRequest_Stdout:
		// Sending output is slow, e.g. under load.
		time.Sleep(100 * time.Millisecond)
		msg = "stdout:" + string(m.Stdout)
	case *terminal.SessionRequest_Stderr:
		time.Sleep(100 * time.Millisecond)
		msg = "stderr:" + string(m.Stderr)
	case *terminal.SessionRequest_Exit:
		msg = fmt.Sprintf("exit:%d", m.Exit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	return nil
}

func (s *fakeTerminalStream) Recv() (*terminal.SessionResponse, error) {
	if !s.readySent {
		s.readySent = true
		return &terminal.SessionResponse{Msg: &terminal.SessionResponse_Ready{}}, nil
	}
	if len(s.queued) > 0 {
		res := s.queued[0]
		s.queued = s.queued[1:]
		return res, nil
	}
	if s.clientEnd != nil {
		<-s.clientEnd
	} else {
		<-s.closed
	}
	return nil, io.EOF
}

func (s *fakeTerminalStream) CloseSend() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *fakeTerminalStream) sentMessages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}
