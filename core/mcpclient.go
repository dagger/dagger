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
)

type ServiceMCPTransport struct {
	Service dagql.ObjectResult[*Service]
}

var _ mcp.Transport = (*ServiceMCPTransport)(nil)

// mcpSessionRegistry tracks the live MCP client sessions of a Dagger session,
// keyed like the per-client service instances behind them (see
// ServiceMCPTransport.Connect).
//
// An LLM's MCP holds its sessions by value, and the LLM is re-derived from its
// ID after every workspace change (MCP.step persists the new binding through a
// withWorkspace selector), so the next tool load starts with no sessions and
// dials again. The service is still running under the same key, though, and
// Services hands the running instance back without attaching the new client's
// pipes — whose first write then blocks forever. Reusing the session is the
// only thing that makes sense anyway: the server is stateful.
//
// A session leaves the registry when it closes, whether through Close or
// because the server exited, so a later dial starts a fresh server.
type mcpSessionRegistry struct {
	mu       sync.Mutex
	sessions map[ServiceKey]*mcp.ClientSession
}

func newMCPSessionRegistry() *mcpSessionRegistry {
	return &mcpSessionRegistry{sessions: map[ServiceKey]*mcp.ClientSession{}}
}

// getOrDial returns the live session for key, dialing one with dial if there
// is none. The lock is held across dial so concurrent dials of the same server
// cannot race each other into the running-instance trap described above.
func (r *mcpSessionRegistry) getOrDial(key ServiceKey, dial func() (*mcp.ClientSession, error)) (*mcp.ClientSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sess, ok := r.sessions[key]; ok {
		return sess, nil
	}
	sess, err := dial()
	if err != nil {
		return nil, err
	}
	r.sessions[key] = sess
	go func() {
		_ = sess.Wait()
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.sessions[key] == sess {
			delete(r.sessions, key)
		}
	}()
	return sess, nil
}

// mcpServiceKey is the key the per-client instance of svc runs under for the
// calling client.
func mcpServiceKey(ctx context.Context, svc dagql.ObjectResult[*Service]) (ServiceKey, error) {
	dig, err := svc.ContentPreferredDigest(ctx)
	if err != nil {
		return ServiceKey{}, fmt.Errorf("service digest: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ServiceKey{}, err
	}
	return ServiceKey{
		Digest:    dig,
		SessionID: clientMetadata.SessionID,
		ClientID:  clientMetadata.ClientID,
		Kind:      ServiceRuntimeShared,
	}, nil
}

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
