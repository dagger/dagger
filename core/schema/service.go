package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type serviceSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &serviceSchema{}

func (s *serviceSchema) Name() string {
	return "service"
}

func (s *serviceSchema) Schema() string {
	return Service
}

func (s *serviceSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"ServiceID": stringResolver(core.ServiceID("")),
		"Query": router.ObjectResolver{
			"service": router.ToResolver(s.service),
		},
		"Container": router.ObjectResolver{
			"service": router.ToResolver(s.containerService),
		},
		"Service": router.ObjectResolver{
			"id":       router.ToResolver(s.id),
			"hostname": router.ToResolver(s.hostname),
			"endpoint": router.ToResolver(s.endpoint),
		},
	}
}

func (s *serviceSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type serviceArgs struct {
	ID core.ServiceID
}

func (s *serviceSchema) service(ctx *router.Context, parent *core.Query, args serviceArgs) (*core.Service, error) {
	return args.ID.ToService()
}

func (s *serviceSchema) containerService(ctx *router.Context, parent *core.Container, args any) (*core.Service, error) {
	return parent.Service, nil
}

func (s *serviceSchema) id(ctx *router.Context, parent *core.Service, args any) (core.ServiceID, error) {
	return parent.ID()
}

func (s *serviceSchema) hostname(ctx *router.Context, parent *core.Service, args any) (string, error) {
	if !s.servicesEnabled {
		return "", ErrServicesDisabled
	}

	return parent.Hostname()
}

type serviceEndpointArgs struct {
	Port   int
	Scheme string
}

func (s *serviceSchema) endpoint(ctx *router.Context, parent *core.Service, args serviceEndpointArgs) (string, error) {
	if !s.servicesEnabled {
		return "", ErrServicesDisabled
	}

	return parent.Endpoint(args.Port, args.Scheme)
}
