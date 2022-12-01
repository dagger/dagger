package core

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/moby/buildkit/session/sshforward"
)

type Socket struct {
	ID SocketID `json:"id"`
}

type SocketID string

func (id SocketID) String() string { return string(id) }
func (id SocketID) LLBID() string  { return fmt.Sprintf("socket:%s", id) }

type socketIDPayload struct {
	HostNetwork string `json:"host_network,omitempty"`
	HostAddr    string `json:"host_addr,omitempty"`
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

func NewHostSocket(network, addr string) (*Socket, error) {
	payload := socketIDPayload{
		HostNetwork: network,
		HostAddr:    addr,
	}

	return payload.ToSocket()
}

func (socket *Socket) IsHost() (bool, error) {
	payload, err := socket.ID.decode()
	if err != nil {
		return false, err
	}

	return payload.HostNetwork != "", nil
}

func (socket *Socket) Server() (sshforward.SSHServer, error) {
	payload, err := socket.ID.decode()
	if err != nil {
		return nil, err
	}

	return &socketProxy{
		dial: func() (io.ReadWriteCloser, error) {
			return net.Dial(payload.HostNetwork, payload.HostAddr)
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
