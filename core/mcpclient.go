// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dagger/dagger/dagql"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServiceTransport struct {
	Service dagql.ObjectResult[*Service]
	nth     int
}

var _ mcp.Transport = (*ServiceTransport)(nil)

func (t *ServiceTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	// Give each connection a distinct service ID.
	t.nth++
	id := t.Service.ID().Append(
		t.Service.Type(),
		t.Service.ID().Field(),
		"",
		t.Service.ID().Module(),
		t.nth,
		"",
	)

	conn := &svcMCPConn{}

	var err error
	conn.svc, err = t.Service.Self().Start(
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

// Close implements [mcp.Connection.Close]. Since this is a simplified example, it is a no-op.
func (t *svcMCPConn) Close() error {
	return t.svc.Stop(context.TODO(), true)
}

// SessionID implements [mcp.Connection.SessionID]. Since this is a simplified example,
// it returns an empty session ID.
func (t *svcMCPConn) SessionID() string {
	return t.svc.Host
}
