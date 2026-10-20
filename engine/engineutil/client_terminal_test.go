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

type fakeTerminalStream struct {
	grpc.ClientStream

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
	<-s.closed
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
