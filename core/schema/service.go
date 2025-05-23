package schema

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"go.opentelemetry.io/otel/trace"
)

type serviceSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &serviceSchema{}

func (s *serviceSchema) Install() {
	dagql.Fields[*core.Container]{
		dagql.NodeFunc("asService", s.containerAsServiceLegacy).
			View(BeforeVersion("v0.15.0")).
			Doc(`Turn the container into a Service.`,
				`Be sure to set any exposed ports before this conversion.`),

		dagql.Func("asService", s.containerAsService).
			View(AfterVersion("v0.15.0")).
			Doc(`Turn the container into a Service.`,
				`Be sure to set any exposed ports before this conversion.`).
			Args(
				dagql.Arg("args").Doc(
					`Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).`,
					`If empty, the container's default command is used.`),
				dagql.Arg("useEntrypoint").Doc(
					`If the container has an entrypoint, prepend it to the args.`),
				dagql.Arg("experimentalPrivilegedNesting").Doc(
					`Provides Dagger access to the executed command.`),
				dagql.Arg("insecureRootCapabilities").Doc(
					`Execute the command with all root capabilities. This is similar to
					running a command with "sudo" or executing "docker run" with the
					"--privileged" flag. Containerization does not provide any security
					guarantees when using this option. It should only be used when
					absolutely necessary and only with trusted commands.`),
				dagql.Arg("expand").Doc(
					`Replace "${VAR}" or "$VAR" in the args according to the current `+
						`environment variables defined in the container (e.g. "/$VAR/foo").`),
				dagql.Arg("noInit").Doc(
					`If set, skip the automatic init process injected into containers by default.`,
					`This should only be used if the user requires that their exec process be the
					pid 1 process in the container. Otherwise it may result in unexpected behavior.`,
				),
			),

		dagql.NodeFunc("up", s.containerUpLegacy).
			View(BeforeVersion("v0.15.2")).
			DoNotCache("Starts a host tunnel, possibly with ports that change each time it's started.").
			Doc(`Starts a Service and creates a tunnel that forwards traffic from the caller's network to that service.`,
				`Be sure to set any exposed ports before calling this api.`).
			Args(
				dagql.Arg("ports").Doc(`List of frontend/backend port mappings to forward.`,
					`Frontend is the port accepting traffic on the host, backend is the service port.`),
				dagql.Arg("random").Doc(`Bind each tunnel port to a random port on the host.`),
			),

		dagql.NodeFunc("up", s.containerUp).
			View(AfterVersion("v0.15.2")).
			DoNotCache("Starts a host tunnel, possibly with ports that change each time it's started.").
			Doc(`Starts a Service and creates a tunnel that forwards traffic from the caller's network to that service.`,
				`Be sure to set any exposed ports before calling this api.`).
			Args(
				dagql.Arg("random").Doc(`Bind each tunnel port to a random port on the host.`),
				dagql.Arg("ports").Doc(`List of frontend/backend port mappings to forward.`,
					`Frontend is the port accepting traffic on the host, backend is the service port.`),
				dagql.Arg("args").Doc(
					`Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).`,
					`If empty, the container's default command is used.`),
				dagql.Arg("useEntrypoint").Doc(
					`If the container has an entrypoint, prepend it to the args.`),
				dagql.Arg("experimentalPrivilegedNesting").Doc(
					`Provides Dagger access to the executed command.`),
				dagql.Arg("insecureRootCapabilities").Doc(
					`Execute the command with all root capabilities. This is similar to
					running a command with "sudo" or executing "docker run" with the
					"--privileged" flag. Containerization does not provide any security
					guarantees when using this option. It should only be used when
					absolutely necessary and only with trusted commands.`),
				dagql.Arg("expand").Doc(
					`Replace "${VAR}" or "$VAR" in the args according to the current `+
						`environment variables defined in the container (e.g. "/$VAR/foo").`),
				dagql.Arg("noInit").Doc(
					`If set, skip the automatic init process injected into containers by default.`,
					`This should only be used if the user requires that their exec process be the
					pid 1 process in the container. Otherwise it may result in unexpected behavior.`,
				),
			),
	}.Install(s.srv)

	dagql.Fields[*core.Service]{
		dagql.NodeFunc("hostname", s.hostname).
			Doc(`Retrieves a hostname which can be used by clients to reach this container.`),

		dagql.NodeFunc("withHostname", s.withHostname).
			Doc(`Configures a hostname which can be used by clients within the session to reach this container.`).
			Args(
				dagql.Arg("hostname").Doc(`The hostname to use.`),
			),

		dagql.NodeFunc("ports", s.ports).
			DoNotCache("A tunnel service's ports can change each time it is restarted.").
			Doc(`Retrieves the list of ports provided by the service.`),

		dagql.NodeFunc("endpoint", s.endpoint).
			DoNotCache("A tunnel service's endpoint can change if tunnel service is restarted.").
			Doc(`Retrieves an endpoint that clients can use to reach this container.`,
				`If no port is specified, the first exposed port is used. If none exist an error is returned.`,
				`If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.`).
			Args(
				dagql.Arg("port").Doc(`The exposed port number for the endpoint`),
				dagql.Arg("scheme").Doc(`Return a URL with the given scheme, eg. http for http://`),
			),

		dagql.NodeFunc("start", s.start).
			DoNotCache("Imperatively mutates runtime state.").
			Doc(`Start the service and wait for its health checks to succeed.`,
				`Services bound to a Container do not need to be manually started.`),

		dagql.NodeFunc("up", s.up).
			DoNotCache("Starts a host tunnel, possibly with ports that change each time it's started.").
			Doc(`Creates a tunnel that forwards traffic from the caller's network to this service.`).
			Args(
				dagql.Arg("ports").Doc(`List of frontend/backend port mappings to forward.`,
					`Frontend is the port accepting traffic on the host, backend is the service port.`),
				dagql.Arg("random").Doc(`Bind each tunnel port to a random port on the host.`),
			),

		dagql.NodeFunc("stop", s.stop).
			DoNotCache("Imperatively mutates runtime state.").
			Doc(`Stop the service.`).
			Args(
				dagql.Arg("kill").Doc(`Immediately kill the service without waiting for a graceful exit`),
			),
	}.Install(s.srv)
}

