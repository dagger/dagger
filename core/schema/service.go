package schema

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

type serviceSchema struct{}

var _ SchemaResolvers = &serviceSchema{}

func (s *serviceSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Container]{
		dagql.NodeFunc("asService", s.containerAsServiceLegacy).
			View(BeforeVersion("v0.15.0")).
			Doc(`Turn the container into a Service.`,
				`Be sure to set any exposed ports before this conversion.`),

		dagql.NodeFunc("asService", s.containerAsService).
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
	}.Install(srv)

	dagql.Fields[*core.Service]{
		Syncer[*core.Service]().
			Doc(`Forces evaluation of the pipeline in the engine.`),

		dagql.NodeFunc("hostname", s.hostname).
			Doc(`Retrieves a hostname which can be used by clients to reach this container.`),

		dagql.Func("withHostname", s.withHostname).
			Doc(`Configures a hostname which can be used by clients within the session to reach this container.`).
			Args(
				dagql.Arg("hostname").Doc(`The hostname to use.`),
			),

		dagql.NodeFunc("ports", s.ports).
			WithInput(dagql.PerCallInput).
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

		dagql.NodeFunc("terminal", s.terminal).
			DoNotCache("Imperatively mutates runtime state."),
	}.Install(srv)
}

func (s *serviceSchema) containerAsServiceLegacy(ctx context.Context, parent dagql.ObjectResult[*core.Container], _ struct{}) (inst dagql.ObjectResult[*core.Service], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	var cur dagql.AnyObjectResult = parent
	var withExecCall *dagql.ResultCall
	var postExecCalls []*dagql.ResultCall
	for cur != nil {
		call, err := cur.ResultCall()
		if err != nil {
			return inst, err
		}
		if call.Field == "withExec" {
			withExecCall = call
			break
		}
		postExecCalls = append(postExecCalls, call)
		cur, err = cur.Receiver(ctx, srv)
		if err != nil {
			return inst, err
		}
	}
	if withExecCall == nil {
		// no withExec found, so just rely on the entrypoint!
		svc, err := parent.Self().AsService(ctx, parent, core.ContainerAsServiceArgs{
			UseEntrypoint: true,
		})
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentCall(ctx, srv, svc)
	}

	// load the withExec receiver and use it as the base container, then replay
	// any later container-returning selectors on top so the final service keeps
	// post-withExec mutations such as WithExposedPort.
	receiver, err := cur.Receiver(ctx, srv)
	if err != nil {
		return inst, err
	}
	if receiver == nil {
		return inst, fmt.Errorf("withExec receiver is nil")
	}
	ctr, ok := receiver.(dagql.ObjectResult[*core.Container])
	if !ok {
		return inst, fmt.Errorf("expected %T, but got %T", ctr, receiver)
	}

	// extract the withExec args
	withExecField, ok := ctr.ObjectType().FieldSpec(withExecCall.Field, withExecCall.View)
	if !ok {
		return inst, fmt.Errorf("could not find %s on %s", withExecCall.Field, ctr.Type().NamedType)
	}
	inputs, err := withExecField.Args.InputsFromResultCallArgs(ctx, withExecCall.Args, withExecCall.View)
	if err != nil {
		return inst, err
	}
	var withExecArgs containerExecArgs
	err = withExecField.Args.Decode(inputs, &withExecArgs, withExecCall.View)
	if err != nil {
		return inst, err
	}

	rebuilt := ctr
	for i := len(postExecCalls) - 1; i >= 0; i-- {
		call := postExecCalls[i]
		field, ok := rebuilt.ObjectType().FieldSpec(call.Field, call.View)
		if !ok {
			return inst, fmt.Errorf("could not find %s on %s", call.Field, rebuilt.Type().NamedType)
		}
		inputs, err := field.Args.InputsFromResultCallArgs(ctx, call.Args, call.View)
		if err != nil {
			return inst, err
		}
		selectorArgs := make([]dagql.NamedInput, 0, len(inputs))
		for _, argSpec := range field.Args.Inputs(call.View) {
			input, ok := inputs[argSpec.Name]
			if !ok {
				continue
			}
			selectorArgs = append(selectorArgs, dagql.NamedInput{
				Name:  argSpec.Name,
				Value: input,
			})
		}
		var next dagql.ObjectResult[*core.Container]
		if err := srv.Select(ctx, rebuilt, &next, dagql.Selector{
			Field: call.Field,
			Args:  selectorArgs,
			Nth:   int(call.Nth),
			View:  call.View,
		}); err != nil {
			return inst, err
		}
		rebuilt = next
	}

	// create a service based on that withExec, but run it against the rebuilt
	// container state after replaying the post-withExec container mutations.
	svc, err := rebuilt.Self().AsService(ctx, rebuilt, core.ContainerAsServiceArgs{
		Args:                          withExecArgs.Args,
		UseEntrypoint:                 withExecArgs.UseEntrypoint,
		ExperimentalPrivilegedNesting: withExecArgs.ExperimentalPrivilegedNesting,
		InsecureRootCapabilities:      withExecArgs.InsecureRootCapabilities,
		NoInit:                        withExecArgs.NoInit,
	})
	if err != nil {
		return inst, err
	}
	if withExecArgs.ExecMD.Self != nil {
		svc.ExecMD = withExecArgs.ExecMD.Self
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, svc)
}

