package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/socket"
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

var socketIDResolver = stringResolver(socket.ID(""))

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

func (s *socketSchema) id(ctx *core.Context, parent *socket.Socket, args any) (socket.ID, error) {
	return parent.ID()
}

type socketArgs struct {
	ID socket.ID
}

// nolint: unparam
func (s *socketSchema) socket(_ *core.Context, _ any, args socketArgs) (*socket.Socket, error) {
	return args.ID.Decode()
}
