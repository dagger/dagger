package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opencontainers/go-digest"
)

// MCPSessions holds a Dagger session's live MCP sessions: one per client and
// MCP server service, opened on first use and shared by every step of every
// conversation that client drives against that server.
//
// A session can't live on the LLM value: each step works on a transient clone
// and records its outcome as a new value, so a session opened during one step
// would be gone by the next. Nor can the next step simply dial again: the
// session owns the stdio of the service it started, and dialing the same
// running service again gets no stdio of its own.
type MCPSessions struct {
	mu       sync.Mutex
	sessions map[mcpSessionKey]*mcpSessionDial
}

// mcpSessionKey identifies a session as ServiceMCPTransport identifies the
// service it starts: a client-specific instance of the service's digest.
type mcpSessionKey struct {
	clientID string
	service  digest.Digest
}

type mcpSessionDial struct {
	done chan struct{}
	sess *mcp.ClientSession
	err  error
}

func NewMCPSessions() *MCPSessions {
	return &MCPSessions{sessions: map[mcpSessionKey]*mcpSessionDial{}}
}

// Session returns the current client's live session with srv, dialing it if
// there is none yet. A session lasts until its server's service stops, at the
// latest when the Dagger session ends; the next call then dials again.
func (s *MCPSessions) Session(ctx context.Context, srv *MCPServerConfig) (*mcp.ClientSession, error) {
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	dig, err := srv.Service.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("service digest: %w", err)
	}
	key := mcpSessionKey{clientID: clientMD.ClientID, service: dig}

	s.mu.Lock()
	dial, dialing := s.sessions[key]
	if !dialing {
		dial = &mcpSessionDial{done: make(chan struct{})}
		s.sessions[key] = dial
	}
	s.mu.Unlock()

	if dialing {
		select {
		case <-dial.done:
			return dial.sess, dial.err
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}

	dial.sess, dial.err = srv.Dial(ctx)
	close(dial.done)
	if dial.err != nil {
		s.forget(key, dial)
		return nil, dial.err
	}
	go func() {
		// The session ends with its server's stdio, when its service stops.
		_ = dial.sess.Wait()
		s.forget(key, dial)
	}()
	return dial.sess, nil
}

func (s *MCPSessions) forget(key mcpSessionKey, dial *mcpSessionDial) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[key] == dial {
		delete(s.sessions, key)
	}
}

type ServiceMCPTransport struct {
	Service dagql.ObjectResult[*Service]
}

var _ mcp.Transport = (*ServiceMCPTransport)(nil)

func (t *ServiceMCPTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	svc, err := svcs.StartResultWithIO(
		ctx,
		t.Service,
		true, // per-client instances
		&ServiceIO{
			Stdin:  stdinR,
			Stdout: stdoutW,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start service: %w", err)
	}

	return &ServiceMCPConnection{
		svc:     svc,
		stdinR:  stdinR,
		stdinW:  stdinW,
		stdoutR: bufio.NewReader(stdoutR),
	}, nil
}

type ServiceMCPConnection struct {
	svc     *RunningService
	stdinR  io.ReadCloser
	stdinW  io.WriteCloser
	stdoutR *bufio.Reader
}

var _ mcp.Connection = (*ServiceMCPConnection)(nil)

// Read implements [mcp.Connection.Read], assuming messages are newline-delimited JSON.
func (t *ServiceMCPConnection) Read(context.Context) (jsonrpc.Message, error) {
	data, err := t.stdoutR.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	return jsonrpc.DecodeMessage(data[:len(data)-1])
}

// Write implements [mcp.Connection.Write], appending a newline delimiter after the message.
func (t *ServiceMCPConnection) Write(_ context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}

	_, err1 := t.stdinW.Write(data)
	_, err2 := t.stdinW.Write([]byte{'\n'})
	return errors.Join(err1, err2)
}

func (t *ServiceMCPConnection) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return t.svc.Stop(ctx, true)
}

func (t *ServiceMCPConnection) SessionID() string {
	return t.svc.ContainerID
}
