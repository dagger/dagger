package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dagger/dagger/dagql"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServiceMCPTransport struct {
	Service dagql.ObjectResult[*Service]
}

var _ mcp.Transport = (*ServiceMCPTransport)(nil)

func (t *ServiceMCPTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn := &ServiceMCPConnection{}

	var err error
	conn.svc, err = t.Service.Self().Start(
		ctx,
		t.Service.ID(),
		false, // MUST be false, otherwise
		func(stdin io.Writer, svcProc bkgw.ContainerProcess) {
			conn.w = stdin
		},
		func(stdout io.Reader) {
			conn.r = bufio.NewReader(stdout)
		},
		func(io.Reader) {
			// nothing to do here, stderr will already show up in server logs
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start service: %w", err)
	}

	return conn, nil
}

type ServiceMCPConnection struct {
	svc *RunningService
	r   *bufio.Reader
	w   io.Writer
}

var _ mcp.Connection = (*ServiceMCPConnection)(nil)

// Read implements [mcp.Connection.Read], assuming messages are newline-delimited JSON.
func (t *ServiceMCPConnection) Read(context.Context) (jsonrpc.Message, error) {
	data, err := t.r.ReadBytes('\n')
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

	_, err1 := t.w.Write(data)
	_, err2 := t.w.Write([]byte{'\n'})
	return errors.Join(err1, err2)
}

func (t *ServiceMCPConnection) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return t.svc.Stop(ctx, true)
}

func (t *ServiceMCPConnection) SessionID() string {
	return t.svc.Container.NamespaceID()
}
