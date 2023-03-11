package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/sshforward"
	"github.com/moby/buildkit/solver/pb"
	"github.com/sirupsen/logrus"
)

type Socket struct {
	ID SocketID `json:"id"`
}

type SocketID string

func (id SocketID) String() string { return string(id) }
func (id SocketID) LLBID() string  { return fmt.Sprintf("socket:%s", id) }

type socketIDPayload struct {
	// Unix socket on the host.
	HostPath string `json:"host_path,omitempty"`

	// IP socket in a container.
	ContainerID       ContainerID     `json:"container_id,omitempty"`
	ContainerProtocol NetworkProtocol `json:"container_protocol,omitempty"`
	ContainerPort     int             `json:"container_port,omitempty"`
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

func NewContainerSocket(id ContainerID, port int, protocol NetworkProtocol) (*Socket, error) {
	payload := socketIDPayload{
		ContainerID:       id,
		ContainerPort:     port,
		ContainerProtocol: protocol,
	}

	return payload.ToSocket()
}

func (socket *Socket) IsHost() (bool, error) {
	payload, err := socket.ID.decode()
	if err != nil {
		return false, err
	}

	return payload.HostPath != "", nil
}

func (socket *Socket) Dial(ctx context.Context, gw bkgw.Client) (net.Conn, error) {
	payload, err := socket.ID.decode()
	if err != nil {
		return nil, err
	}

	switch {
	case payload.HostPath != "":
		return (&net.Dialer{}).DialContext(ctx, "unix", payload.HostPath)
	case payload.ContainerID != "":
		r, w := net.Pipe()

		containerPayload, err := payload.ContainerID.decode()
		if err != nil {
			return nil, err
		}

		scratchRes, err := result(ctx, gw, llb.Scratch())
		if err != nil {
			return nil, err
		}

		containerReq := bkgw.NewContainerRequest{
			Mounts: []bkgw.Mount{
				{
					Dest:      "/",
					MountType: pb.MountType_BIND,
					Ref:       scratchRes.Ref,
				},
			},
			NetMode: pb.NetMode_HOST,
		}

		go proxyConn(
			ctx,
			gw,
			containerReq,
			containerPayload.Hostname,
			payload.ContainerPort,
			payload.ContainerProtocol,
			r,
		)

		return w, nil

	default:
		return nil, fmt.Errorf("unknown socket type! this is probably a bug")
	}
}

func (socket *Socket) Bind(ctx context.Context, gw bkgw.Client, addr string, family NetworkFamily) error {
	payload, err := socket.ID.decode()
	if err != nil {
		return err
	}

	if payload.HostPath != "" {
		return fmt.Errorf("cannot bind to a host socket")
	}

	containerPayload, err := payload.ContainerID.decode()
	if err != nil {
		return err
	}

	scratchRes, err := result(ctx, gw, llb.Scratch())
	if err != nil {
		return err
	}

	containerReq := bkgw.NewContainerRequest{
		Mounts: []bkgw.Mount{
			{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       scratchRes.Ref,
			},
		},
		NetMode: pb.NetMode_HOST,
	}

	var l net.Listener
	switch family {
	case NetworkFamilyIP:
		switch payload.ContainerProtocol {
		case NetworkProtocolTCP:
			l, err = net.Listen("tcp", addr)
		// case NetworkProtocolUDP:
		// 	l, err = net.ListenUDP("udp", addr)
		default:
			return fmt.Errorf("unknown network protocol: %s", payload.ContainerProtocol)
		}
	case NetworkFamilyUNIX:
		l, err = net.Listen("unix", addr)
	default:
		return fmt.Errorf("unknown socket type! this is probably a bug")
	}

	if err != nil {
		return err
	}

	// go func() {
	for {
		conn, err := l.Accept()
		if err != nil {
			logrus.Warnf("bind %s accept error: %s", addr, err)
			break
		}

		go proxyConn(
			ctx,
			gw,
			containerReq,
			containerPayload.Hostname,
			payload.ContainerPort,
			payload.ContainerProtocol,
			conn,
		)
	}
	// }()

	return nil
}

func proxyConn(ctx context.Context, gw bkgw.Client, containerReq bkgw.NewContainerRequest, host string, port int, proto NetworkProtocol, conn net.Conn) {
	defer conn.Close()

	proxyContainer, err := gw.NewContainer(ctx, containerReq)
	if err != nil {
		logrus.Errorf("failed to create proxy container: %s", err)
		return
	}

	// NB: use a different ctx than the one that'll be interrupted for anything
	// that needs to run as part of post-interruption cleanup
	cleanupCtx := context.Background()

	defer proxyContainer.Release(cleanupCtx)

	proc, err := proxyContainer.Start(ctx, bkgw.StartRequest{
		Args:   []string{"proxy", host, fmt.Sprintf("%d/%s", port, proto.Network())},
		Env:    []string{"_DAGGER_INTERNAL_COMMAND="},
		Stdin:  conn,
		Stdout: conn,
		Stderr: os.Stderr, // TODO(vito)
	})
	if err != nil {
		logrus.Errorf("failed to start proxy process: %s", err)
		return
	}

	go func() {
		<-ctx.Done()

		err := proc.Signal(cleanupCtx, syscall.SIGKILL)
		if err != nil {
			logrus.Warnf("failed to kill proxy: %s", err)
			return
		}
	}()

	err = proc.Wait()
	if err != nil {
		logrus.Warnf("proxy exited with error: %s", err)
		return
	}
}

func (socket *Socket) Server() (sshforward.SSHServer, error) {
	return &socketSSHServer{
		dial: func() (io.ReadWriteCloser, error) {
			return socket.Dial(context.TODO(), nil) // TODO(vito): no gateway
		},
	}, nil
}

type socketSSHServer struct {
	dial func() (io.ReadWriteCloser, error)
}

var _ sshforward.SSHServer = &socketSSHServer{}

func (p *socketSSHServer) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *socketSSHServer) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	conn, err := p.dial()
	if err != nil {
		return err
	}

	return sshforward.Copy(context.TODO(), conn, stream, nil)
}

// NetworkFamily is a string deriving from NetworkFamily enum
type NetworkFamily string

const (
	NetworkFamilyIP   NetworkFamily = "IP"
	NetworkFamilyUNIX NetworkFamily = "UNIX"
)
