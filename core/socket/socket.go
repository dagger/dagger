package socket

import (
	"context"
	"io"
	"net"

	"github.com/dagger/dagger/core/resourceid"
	"github.com/moby/buildkit/session/sshforward"
	"github.com/opencontainers/go-digest"
)

type Socket struct {
	HostPath string `json:"host_path,omitempty"`
}

type ID = resourceid.ID[Socket]

func (socket *Socket) Digest() (digest.Digest, error) {
	id, err := socket.ID()
	if err != nil {
		return "", err
	}
	return digest.FromString(string(id)), nil
}

func NewHostSocket(absPath string) *Socket {
	return &Socket{
		HostPath: absPath,
	}
}

func (socket *Socket) ID() (ID, error) {
	return resourceid.Encode(socket)
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
