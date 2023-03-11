package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type socketSchema struct {
	*baseSchema

	host *core.Host
}

var _ router.ExecutableSchema = &socketSchema{}

func (s *socketSchema) Name() string {
	return "socket"
}

func (s *socketSchema) Schema() string {
	return Socket
}

var socketIDResolver = stringResolver(core.SocketID(""))

func (s *socketSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"SocketID": socketIDResolver,
		"Query": router.ObjectResolver{
			"socket": router.ToResolver(s.socket),
		},
		"Socket": router.ObjectResolver{
			"bind": router.ToResolver(s.socketBind),
		},
	}
}

func (s *socketSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type socketArgs struct {
	ID core.SocketID
}

// nolint: unparam
func (s *socketSchema) socket(_ *router.Context, _ any, args socketArgs) (*core.Socket, error) {
	return core.NewSocket(args.ID), nil
}

type socketBindArgs struct {
	Address string
	Family  core.NetworkFamily
}

func (s *socketSchema) socketBind(ctx *router.Context, parent *core.Socket, args socketBindArgs) (core.Void, error) {
	return core.Nothing, parent.Bind(ctx, s.gw, args.Address, args.Family)
}