func (s *serviceSchema) containerAsService(ctx context.Context, parent dagql.ObjectResult[*core.Container], args core.ContainerAsServiceArgs) (*core.Service, error) {
	expandedArgs := make([]string, len(args.Args))
	for i, arg := range args.Args {
		expandedArg, err := expandEnvVar(ctx, parent.Self(), arg, args.Expand)
		if err != nil {
			return nil, err
		}

		expandedArgs[i] = expandedArg
	}
	args.Args = expandedArgs

	return parent.Self().AsService(ctx, parent, args)
}

func (s *serviceSchema) containerUp(ctx context.Context, ctr dagql.ObjectResult[*core.Container], args struct {
	UpArgs
	core.ContainerAsServiceArgs
}) (res dagql.Nullable[core.Void], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get Dagger server: %w", err)
	}

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

	var svc dagql.ObjectResult[*core.Service]
	curCall := dagql.CurrentCall(ctx)
	if curCall == nil {
		return res, fmt.Errorf("current call is nil")
	}
	err = srv.Select(ctx, ctr, &svc,
		dagql.Selector{
			Field: "asService",
			View:  curCall.View,
			Args:  inputs,
		},
	)
	if err != nil {
		return res, err
	}

	return s.up(ctx, svc, args.UpArgs)
}

func (s *serviceSchema) containerUpLegacy(ctx context.Context, ctr dagql.ObjectResult[*core.Container], args UpArgs) (res dagql.Nullable[core.Void], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get Dagger server: %w", err)
	}

	var svc dagql.ObjectResult[*core.Service]
	curCall := dagql.CurrentCall(ctx)
	if curCall == nil {
		return res, fmt.Errorf("current call is nil")
	}
	err = srv.Select(ctx, ctr, &svc,
		dagql.Selector{
			Field: "asService",
			View:  curCall.View,
		},
	)
	if err != nil {
		return res, err
	}
	return s.up(ctx, svc, args)
}

func (s *serviceSchema) hostname(ctx context.Context, parent dagql.ObjectResult[*core.Service], args struct{}) (res dagql.Result[dagql.String], _ error) {
	parentDig, err := parent.ContentPreferredDigest(ctx)
	if err != nil {
		return res, fmt.Errorf("service digest: %w", err)
	}
	hn, err := parent.Self().Hostname(ctx, parentDig)
	if err != nil {
		return res, err
	}
	str := dagql.NewString(hn)
	return dagql.NewResultForCurrentCall(ctx, str)
}

func (s *serviceSchema) withHostname(ctx context.Context, parent *core.Service, args struct {
	Hostname string
}) (*core.Service, error) {
	return parent.WithHostname(args.Hostname), nil
}

func (s *serviceSchema) ports(ctx context.Context, parent dagql.ObjectResult[*core.Service], args struct{}) (res dagql.Result[dagql.Array[core.Port]], _ error) {
	parentDig, err := parent.ContentPreferredDigest(ctx)
	if err != nil {
		return res, fmt.Errorf("service digest: %w", err)
	}
	ports, err := parent.Self().Ports(ctx, parentDig)
	if err != nil {
		return res, fmt.Errorf("failed to get service ports: %w", err)
	}
	return dagql.NewResultForCurrentCall(ctx, dagql.Array[core.Port](ports))
}

type serviceEndpointArgs struct {
	Port   dagql.Optional[dagql.Int]
	Scheme string `default:""`
}

func (s *serviceSchema) endpoint(ctx context.Context, parent dagql.ObjectResult[*core.Service], args serviceEndpointArgs) (res dagql.Result[dagql.String], _ error) {
	parentDig, err := parent.ContentPreferredDigest(ctx)
	if err != nil {
		return res, fmt.Errorf("service digest: %w", err)
	}
	str, err := parent.Self().Endpoint(ctx, parentDig, args.Port.Value.Int(), args.Scheme)
	if err != nil {
		return res, err
	}
	return dagql.NewResultForCurrentCall(ctx, dagql.NewString(str))
}

