package schema

import (
	"context"
	"runtime/debug"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type serviceSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &serviceSchema{}

func (s *serviceSchema) Install() {
	dagql.Fields[*core.Container]{
		dagql.Func("asService", s.containerAsService).
			Doc(`Turn the container into a Service.`,
				`Be sure to set any exposed ports before this conversion.`),
	}.Install(s.srv)

	dagql.Fields[*core.Service]{
		dagql.NodeFunc("hostname", s.hostname).
			Doc(`Retrieves a hostname which can be used by clients to reach this container.`),

		dagql.NodeFunc("ports", s.ports).
			Impure("A tunnel service's ports can change each time it is restarted.").
			Doc(`Retrieves the list of ports provided by the service.`),

		dagql.NodeFunc("endpoint", s.endpoint).
			Impure("A tunnel service's endpoint can change if tunnel service is restarted.").
			Doc(`Retrieves an endpoint that clients can use to reach this container.`,
				`If no port is specified, the first exposed port is used. If none exist an error is returned.`,
				`If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.`).
			ArgDoc("port", `The exposed port number for the endpoint`).
			ArgDoc("scheme", `Return a URL with the given scheme, eg. http for http://`),

		dagql.NodeFunc("start", s.start).
			Impure("Imperatively mutates runtime state.").
			Doc(`Start the service and wait for its health checks to succeed.`,
				`Services bound to a Container do not need to be manually started.`),

		dagql.NodeFunc("stop", s.stop).
			Impure("Imperatively mutates runtime state.").
			Doc(`Stop the service.`),
	}.Install(s.srv)
}

func (s *serviceSchema) containerAsService(ctx context.Context, parent *core.Container, args struct{}) (*core.Service, error) {
	return parent.Service(ctx)
}

func (s *serviceSchema) hostname(ctx context.Context, parent dagql.Instance[*core.Service], args struct{}) (dagql.String, error) {
	hn, err := parent.Self.Hostname(ctx, parent.ID())
	if err != nil {
		return "", err
	}
	return dagql.NewString(hn), nil
}

func (s *serviceSchema) ports(ctx context.Context, parent dagql.Instance[*core.Service], args struct{}) (dagql.Array[core.Port], error) {
	return parent.Self.Ports(ctx, parent.ID())
}

type serviceEndpointArgs struct {
	Port   dagql.Optional[dagql.Int]
	Scheme string `default:""`
}

func (s *serviceSchema) endpoint(ctx context.Context, parent dagql.Instance[*core.Service], args serviceEndpointArgs) (dagql.String, error) {
	str, err := parent.Self.Endpoint(ctx, parent.ID(), args.Port.Value.Int(), args.Scheme)
	if err != nil {
		return "", err
	}
	return dagql.NewString(str), nil
}

func (s *serviceSchema) start(ctx context.Context, parent dagql.Instance[*core.Service], args struct{}) (core.ServiceID, error) {
	defer func() {
		if err := recover(); err != nil {
			debug.PrintStack()
			panic(err)
		}
	}()

	if err := parent.Self.StartAndTrack(ctx, parent.ID()); err != nil {
		return core.ServiceID{}, err
	}

	return dagql.NewID[*core.Service](parent.ID()), nil
}

func (s *serviceSchema) stop(ctx context.Context, parent dagql.Instance[*core.Service], args struct{}) (core.ServiceID, error) {
	if err := parent.Self.Stop(ctx, parent.ID()); err != nil {
		return core.ServiceID{}, err
	}

	err := parent.Self.Stop(ctx, parent.ID())
	if err != nil {
		return core.ServiceID{}, err
	}

	return dagql.NewID[*core.Service](parent.ID()), nil
}
