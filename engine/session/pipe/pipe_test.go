package pipe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// scriptedStream feeds Recv from a fixed list and records what Send is given.
type scriptedStream struct {
	recv  []*Data
	sent  []*Data
	calls int
}

func (s *scriptedStream) Send(d *Data) error {
	s.sent = append(s.sent, d)
	return nil
}

func (s *scriptedStream) Recv() (*Data, error) {
	s.calls++
	if len(s.recv) == 0 {
		return nil, errors.New("recv called past the end of the script")
	}
	d := s.recv[0]
	s.recv = s.recv[1:]
	return d, nil
}

func TestPipeIOReadEOFMarker(t *testing.T) {
	stream := &scriptedStream{recv: []*Data{{Data: []byte("hello")}, {Eof: true}}}
	pio := &PipeIO{GRPC: stream}

	buf := make([]byte, 16)
	n, err := pio.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "hello", string(buf[:n]))

	n, err = pio.Read(buf)
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)

	// EOF is sticky: no further Recv, no scripted error.
	n, err = pio.Read(buf)
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, 2, stream.calls)
}

func TestPipeIOReadEOFMarkerWithData(t *testing.T) {
	stream := &scriptedStream{recv: []*Data{{Data: []byte("tail"), Eof: true}}}
	pio := &PipeIO{GRPC: stream}

	all, err := io.ReadAll(pio)
	require.NoError(t, err)
	require.Equal(t, "tail", string(all))
	require.Equal(t, 1, stream.calls)
}

func TestPipeIOCloseWrite(t *testing.T) {
	stream := &scriptedStream{}
	pio := &PipeIO{GRPC: stream}
	_, err := pio.Write([]byte("data"))
	require.NoError(t, err)
	require.NoError(t, pio.CloseWrite())
	require.Len(t, stream.sent, 2)
	require.Equal(t, []byte("data"), stream.sent[0].Data)
	require.False(t, stream.sent[0].Eof)
	require.Empty(t, stream.sent[1].Data)
	require.True(t, stream.sent[1].Eof)
}

// TestPipeAttachableForwardsStdinEOF drives the attachable over a real gRPC
// stream: once the client's stdin ends, the engine-side reader observes
// io.EOF, and the engine can still write to the client's stdout afterwards.
func TestPipeAttachableForwardsStdinEOF(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	var stdout lockedBuffer
	engineSide := connectAttachable(t, stdinR, &stdout)

	// stdin -> engine
	_, err := io.WriteString(stdinW, "request\n")
	require.NoError(t, err)
	buf := make([]byte, 64)
	n, err := engineSide.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "request\n", string(buf[:n]))

	// closing stdin surfaces as io.EOF on the engine side
	require.NoError(t, stdinW.Close())
	n, err = engineSide.Read(buf)
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)

	// engine -> stdout still works after the client's input ended
	_, err = io.WriteString(engineSide, "response\n")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return stdout.String() == "response\n" }, 5*time.Second, 10*time.Millisecond)
}

// TestPipeAttachableEOFBeforeAnyRead covers stdin closing before the engine
// has read anything: the marker must not be lost.
func TestPipeAttachableEOFBeforeAnyRead(t *testing.T) {
	engineSide := connectAttachable(t, bytes.NewReader(nil), io.Discard)

	all, err := io.ReadAll(engineSide)
	require.NoError(t, err)
	require.Empty(t, all)
}

// connectAttachable serves a PipeAttachable over an in-memory gRPC connection
// and returns the engine's end of the IO stream.
func connectAttachable(t *testing.T, stdin io.Reader, stdout io.Writer) *PipeIO {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	NewPipeAttachable(ctx, stdin, stdout).Register(grpcServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///pipe", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	stream, err := NewPipeClient(conn).IO(ctx)
	require.NoError(t, err)
	return &PipeIO{GRPC: stream}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
