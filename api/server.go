package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/graphql-go/handler"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/sshforward"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func NewServer(gw bkgw.Client, platform *specs.Platform) Server {
	s := Server{
		gw:       gw,
		platform: platform,
	}

	return s
}

func RunGraphiQLServer(ctx context.Context, port int, gw bkgw.Client, platform *specs.Platform) error {
	s := Server{
		gw:       gw,
		platform: platform,
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return err
	}
	fmt.Printf("serving graphql on localhost:%d...\n", port)
	return s.serve(ctx, ln)
}

type Server struct {
	gw       bkgw.Client
	platform *specs.Platform
}

func (s Server) ServeConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	ch := make(chan net.Conn, 1)
	ch <- conn
	go func() {
		err := s.serve(ctx, &singleConnListener{ch: ch})
		if err != nil {
			// TODO: actual logging
			fmt.Printf("error serving conn: %v\n", err)
		}
	}()
	<-ctx.Done()
}

func (s Server) serve(ctx context.Context, l net.Listener) error {
	return (&http.Server{
		Handler: handler.New(&handler.Config{
			Schema:   &schema,
			Pretty:   true,
			GraphiQL: true,
		}),
		BaseContext: func(net.Listener) context.Context {
			return withPlatform(withGatewayClient(ctx, s.gw), s.platform)
		},
	}).Serve(l)
}

func (s *Server) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, s)
}

func (s *Server) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (s *Server) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	opts, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Errorf(codes.Internal, "no metadata in context")
	}
	v, ok := opts[sshforward.KeySSHID]
	if !ok || len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.Internal, "no sshid in metadata")
	}
	id := v[0]
	if id != daggerSockName {
		return status.Errorf(codes.Internal, "no api connection for id %s", id)
	}

	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.ServeConn(ctx, serverConn) // TODO: better synchronization
	return sshforward.Copy(context.TODO(), clientConn, stream, nil)
}

// converts a pre-existing net.Conn into a net.Listener that returns the conn
type singleConnListener struct {
	ch chan net.Conn
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, io.ErrClosedPipe
	}
	return conn, nil
}

func (l *singleConnListener) Addr() net.Addr {
	return nil
}

func (l *singleConnListener) Close() error {
	close(l.ch)
	return nil
}
