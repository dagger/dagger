package schema

import (
	"context"
	"runtime/debug"

	"github.com/dagger/dagger/core"
)

type serviceSchema struct {
	*APIServer

	svcs *core.Services
}

var _ SchemaResolvers = &serviceSchema{}

func (s *serviceSchema) Name() string {
	return "service"
}

func (s *serviceSchema) Schema() string {
	return Service
}

func (s *serviceSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Container": ObjectResolver{
			"asService": ToResolver(s.containerAsService),
		},
	}

	ResolveIDable[core.Service](rs, "Service", ObjectResolver{
		"hostname": ToResolver(s.hostname),
		"ports":    ToResolver(s.ports),
		"endpoint": ToResolver(s.endpoint),
		"start":    ToResolver(s.start),
		"stop":     ToResolver(s.stop),
	})

	return rs
}

func (s *serviceSchema) containerAsService(ctx context.Context, parent *core.Container, args any) (*core.Service, error) {
	return parent.Service(ctx, s.bk, s.progSockPath)
}

func (s *serviceSchema) hostname(ctx context.Context, parent *core.Service, args any) (string, error) {
	return parent.Hostname(ctx, s.svcs)
}

func (s *serviceSchema) ports(ctx context.Context, parent *core.Service, args any) ([]core.Port, error) {
	return parent.Ports(ctx, s.svcs)
}

type serviceEndpointArgs struct {
	Port   int
	Scheme string
}

func (s *serviceSchema) endpoint(ctx context.Context, parent *core.Service, args serviceEndpointArgs) (string, error) {
	return parent.Endpoint(ctx, s.svcs, args.Port, args.Scheme)
}

func (s *serviceSchema) start(ctx context.Context, parent *core.Service, args any) (core.ServiceID, error) {
	defer func() {
		if err := recover(); err != nil {
			debug.PrintStack()
			panic(err)
		}
	}()

	running, err := s.svcs.Start(ctx, parent)
	if err != nil {
		return "", err
	}

	return running.Service.ID()
}

type serviceStopArgs struct {
	Kill bool
}

func (s *serviceSchema) stop(ctx context.Context, parent *core.Service, args serviceStopArgs) (core.ServiceID, error) {
	err := s.svcs.Stop(ctx, s.bk, parent, args.Kill)
	if err != nil {
		return "", err
	}

	return parent.ID()
}
