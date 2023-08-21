package schema

import (
	"runtime/debug"

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
			"id":        ToResolver(s.id),
			"hostname":  ToResolver(s.hostname),
			"ports":     ToResolver(s.ports),
			"endpoint":  ToResolver(s.endpoint),
			"endpoints": ToResolver(s.endpoints),
			"start":     ToResolver(s.start),
			"stop":      ToResolver(s.stop),
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
	return parent.Hostname(ctx)
}

func (s *serviceSchema) ports(ctx *core.Context, parent *core.Service, args any) ([]core.Port, error) {
	return parent.Ports(ctx)
}

type serviceEndpointsArgs struct {
	Scheme string
}

func (s *serviceSchema) endpoints(ctx *core.Context, parent *core.Service, args serviceEndpointsArgs) ([]string, error) {
	return parent.Endpoints(ctx, args.Scheme)
}

type serviceEndpointArgs struct {
	Port   int
	Scheme string
}

func (s *serviceSchema) endpoint(ctx *core.Context, parent *core.Service, args serviceEndpointArgs) (string, error) {
	return parent.Endpoint(ctx, args.Port, args.Scheme)
}

func (s *serviceSchema) start(ctx *core.Context, parent *core.Service, args any) (core.ServiceID, error) {
	defer func() {
		if err := recover(); err != nil {
			debug.PrintStack()
			panic(err)
		}
	}()

	running, err := core.AllServices.Start(ctx, s.bk, parent)
	if err != nil {
		return "", err
	}

	return running.Service.ID()
}

func (s *serviceSchema) stop(ctx *core.Context, parent *core.Service, args any) (core.ServiceID, error) {
	err := core.AllServices.Stop(ctx, s.bk, parent)
	if err != nil {
		return "", err
	}

	return parent.ID()
}
