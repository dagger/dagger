package core

import (
	"context"
	"io"
	"net"
	"net/url"

	"github.com/moby/buildkit/session/sshforward"
	"github.com/vektah/gqlparser/v2/ast"
)

type Socket struct {
	// Unix
	HostPath string `json:"host_path,omitempty"`

	// IP
	HostProtocol string `json:"host_protocol,omitempty"`
	HostAddr     string `json:"host_addr,omitempty"`
}

func (*Socket) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Socket",
		NonNull:   true,
	}
}

func (*Socket) TypeDescription() string {
	return "A Unix or TCP/IP socket that can be mounted into a container."
}

func NewHostUnixSocket(absPath string) *Socket {
	return &Socket{
		HostPath: absPath,
	}
}

func NewHostIPSocket(proto string, addr string) *Socket {
	return &Socket{
		HostAddr:     addr,
		HostProtocol: proto,
	}
}

func (socket *Socket) SSHID() string {
	u := &url.URL{}
	switch {
	case socket.HostPath != "":
		u.Scheme = "unix"
		u.Path = socket.HostPath
	default:
		u.Scheme = socket.HostProtocol
		u.Host = socket.HostAddr
	}
	return u.String()
}

func (socket *Socket) Server() (sshforward.SSHServer, error) {
	// TODO udp
	return &socketProxy{
		dial: func() (io.ReadWriteCloser, error) {
			return net.Dial(socket.Network(), socket.Addr())
		},
	}, nil
}

func (socket *Socket) Network() string {
	switch {
	case socket.HostPath != "":
		return "unix"
	default:
		return socket.HostProtocol
	}
}

func (socket *Socket) Addr() string {
	switch {
	case socket.HostPath != "":
		return socket.HostPath
	default:
		return socket.HostAddr
	}
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
