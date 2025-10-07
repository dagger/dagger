package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPClients struct {
	services *Services
	clients  map[string]*mcp.ClientSession
	clientsL *sync.Mutex
}

func NewMCPClients() *MCPClients {
	return &MCPClients{
		services: NewServices(),
		clients:  make(map[string]*mcp.ClientSession),
		clientsL: &sync.Mutex{},
	}
}

func (mcps *MCPClients) Dial(ctx context.Context, cfg *MCPServerConfig) (_ *mcp.ClientSession, rerr error) {
	mcps.clientsL.Lock()
	defer mcps.clientsL.Unlock()
	var containerID string
	running, err := mcps.services.Get(ctx, cfg.Service.ID(), true)
	if err != nil {
		// not started yet; start + connect
		ctx, span := Tracer(ctx).Start(ctx, "start mcp server: "+cfg.Name, telemetry.Reveal())
		defer telemetry.End(span, func() error { return rerr })
		sess, err := mcp.NewClient(&mcp.Implementation{
			Title:   "Dagger",
			Version: engine.Version,
		}, nil).Connect(ctx, &ServiceMCPTransport{
			Service:  cfg.Service,
			Services: mcps.services,
		}, nil)
		if err != nil {
			return nil, err
		}
		containerID = sess.ID()
		mcps.clients[containerID] = sess
	} else {
		containerID = running.ContainerID
	}
	return mcps.clients[containerID], nil
}

// MCPServerConfig represents configuration for an external MCP server
type MCPServerConfig struct {
	// Name of the MCP server
	Name string

	// Command to run the MCP server
	Service dagql.ObjectResult[*Service]
}

type ServiceMCPTransport struct {
	Services *Services
	Service  dagql.ObjectResult[*Service]
}

var _ mcp.Transport = (*ServiceMCPTransport)(nil)

func (t *ServiceMCPTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	svc, err := t.Services.StartWithIO(
		ctx,
		t.Service.ID(),
		t.Service.Self(),
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