func (s *serviceSchema) containerAsServiceLegacy(ctx context.Context, parent dagql.Instance[*core.Container], args struct{}) (inst dagql.Instance[*core.Service], _ error) {
	var ctr dagql.Instance[*core.Container]
	if parent.Self.Meta == nil {
		if err := s.srv.Select(ctx, parent, &ctr, dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name:  "args",
					Value: dagql.ArrayInput[dagql.String]{},
				},
				{
					Name:  "useEntrypoint",
					Value: dagql.NewBoolean(true),
				},
			},
		}); err != nil {
			return inst, err
		}
	}

	svc := &core.Service{
		Creator:   trace.SpanContextFromContext(ctx),
		Container: ctr.Self,
	}
	return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, svc)
}

func (s *serviceSchema) containerAsService(ctx context.Context, parent *core.Container, args core.ContainerAsServiceArgs) (*core.Service, error) {
	expandedArgs := make([]string, len(args.Args))
	for i, arg := range args.Args {
		expandedArg, err := expandEnvVar(ctx, parent, arg, args.Expand)
		if err != nil {
			return nil, err
		}

		expandedArgs[i] = expandedArg
	}
	args.Args = expandedArgs

	return parent.AsService(ctx, args)
}

func (s *serviceSchema) containerUp(ctx context.Context, ctr dagql.Instance[*core.Container], args struct {
	UpArgs
	core.ContainerAsServiceArgs
}) (dagql.Nullable[core.Void], error) {
	void := dagql.Null[core.Void]()

	var inputs []dagql.NamedInput
	if args.Args != nil {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "args",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(args.Args...)),
		})
	}
	if args.UseEntrypoint {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "useEntrypoint",
			Value: dagql.Boolean(true),
		})
	}
	if args.ExperimentalPrivilegedNesting {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "experimentalPrivilegedNesting",
			Value: dagql.Boolean(true),
		})
	}
	if args.InsecureRootCapabilities {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "insecureRootCapabilities",
			Value: dagql.Boolean(true),
		})
	}
	if args.Expand {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "expand",
			Value: dagql.Boolean(true),
		})
	}
	if args.NoInit {
		inputs = append(inputs, dagql.NamedInput{
			Name:  "noInit",
			Value: dagql.Boolean(true),
		})
	}

	var svc dagql.Instance[*core.Service]
	err := s.srv.Select(ctx, ctr, &svc,
		dagql.Selector{
			Field: "asService",
			View:  dagql.View(dagql.CurrentID(ctx).View()),
			Args:  inputs,
		},
	)
	if err != nil {
		return void, err
	}

	return s.up(ctx, svc, args.UpArgs)
}

func (s *serviceSchema) containerUpLegacy(ctx context.Context, ctr dagql.Instance[*core.Container], args UpArgs) (dagql.Nullable[core.Void], error) {
	void := dagql.Null[core.Void]()

	var svc dagql.Instance[*core.Service]
	err := s.srv.Select(ctx, ctr, &svc,
		dagql.Selector{
			Field: "asService",
			View:  dagql.View(dagql.CurrentID(ctx).View()),
		},
	)
	if err != nil {
		return void, err
	}
	return s.up(ctx, svc, args)
}

func (s *serviceSchema) hostname(ctx context.Context, parent dagql.Instance[*core.Service], args struct{}) (dagql.String, error) {
	hn, err := parent.Self.Hostname(ctx, parent.ID())
	if err != nil {
		return "", err
	}
	return dagql.NewString(hn), nil
}

func (s *serviceSchema) withHostname(ctx context.Context, parent dagql.Instance[*core.Service], args struct {
	Hostname string
}) (*core.Service, error) {
	return parent.Self.WithHostname(args.Hostname), nil
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

type UpArgs struct {
	Ports  []dagql.InputObject[core.PortForward] `default:"[]"`
	Random bool                                  `default:"false"`
}

const InstrumentationLibrary = "dagger.io/engine.schema"

func (s *serviceSchema) up(ctx context.Context, svc dagql.Instance[*core.Service], args UpArgs) (dagql.Nullable[core.Void], error) {
	void := dagql.Null[core.Void]()

	useNative := !args.Random && len(args.Ports) == 0

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
				{Name: "native", Value: dagql.Boolean(useNative)},
			},
		},
	)
	if err != nil {
		return void, fmt.Errorf("failed to select host service: %w", err)
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return void, err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return void, fmt.Errorf("failed to get host services: %w", err)
	}
	runningSvc, err := svcs.Start(ctx, hostSvc.ID(), hostSvc.Self, true)
	if err != nil {
		return void, fmt.Errorf("failed to start host service: %w", err)
	}

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	for _, port := range runningSvc.Ports {
		slog.Info(
			"tunnel started",
			"port", port.Port,
			"protocol", port.Protocol.Network(),
			"http_url", fmt.Sprintf("http://%s:%d", "localhost", port.Port),
			"description", *port.Description,
		)
	}

	// wait for the request to be canceled
	<-ctx.Done()

	return void, nil
}
