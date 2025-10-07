package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dagger/dagger/dagql"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	svc, err := svcs.StartWithIO(
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