func (s *serviceSchema) start(ctx context.Context, parent dagql.ObjectResult[*core.Service], args struct{}) (res dagql.Result[core.ServiceID], _ error) {
	defer func() {
		if err := recover(); err != nil {
			debug.PrintStack()
			panic(err)
		}
	}()

	parentDig, err := parent.ContentPreferredDigest(ctx)
	if err != nil {
		return res, fmt.Errorf("service digest: %w", err)
	}
	if err := parent.Self().StartAndTrack(ctx, parentDig); err != nil {
		return res, err
	}

	parentID, err := parent.ID()
	if err != nil {
		return res, fmt.Errorf("service ID: %w", err)
	}
	id := dagql.NewID[*core.Service](parentID)
	return dagql.NewResultForCurrentCall(ctx, id)
}

type serviceStopArgs struct {
	Kill bool `default:"false"`
}

func (s *serviceSchema) stop(ctx context.Context, parent dagql.ObjectResult[*core.Service], args serviceStopArgs) (res dagql.Result[core.ServiceID], _ error) {
	parentDig, err := parent.ContentPreferredDigest(ctx)
	if err != nil {
		return res, fmt.Errorf("service digest: %w", err)
	}
	if err := parent.Self().Stop(ctx, parentDig, args.Kill); err != nil {
		return res, err
	}
	parentID, err := parent.ID()
	if err != nil {
		return res, fmt.Errorf("service ID: %w", err)
	}
	id := dagql.NewID[*core.Service](parentID)
	return dagql.NewResultForCurrentCall(ctx, id)
}

type serviceTerminalArgs struct {
	core.ExecTerminalArgs
}

func (s *serviceSchema) terminal(ctx context.Context, parent dagql.ObjectResult[*core.Service], args serviceTerminalArgs) (res dagql.ObjectResult[*core.Service], _ error) {
	ctr := parent.Self().Container
	if ctr.Self() == nil {
		return res, fmt.Errorf("terminal not supported on non-container services")
	}

	if len(args.Cmd) == 0 {
		args.Cmd = ctr.Self().DefaultTerminalCmd.Args
	}
	if len(args.Cmd) == 0 {
		args.Cmd = []string{"sh"}
	}

	err := parent.Self().Terminal(ctx, parent, &args.ExecTerminalArgs)
	if err != nil {
		return res, err
	}

	return parent, nil
}

type UpArgs struct {
	Ports  []dagql.InputObject[core.PortForward] `default:"[]"`
	Random bool                                  `default:"false"`
}

const InstrumentationLibrary = "dagger.io/engine.schema"

func (s *serviceSchema) up(ctx context.Context, svc dagql.ObjectResult[*core.Service], args UpArgs) (res dagql.Nullable[core.Void], _ error) {
	void := dagql.Null[core.Void]()

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get Dagger server: %w", err)
	}
	svcID, err := svc.ID()
	if err != nil {
		return res, fmt.Errorf("service ID: %w", err)
	}

	useNative := !args.Random && len(args.Ports) == 0

	var hostSvc dagql.Result[*core.Service]
	err = srv.Select(ctx, srv.Root(), &hostSvc,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "tunnel",
			Args: []dagql.NamedInput{
				{Name: "service", Value: dagql.NewID[*core.Service](svcID)},
				{Name: "ports", Value: dagql.ArrayInput[dagql.InputObject[core.PortForward]](args.Ports)},
				{Name: "native", Value: dagql.Boolean(useNative)},
			},
		},
	)
	if err != nil {
		return res, fmt.Errorf("failed to select host service: %w", err)
	}
	hostSvcDig, err := hostSvc.ContentPreferredDigest(ctx)
	if err != nil {
		return res, fmt.Errorf("host service digest: %w", err)
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return res, err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get host services: %w", err)
	}
	runningSvc, err := svcs.Start(ctx, hostSvcDig, hostSvc.Self(), true)
	if err != nil {
		return res, fmt.Errorf("failed to start host service: %w", err)
	}

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	for _, port := range runningSvc.Ports {
		httpKey, httpMsg := "http_url", "http://%s:%d"
		if port.Port == 443 {
			httpKey, httpMsg = "https_url", "https://%s:%d"
		}
		slog.Info(
			"tunnel started",
			"port", port.Port,
			"protocol", port.Protocol.Network(),
			httpKey, fmt.Sprintf(httpMsg, "localhost", port.Port),
			"description", *port.Description,
		)
	}

	// wait for the request to be canceled
	<-ctx.Done()

	return void, nil
}
