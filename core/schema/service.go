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
			"start":    ToResolver(s.start),
			"stop":     ToResolver(s.stop),
			"proxy":    ToResolver(s.proxy),
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

func (s *serviceSchema) start(ctx *core.Context, parent *core.Service, args any) (core.Void, error) {
	bnd := core.ServiceBinding{
		Service: parent,
	}

	var err error

	// TODO(vito): AllServices.Start takes a Binding, which is normally helpful
	// as it has a precomputed Hostname, but in this case it's a tad clunky
	bnd.Hostname, err = parent.Hostname()
	if err != nil {
		return "", err
	}

	_, err = core.AllServices.Start(ctx, s.bk, bnd)
	if err != nil {
		return "", err
	}

	return "", nil
}

func (s *serviceSchema) stop(ctx *core.Context, parent *core.Service, args any) (core.Void, error) {
	err := core.AllServices.Stop(ctx, s.bk, parent)
	if err != nil {
		return "", err
	}

	return "", nil
}

type serviceProxyArgs struct {
	Address string
	// TODO: family, target
}

func (s *serviceSchema) proxy(ctx *core.Context, parent *core.Service, args serviceProxyArgs) (core.Void, error) {
	// err := core.AllServices.Proxy(ctx, s.bk, parent, args.Address)
	// if err != nil {
	// 	return "", err
	// }

	return "", nil
}
