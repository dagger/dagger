package schema

import (
	"github.com/dagger/dagger/core"
)

type serviceSchema struct {
	*MergedSchemas
}

var _ ExecutableSchema = &serviceSchema{}

func (s *serviceSchema) Name() string {
	return "service"
}

func (s *serviceSchema) Schema() string {
	return Service
}

func (s *serviceSchema) Resolvers() Resolvers {
	return Resolvers{
		"ServiceID": stringResolver(core.ServiceID("")),
		"Query": ObjectResolver{
			"service": ToResolver(s.service),
		},
		"Container": ObjectResolver{
			"service": ToResolver(s.containerService),
		},
		"Service": ObjectResolver{
			"id":       ToResolver(s.id),
			"hostname": ToResolver(s.hostname),
			"endpoint": ToResolver(s.endpoint),
		},
	}
}

func (s *serviceSchema) Dependencies() []ExecutableSchema {
	return nil
}

type serviceArgs struct {
	ID core.ServiceID
}

func (s *serviceSchema) service(ctx *core.Context, parent *core.Query, args serviceArgs) (*core.Service, error) {
	return args.ID.ToService()
}

func (s *serviceSchema) containerService(ctx *core.Context, parent *core.Container, args any) (*core.Service, error) {
	return parent.Service(ctx, s.bk, s.progSockPath)
}

func (s *serviceSchema) id(ctx *core.Context, parent *core.Service, args any) (core.ServiceID, error) {
	return parent.ID()
}

func (s *serviceSchema) hostname(ctx *core.Context, parent *core.Service, args any) (string, error) {
	return parent.Hostname()
}

type serviceEndpointArgs struct {
	Port   int
	Scheme string
}

func (s *serviceSchema) endpoint(ctx *core.Context, parent *core.Service, args serviceEndpointArgs) (string, error) {
	return parent.Endpoint(args.Port, args.Scheme)
}
