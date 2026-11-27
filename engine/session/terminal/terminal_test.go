package terminal

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
)

// fakeSessionServer is an in-memory Terminal_SessionServer: it replays reqs
// to the attachable and records everything sent back.
type fakeSessionServer struct {
	grpc.ServerStream
	ctx context.Context

	mu   sync.Mutex
	reqs []*SessionRequest
	sent []*SessionResponse
}

func (f *fakeSessionServer) Context() context.Context { return f.ctx }

func (f *fakeSessionServer) Send(res *SessionResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, res)
	return nil
}

func (f *fakeSessionServer) Recv() (*SessionRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		return nil, io.EOF
	}
	req := f.reqs[0]
	f.reqs = f.reqs[1:]
	return req, nil
}

// A session must work when stdout is not a terminal, such as when the CLI's
// output is redirected to a file.
func TestSessionWithoutTerminalStdout(t *testing.T) {
	srv := &fakeSessionServer{
		ctx: context.Background(),
		reqs: []*SessionRequest{
			{Msg: &SessionRequest_Stdout{Stdout: []byte("hello\n")}},
			{Msg: &SessionRequest_Exit{Exit: 0}},
		},
	}

	var stdout, stderr bytes.Buffer
	attachable := NewTerminalAttachable(context.Background(), nil)
	err := attachable.session(srv, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("session failed: %v", err)
	}
	if got := stdout.String(); got != "hello\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.sent) == 0 || srv.sent[0].GetReady() == nil {
		t.Fatalf("expected a ready message first, got %v", srv.sent)
	}
	for _, res := range srv.sent {
		if res.GetResize() != nil {
			t.Fatalf("unexpected resize for a non-terminal stdout: %v", res)
		}
	}
}
