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

func (s *socketSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"socket": ToResolver(s.socket),
		},
	}

	ResolveIDable[socket.Socket](rs, "Socket", ObjectResolver{})

	return rs
}

func (s *socketSchema) Dependencies() []ExecutableSchema {
	return nil
}

type socketArgs struct {
	ID socket.ID
}

// nolint: unparam
func (s *socketSchema) socket(_ *core.Context, _ any, args socketArgs) (*socket.Socket, error) {
	return args.ID.Decode()
}
