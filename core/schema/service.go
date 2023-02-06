package schema

import (
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type serviceSchema struct {
	*baseSchema

	services *core.Services
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
		"Service": router.ObjectResolver{
			"detach": router.ToResolver(s.detach),
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
	svc, found := s.services.Service(args.ID)
	if !found {
		return nil, fmt.Errorf("service %q not found", args.ID)
	}
	return svc, nil
}

func (s *serviceSchema) detach(ctx *router.Context, parent *core.Service, args any) (bool, error) {
	parent.Detach()
	return true, nil
}
