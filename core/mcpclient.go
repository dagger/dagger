package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServiceTransport struct {
	Container dagql.ObjectResult[*Container]
	Env       dagql.ObjectResult[*Env]
}

var _ mcp.Transport = (*ServiceTransport)(nil)

func (t *ServiceTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var svc dagql.ObjectResult[*Service]
	if err := srv.Select(ctx, t.Container, &svc, dagql.Selector{
		Field: "withMountedDirectory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString("."),
			},
			{
				Name:  "source",
				Value: dagql.NewID[*Directory](t.Env.Self().Hostfs.ID()),
			},
		},
	}, dagql.Selector{
		Field: "asService",
	}); err != nil {
		return nil, fmt.Errorf("failed to select service: %w", err)
	}

	id := svc.ID().Append(
		svc.Type(),
		svc.ID().Field(),
		"",
		svc.ID().Module(),
		0,
		"",
		// kind hacky, but we just want to pair the service ID with the
		call.NewArgument("env", call.NewLiteralID(t.Env.ID()), false),
	)

	conn := &svcMCPConn{}

	conn.svc, err = svc.Self().Start(
		ctx,
		id,
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

type svcMCPConn struct {
	svc *RunningService
	r   *bufio.Reader
	w   io.Writer
}

var _ mcp.Connection = (*svcMCPConn)(nil)

// Read implements [mcp.Connection.Read], assuming messages are newline-delimited JSON.
func (t *svcMCPConn) Read(context.Context) (jsonrpc.Message, error) {
	data, err := t.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	return jsonrpc.DecodeMessage(data[:len(data)-1])
}

// Write implements [mcp.Connection.Write], appending a newline delimiter after the message.
func (t *svcMCPConn) Write(_ context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}

	_, err1 := t.w.Write(data)
	_, err2 := t.w.Write([]byte{'\n'})
	return errors.Join(err1, err2)
}

func (t *svcMCPConn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return t.svc.Stop(ctx, true)
}

// SessionID implements [mcp.Connection.SessionID]. Since this is a simplified example,
// it returns an empty session ID.
func (t *svcMCPConn) SessionID() string {
	return t.svc.Host
}
