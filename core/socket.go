package core

import (
	"fmt"
)

type Socket struct {
	*Identified

	// Unix
	HostPath string `json:"host_path,omitempty"`

	// IP
	HostProtocol string `json:"host_protocol,omitempty"`
	HostAddr     string `json:"host_addr,omitempty"`
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

func (socket Socket) Clone() *Socket {
	socket.Identified = socket.Identified.Clone()
	return &socket
}

func (socket *Socket) SocketID() string {
	switch {
	case socket.HostPath != "":
		return fmt.Sprintf("unix://%s", socket.HostPath)
	default:
		return fmt.Sprintf("%s://%s", socket.HostProtocol, socket.HostAddr)
	}
}
