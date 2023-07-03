package schema

import (
	"context"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type socketSchema struct {
	*MergedSchemas

	host *core.Host
}

var _ ExecutableSchema = &socketSchema{}

func (s *socketSchema) Name() string {
	return "socket"
}

func (s *socketSchema) Schema() string {
	return Socket
}

var socketIDResolver = stringResolver(core.SocketID(""))

func (s *socketSchema) Resolvers() Resolvers {
	return Resolvers{
		"SocketID": socketIDResolver,
		"Query": ObjectResolver{
			"socket": ToResolver(s.socket),
		},
		"Socket": ObjectResolver{
			"id": ToResolver(s.id),
		},
	}
}

func (s *socketSchema) Dependencies() []ExecutableSchema {
	return nil
}

func (s *socketSchema) id(ctx *core.Context, parent *core.Socket, args any) (core.SocketID, error) {
	return parent.ID()
}

type socketArgs struct {
	ID core.SocketID
}

// nolint: unparam
func (s *socketSchema) socket(_ *core.Context, _ any, args socketArgs) (*core.Socket, error) {
	return args.ID.ToSocket()
}

func (s *MergedSchemas) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	id := sshforward.DefaultID
	if req.ID != "" {
		id = req.ID
	}
	if strings.HasPrefix(id, "socket:") {
		return &sshforward.CheckAgentResponse{}, nil
	}
	return nil, status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
}

func (s *MergedSchemas) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	id := sshforward.DefaultID
	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}

	if key, socketID, ok := strings.Cut(id, ":"); key == "socket" && ok {
		socket, err := core.SocketID(socketID).ToSocket()
		if err != nil {
			return err
		}

		/* TODO: enforce this in session manager instead now
		if socket.IsHost() && !s.EnableHostNetworkAccess {
			return status.Errorf(codes.PermissionDenied, "host network access is disabled")
		}
		*/

		h, err := socket.Server()
		if err != nil {
			return err
		}
		return h.ForwardAgent(stream)
	}
	return status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
}
