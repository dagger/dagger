package schema

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/ioctx"
	"github.com/moby/buildkit/identity"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
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

		dagql.NodeFunc("up", s.up).
			Impure("Starts a host tunnel, possibly with ports that change each time it's started.").
			Doc(`Creates a tunnel that forwards traffic from the caller's network to this service.`).
			ArgDoc("random", `Bind each tunnel port to a random port on the host.`).
			ArgDoc("ports", `List of frontend/backend port mappings to forward.`,
				`Frontend is the port accepting traffic on the host, backend is the service port.`),

		dagql.NodeFunc("stop", s.stop).
			Impure("Imperatively mutates runtime state.").
			Doc(`Stop the service.`).
			ArgDoc("kill", `Immediately kill the service without waiting for a graceful exit`),
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

type serviceStopArgs struct {
	Kill bool `default:"false"`
}

func (s *serviceSchema) stop(ctx context.Context, parent dagql.Instance[*core.Service], args serviceStopArgs) (core.ServiceID, error) {
	if err := parent.Self.Stop(ctx, parent.ID(), args.Kill); err != nil {
		return core.ServiceID{}, err
	}
	return dagql.NewID[*core.Service](parent.ID()), nil
}

type upArgs struct {
	Ports  []dagql.InputObject[core.PortForward] `default:"[]"`
	Random bool                                  `default:"false"`
}

func (s *serviceSchema) up(ctx context.Context, svc dagql.Instance[*core.Service], args upArgs) (dagql.Nullable[core.Void], error) {
	void := dagql.Null[core.Void]()

	var hostSvc dagql.Instance[*core.Service]
	err := s.srv.Select(ctx, s.srv.Root(), &hostSvc,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "tunnel",
			Args: []dagql.NamedInput{
				{Name: "service", Value: dagql.NewID[*core.Service](svc.ID())},
				{Name: "ports", Value: dagql.ArrayInput[dagql.InputObject[core.PortForward]](args.Ports)},
				{Name: "native", Value: dagql.Boolean(!args.Random)},
			},
		},
	)
	if err != nil {
		return void, fmt.Errorf("failed to select host service: %w", err)
	}

	runningSvc, err := hostSvc.Self.Query.Services.Start(ctx, hostSvc.ID(), hostSvc.Self)
	if err != nil {
		return void, fmt.Errorf("failed to start host service: %w", err)
	}

	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex(digest.Digest(identity.NewID()), "", progrock.Focused())
	defer vtx.Done(nil)
	ioctxOut := ioctx.Stdout(ctx) // TODO: consolidate to just this once new UI is up and running

	for _, port := range runningSvc.Ports {
		portStr := fmt.Sprintf("%d/%s", port.Port, port.Protocol)
		if port.Description != nil {
			portStr += ": " + *port.Description
		}
		portStr += "\n"

		vtx.Stdout().Write([]byte(portStr))
		ioctxOut.Write([]byte(portStr))
	}

	<-ctx.Done()
	return void, nil
}
