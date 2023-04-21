package core

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/moby/buildkit/session/sshforward"
)

type Socket struct {
	HostPath string `json:"host_path,omitempty"`
}

type SocketID string

func (id SocketID) String() string { return string(id) }
func (id SocketID) LLBID() string  { return fmt.Sprintf("socket:%s", id) }

func (id SocketID) ToSocket() (*Socket, error) {
	var socket Socket
	if err := decodeID(&socket, id); err != nil {
		return nil, err
	}

	return &socket, nil
}

func NewHostSocket(absPath string) *Socket {
	return &Socket{
		HostPath: absPath,
	}
}

func (socket *Socket) ID() (SocketID, error) {
	return encodeID[SocketID](socket)
}

func (socket *Socket) IsHost() bool {
	return socket.HostPath != ""
}

func (socket *Socket) Server() (sshforward.SSHServer, error) {
	return &socketProxy{
		dial: func() (io.ReadWriteCloser, error) {
			return net.Dial("unix", socket.HostPath)
		},
	}, nil
}

type socketProxy struct {
	dial func() (io.ReadWriteCloser, error)
}

var _ sshforward.SSHServer = &socketProxy{}

func (p *socketProxy) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *socketProxy) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	conn, err := p.dial()
	if err != nil {
		return err
	}

	return sshforward.Copy(context.TODO(), conn, stream, nil)
}
