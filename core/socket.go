package core

import (
	"context"
	"io"
	"net"

	"github.com/moby/buildkit/session/sshforward"
)

type Socket struct {
	HostPath string `json:"host_path,omitempty"`
	// TODO: explain more, best-effort semi-stable client identifier
	// TODO: think through caching implications one more time, ensure not breaking change
	ClientHostname string `json:"client_hostname,omitempty"`
}

type SocketID string

func (id SocketID) String() string { return string(id) }

func (id SocketID) ToSocket() (*Socket, error) {
	var socket Socket
	if err := decodeID(&socket, id); err != nil {
		return nil, err
	}

	return &socket, nil
}

func NewHostSocket(absPath, clientHostname string) *Socket {
	return &Socket{
		HostPath:       absPath,
		ClientHostname: clientHostname,
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
