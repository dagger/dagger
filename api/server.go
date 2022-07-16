package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/graphql-go/graphql"
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

type Server struct {
	gw       bkgw.Client
	platform *specs.Platform
}

func (s Server) ServeConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	go (&http.Server{Handler: s}).Serve(&singleConnListener{Conn: conn})
	<-ctx.Done()
}

func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/graphql" {
		http.NotFound(w, r)
		return
	}
	payload := r.URL.Query().Get("payload")
	result := graphql.Do(graphql.Params{
		Schema:        schema,
		RequestString: payload,
		Context:       withPayload(withPlatform(withGatewayClient(r.Context(), s.gw), s.platform), payload),
	})
	if result.HasErrors() {
		http.Error(w, fmt.Sprintf("unexpected errors: %v", result.Errors), http.StatusInternalServerError)
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result.Data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	net.Conn
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	return l.Conn, nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.LocalAddr()
}
