package schema

import (
	"context"

	"github.com/dagger/dagger/core"
)

type socketSchema struct {
	*MergedSchemas

	host *core.Host
}

var _ ExecutableSchema = &socketSchema{}

func (s *socketSchema) Name() string {
	return "socket"
}

func (s *socketSchema) SourceModuleName() string {
	return coreModuleName
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

	ResolveIDable[core.Socket](s.queryCache, rs, "Socket", ObjectResolver{})

	return rs
}

type socketArgs struct {
	ID core.SocketID
}

// nolint: unparam
func (s *socketSchema) socket(_ context.Context, _ any, args socketArgs) (*core.Socket, error) {
	return args.ID.Decode()
}
