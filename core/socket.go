package core

import (
	"context"
	"io"
	"net"

	"github.com/moby/buildkit/session/sshforward"
)

type Socket struct {
	ID SocketID `json:"id"`
}

type SocketID string

func (id SocketID) String() string { return string(id) }

type socketIDPayload struct {
	HostPath string `json:"host_path,omitempty"`
}

func (id SocketID) decode() (*socketIDPayload, error) {
	var payload socketIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

func (payload *socketIDPayload) ToSocket() (*Socket, error) {
	id, err := encodeID(payload)
	if err != nil {
		return nil, err
	}

	return NewSocket(SocketID(id)), nil
}

func NewSocket(id SocketID) *Socket {
	return &Socket{id}
}

func NewHostSocket(absPath string) (*Socket, error) {
	payload := socketIDPayload{
		HostPath: absPath,
	}

	return payload.ToSocket()
}

func (socket *Socket) Server() (sshforward.SSHServer, error) {
	payload, err := socket.ID.decode()
	if err != nil {
		return nil, err
	}

	return &socketProxy{
		dial: func() (io.ReadWriteCloser, error) {
			return net.Dial("unix", payload.HostPath)
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
