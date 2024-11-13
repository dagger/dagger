package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
)

const (
	ShimEnableTTYEnvVar = "_DAGGER_ENABLE_TTY"
)

type Service struct {
	// The span that created the service, which future runs of the service will
	// link to.
	Creator trace.SpanContext

	Query *Query

	// A custom hostname set by the user.
	CustomHostname string

	// Container is the container to run as a service.
	Container *Container `json:"container"`

	// TunnelUpstream is the service that this service is tunnelling to.
	TunnelUpstream *dagql.Instance[*Service] `json:"upstream,omitempty"`
	// TunnelPorts configures the port forwarding rules for the tunnel.
	TunnelPorts []PortForward `json:"tunnel_ports,omitempty"`

	// The sockets on the host to reverse tunnel
	HostSockets []*Socket `json:"host_sockets,omitempty"`
}

func (*Service) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Service",
		NonNull:   true,
	}
}

func (*Service) TypeDescription() string {
	return "A content-addressed service providing TCP connectivity."
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (svc *Service) Clone() *Service {
	cp := *svc
	if cp.Container != nil {
		cp.Container = cp.Container.Clone()
	}
	if cp.TunnelUpstream != nil {
		cp.TunnelUpstream.Self = cp.TunnelUpstream.Self.Clone()
	}
	cp.TunnelPorts = cloneSlice(cp.TunnelPorts)
	cp.HostSockets = cloneSlice(cp.HostSockets)
	return &cp
}

func (svc *Service) WithHostname(hostname string) *Service {
	svc = svc.Clone()
	svc.CustomHostname = hostname
	return svc
}

func (svc *Service) Hostname(ctx context.Context, id *call.ID) (string, error) {
	if svc.CustomHostname != "" {
		return svc.CustomHostname, nil
	}
	switch {
	case svc.TunnelUpstream != nil: // host=>container (127.0.0.1)
		svcs, err := svc.Query.Services(ctx)
		if err != nil {
			return "", err
		}
		upstream, err := svcs.Get(ctx, id)
		if err != nil {
			return "", err
		}

		return upstream.Host, nil
	case svc.Container != nil, // container=>container
		len(svc.HostSockets) > 0: // container=>host
		return network.HostHash(id.Digest()), nil
	default:
		return "", errors.New("unknown service type")
	}
}

func (svc *Service) Ports(ctx context.Context, id *call.ID) ([]Port, error) {
	switch {
	case svc.TunnelUpstream != nil, len(svc.HostSockets) > 0:
		svcs, err := svc.Query.Services(ctx)
		if err != nil {
			return nil, err
		}
		running, err := svcs.Get(ctx, id)
		if err != nil {
			return nil, err
		}

		return running.Ports, nil
	case svc.Container != nil:
		return svc.Container.Ports, nil
	default:
		return nil, errors.New("unknown service type")
	}
}

func (svc *Service) Endpoint(ctx context.Context, id *call.ID, port int, scheme string) (string, error) {
	var host string
	var err error
	switch {
	case svc.Container != nil:
		host, err = svc.Hostname(ctx, id)
		if err != nil {
			return "", err
		}

		if port == 0 {
			if len(svc.Container.Ports) == 0 {
				return "", fmt.Errorf("no ports exposed")
			}

			port = svc.Container.Ports[0].Port
		}
	case svc.TunnelUpstream != nil:
		svcs, err := svc.Query.Services(ctx)
		if err != nil {
			return "", err
		}
		tunnel, err := svcs.Get(ctx, id)
		if err != nil {
			return "", err
		}

		host = tunnel.Host

		if port == 0 {
			if len(tunnel.Ports) == 0 {
				return "", fmt.Errorf("no ports")
			}

			port = tunnel.Ports[0].Port
		}
	case len(svc.HostSockets) > 0:
		host, err = svc.Hostname(ctx, id)
		if err != nil {
			return "", err
		}

		if port == 0 {
			socketStore, err := svc.Query.Sockets(ctx)
			if err != nil {
				return "", fmt.Errorf("failed to get socket store: %w", err)
			}
			portForward, ok := socketStore.GetSocketPortForward(svc.HostSockets[0].IDDigest)
			if !ok {
				return "", fmt.Errorf("socket not found: %s", svc.HostSockets[0].IDDigest)
			}
			port = portForward.FrontendOrBackendPort()
		}
	default:
		return "", fmt.Errorf("unknown service type")
	}

	endpoint := fmt.Sprintf("%s:%d", host, port)
	if scheme != "" {
		endpoint = scheme + "://" + endpoint
	}

	return endpoint, nil
}

func (svc *Service) StartAndTrack(ctx context.Context, id *call.ID) error {
	svcs, err := svc.Query.Services(ctx)
	if err != nil {
		return err
	}
	_, err = svcs.Start(ctx, id, svc)
	return err
}

func (svc *Service) Stop(ctx context.Context, id *call.ID, kill bool) error {
	svcs, err := svc.Query.Services(ctx)
	if err != nil {
		return err
	}
	return svcs.Stop(ctx, id, kill)
}

func (svc *Service) Start(
	ctx context.Context,
	id *call.ID,
	interactive bool,
	forwardStdin func(io.Writer, bkgw.ContainerProcess),
	forwardStdout func(io.Reader),
	forwardStderr func(io.Reader),
) (running *RunningService, err error) {
	switch {
	case svc.Container != nil:
		return svc.startContainer(ctx, id, interactive, forwardStdin, forwardStdout, forwardStderr)
	case svc.TunnelUpstream != nil:
		return svc.startTunnel(ctx, id)
	case len(svc.HostSockets) > 0:
		return svc.startReverseTunnel(ctx, id)
	default:
		return nil, fmt.Errorf("unknown service type")
	}
}

//nolint:gocyclo
func (svc *Service) startContainer(
	ctx context.Context,
	id *call.ID,
	interactive bool,
	forwardStdin func(io.Writer, bkgw.ContainerProcess),
	forwardStdout func(io.Reader),
	forwardStderr func(io.Reader),
) (running *RunningService, rerr error) {
	dig := id.Digest()

	slog := slog.With("service", dig.String(), "id", id.DisplaySelf())

	host, err := svc.Hostname(ctx, id)
	if err != nil {
		return nil, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	ctr := svc.Container

	dag, err := buildkit.DefToDAG(ctr.FS)
	if err != nil {
		return nil, err
	}

	if dag.GetOp() == nil && len(dag.Inputs) == 1 {
		dag = dag.Inputs[0]
	} else {
		// i mean, theoretically this should never happen, but it's better to
		// notice it
		return nil, fmt.Errorf("what in tarnation? that's too many inputs! (%d) %v", len(dag.Inputs), dag.GetInputs())
	}

	execOp, ok := dag.AsExec()
	if !ok {
		return nil, fmt.Errorf("service container must be result of withExec (expected exec op, got %T)", dag.GetOp())
	}

	execMD, ok, err := buildkit.ExecutionMetadataFromDescription(execOp.Metadata.Description)
	if err != nil {
		return nil, fmt.Errorf("parse execution metadata: %w", err)
	}
	if !ok {
		execMD = &buildkit.ExecutionMetadata{
			ExecID:    identity.NewID(),
			SessionID: clientMetadata.SessionID,
		}
	}

	svcs, err := svc.Query.Services(ctx)
	if err != nil {
		return nil, err
	}
	detachDeps, _, err := svcs.StartBindings(ctx, ctr.Services)
	if err != nil {
		return nil, fmt.Errorf("start dependent services: %w", err)
	}

	defer func() {
		if err != nil {
			detachDeps()
		}
	}()

	var domain string
	if mod, err := svc.Query.CurrentModule(ctx); err == nil && svc.CustomHostname != "" {
		domain = network.ModuleDomain(mod.InstanceID, clientMetadata.SessionID)
		if !slices.Contains(execMD.ExtraSearchDomains, domain) {
			// ensure a service can reach other services in the module that started
			// it, to support services returned by modules and re-configured with
			// local hostnames. otherwise, the service is "stuck" in the installing
			// module's domain.
			execMD.ExtraSearchDomains = append(execMD.ExtraSearchDomains, domain)
		}
	} else {
		domain = network.SessionDomain(clientMetadata.SessionID)
	}

	fullHost := host + "." + domain

	bk, err := svc.Query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	pbPlatform := pb.PlatformFromSpec(ctr.Platform.Spec())

	mountsG := pool.New().WithErrors()
	mounts := make([]buildkit.ContainerMount, len(execOp.Mounts))
	for i, m := range execOp.Mounts {
		mount := bkgw.Mount{
			Selector:  m.Selector,
			Dest:      m.Dest,
			ResultID:  m.ResultID,
			Readonly:  m.Readonly,
			MountType: m.MountType,
			CacheOpt:  m.CacheOpt,
			SecretOpt: m.SecretOpt,
			SSHOpt:    m.SSHOpt,
			// TODO(vito): why is there no TmpfsOpt? PR upstream?
			// TmpfsOpt  *TmpfsOpt   `protobuf:"bytes,19,opt,name=TmpfsOpt,proto3" json:"TmpfsOpt,omitempty"`
		}

		if m.Input > -1 {
			input := execOp.Input(m.Input)
			def, err := input.Marshal()
			if err != nil {
				return nil, fmt.Errorf("marshal mount %s: %w", m.Dest, err)
			}

			mountsG.Go(func() error {
				res, err := bk.Solve(ctx, bkgw.SolveRequest{
					Definition: def,
					Evaluate:   true,
				})
				if err != nil {
					return fmt.Errorf("solve mount %s: %w", m.Dest, err)
				}
				mount.Ref = res.Ref
				return nil
			})
		}

		mounts[i] = buildkit.ContainerMount{
			Mount: &mount,
		}
	}
	if err := mountsG.Wait(); err != nil {
		return nil, err
	}

	execCtx := trace.ContextWithSpanContext(ctx, svc.Creator)
	ctx, span := Tracer(ctx).Start(
		// The parent is the call site that triggered it to start.
		ctx,
		// Match naming scheme of normal exec span.
		fmt.Sprintf("exec %s", strings.Join(execOp.Meta.Args, " ")),
		// This span continues the original withExec, by linking to it.
		telemetry.Resume(execCtx),
		// Hide this span so the user can just focus on the withExec.
		telemetry.Internal(),
		// The withExec span expects to see this effect, otherwise it'll still be
		// pending.
		trace.WithAttributes(attribute.String(telemetry.DagDigestAttr, execOp.OpDigest.String())),
		trace.WithAttributes(attribute.String(telemetry.EffectIDAttr, execOp.OpDigest.String())),
	)
	defer func() {
		if rerr != nil {
			// NB: this is intentionally conditional; we only complete if there was
			// an error starting. span.End is called when the service exits.
			telemetry.End(span, func() error { return rerr })
		}
	}()

	gc, err := bk.NewContainer(execCtx, buildkit.NewContainerRequest{
		Mounts:            mounts,
		Hostname:          fullHost,
		Platform:          &pbPlatform,
		ExecutionMetadata: *execMD,
	})
	if err != nil {
		return nil, fmt.Errorf("new container: %w", err)
	}

	defer func() {
		if err != nil {
			gc.Release(context.WithoutCancel(ctx))
		}
	}()

	checked := make(chan error, 1)
	go func() {
		checked <- newHealth(bk, gc, fullHost, ctr.Ports).Check(ctx)
	}()

	env := append([]string{}, execOp.Meta.Env...)
	env = append(env, telemetry.PropagationEnv(ctx)...)

	var stdinCtr, stdoutClient, stderrClient io.ReadCloser
	var stdinClient, stdoutCtr, stderrCtr io.WriteCloser
	if forwardStdin != nil {
		stdinCtr, stdinClient = io.Pipe()
	}

	if forwardStdout != nil {
		stdoutClient, stdoutCtr = io.Pipe()
	}

	if forwardStderr != nil {
		stderrClient, stderrCtr = io.Pipe()
	}

	svcProc, err := gc.Start(execCtx, bkgw.StartRequest{
		Args:         execOp.Meta.Args,
		Env:          env,
		Cwd:          execOp.Meta.Cwd,
		User:         execOp.Meta.User,
		SecretEnv:    execOp.Secretenv,
		Tty:          interactive,
		Stdin:        stdinCtr,
		Stdout:       stdoutCtr,
		Stderr:       stderrCtr,
		SecurityMode: execOp.Security,
	})
	if err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}

	if forwardStdin != nil {
		forwardStdin(stdinClient, svcProc)
	}
	if forwardStdout != nil {
		forwardStdout(stdoutClient)
	}
	if forwardStderr != nil {
		forwardStderr(stderrClient)
	}

	var stopped atomic.Bool

	var exitErr error
	exited := make(chan struct{})
	go func() {
		defer func() {
			if stdinClient != nil {
				stdinClient.Close()
			}
			if stdoutClient != nil {
				stdoutClient.Close()
			}
			if stderrClient != nil {
				stderrClient.Close()
			}
			close(exited)
		}()

		exitErr = svcProc.Wait()
		slog.Info("service exited", "err", exitErr)

		// show the exit status; doing so won't fail anything, and is
		// helpful for troubleshooting
		defer telemetry.End(span, func() error {
			if stopped.Load() {
				// stopped; we don't care about the exit result (likely 137)
				return nil
			}
			return exitErr
		})

		// detach dependent services when process exits
		detachDeps()

		// release container
		if err := gc.Release(ctx); exitErr == nil && err != nil {
			if !errors.Is(err, context.Canceled) {
				exitErr = fmt.Errorf("release: %w", err)
			}
		}
	}()

	stopSvc := func(ctx context.Context, force bool) error {
		stopped.Store(true)
		sig := syscall.SIGTERM
		if force {
			sig = syscall.SIGKILL
		}
		if err := svcProc.Signal(ctx, sig); err != nil {
			return fmt.Errorf("signal: %w", err)
		}
		select {
		case <-ctx.Done():
			slog.Info("service stop interrupted", "err", ctx.Err())
			return ctx.Err()
		case exitErr := <-exited:
			slog.Info("service exited in stop", "err", exitErr)
			return nil
		}
	}

	waitSvc := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return exitErr
		}
	}

	select {
	case err := <-checked:
		if err != nil {
			return nil, fmt.Errorf("health check errored: %w", err)
		}

		return &RunningService{
			Service: svc,
			Host:    fullHost,
			Ports:   ctr.Ports,
			Key: ServiceKey{
				Digest:    dig,
				SessionID: clientMetadata.SessionID,
			},
			Stop: stopSvc,
			Wait: waitSvc,
		}, nil
	case <-exited:
		if exitErr != nil {
			return nil, fmt.Errorf("exited: %w", exitErr)
		}
		return nil, fmt.Errorf("service exited before healthcheck")
	}
}

func (svc *Service) startTunnel(ctx context.Context, id *call.ID) (running *RunningService, rerr error) {
	svcCtx, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	defer func() {
		if rerr != nil {
			stop(fmt.Errorf("tunnel start error: %w", rerr))
		}
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	svcCtx = engine.ContextWithClientMetadata(svcCtx, clientMetadata)

	svcs, err := svc.Query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	bk, err := svc.Query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	upstream, err := svcs.Start(svcCtx, svc.TunnelUpstream.ID(), svc.TunnelUpstream.Self)
	if err != nil {
		return nil, fmt.Errorf("start upstream: %w", err)
	}

	closers := make([]func() error, len(svc.TunnelPorts))
	ports := make([]Port, len(svc.TunnelPorts))

	// TODO: make these configurable?
	const bindHost = "0.0.0.0"
	const dialHost = "127.0.0.1"

	for i, forward := range svc.TunnelPorts {
		var frontend int
		if forward.Frontend != nil {
			frontend = *forward.Frontend
		} else {
			frontend = 0 // allow OS to choose
		}
		res, closeListener, err := bk.ListenHostToContainer(
			svcCtx,
			fmt.Sprintf("%s:%d", bindHost, frontend),
			forward.Protocol.Network(),
			fmt.Sprintf("%s:%d", upstream.Host, forward.Backend),
		)
		if err != nil {
			return nil, fmt.Errorf("host to container: %w", err)
		}

		_, portStr, err := net.SplitHostPort(res.GetAddr())
		if err != nil {
			return nil, fmt.Errorf("split host port: %w", err)
		}

		frontend, err = strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("parse port: %w", err)
		}

		desc := fmt.Sprintf("tunnel %s:%d -> %s:%d", bindHost, frontend, upstream.Host, forward.Backend)

		ports[i] = Port{
			Port:        frontend,
			Protocol:    forward.Protocol,
			Description: &desc,
		}

		closers[i] = closeListener
	}

	dig := id.Digest()

	return &RunningService{
		Service: svc,
		Key: ServiceKey{
			Digest:    dig,
			SessionID: clientMetadata.SessionID,
		},
		Host:  dialHost,
		Ports: ports,
		Stop: func(_ context.Context, _ bool) error {
			stop(errors.New("service stop called"))
			svcs.Detach(svcCtx, upstream)
			var errs []error
			for _, closeListener := range closers {
				errs = append(errs, closeListener())
			}
			return errors.Join(errs...)
		},
	}, nil
}

func (svc *Service) startReverseTunnel(ctx context.Context, id *call.ID) (running *RunningService, rerr error) {
	dig := id.Digest()

	host, err := svc.Hostname(ctx, id)
	if err != nil {
		return nil, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	fullHost := host + "." + network.SessionDomain(clientMetadata.SessionID)

	bk, err := svc.Query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	sockStore, err := svc.Query.Sockets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get socket store: %w", err)
	}

	// we don't need a full container, just a CNI provisioned network namespace to listen in
	netNS, err := bk.NewNetworkNamespace(ctx, fullHost)
	if err != nil {
		return nil, fmt.Errorf("new network namespace: %w", err)
	}

	checkPorts := []Port{}
	descs := make([]string, 0, len(svc.HostSockets))
	for _, sock := range svc.HostSockets {
		port, ok := sockStore.GetSocketPortForward(sock.IDDigest)
		if !ok {
			return nil, fmt.Errorf("socket not found: %s", sock.IDDigest)
		}
		desc := fmt.Sprintf("tunnel %s %d -> %d", port.Protocol, port.FrontendOrBackendPort(), port.Backend)
		descs = append(descs, desc)
		checkPorts = append(checkPorts, Port{
			Port:        port.FrontendOrBackendPort(),
			Protocol:    port.Protocol,
			Description: &desc,
		})
	}

	ctx, span := Tracer(ctx).Start(ctx, strings.Join(descs, ", "), trace.WithLinks(
		trace.Link{SpanContext: svc.Creator},
	))
	defer func() {
		if rerr != nil {
			// NB: this is intentionally conditional; we only complete if there was
			// an error starting. span.End is called when the service exits.
			telemetry.End(span, func() error { return rerr })
		}
	}()

	tunnel := &c2hTunnel{
		bk:        bk,
		ns:        netNS,
		socks:     svc.HostSockets,
		sockStore: sockStore,
	}

	// NB: decouple from the incoming ctx cancel and add our own
	svcCtx, stop := context.WithCancelCause(context.WithoutCancel(ctx))

	exited := make(chan struct{}, 1)
	var exitErr error
	go func() {
		defer close(exited)
		exitErr = tunnel.Tunnel(svcCtx)
	}()

	checked := make(chan error, 1)
	go func() {
		checked <- newHealth(bk, netNS, fullHost, checkPorts).Check(svcCtx)
	}()

	select {
	case err := <-checked:
		if err != nil {
			netNS.Release(svcCtx)
			err = fmt.Errorf("health check errored: %w", err)
			stop(err)
			return nil, err
		}

		return &RunningService{
			Service: svc,
			Key: ServiceKey{
				Digest:    dig,
				SessionID: clientMetadata.SessionID,
			},
			Host:  fullHost,
			Ports: checkPorts,
			Stop: func(context.Context, bool) (rerr error) {
				defer telemetry.End(span, func() error { return rerr })
				stop(errors.New("service stop called"))
				waitCtx, waitCancel := context.WithTimeout(context.WithoutCancel(svcCtx), 10*time.Second)
				defer waitCancel()
				netNS.Release(waitCtx)
				select {
				case <-waitCtx.Done():
					return fmt.Errorf("timeout waiting for tunnel to stop: %w", waitCtx.Err())
				case <-exited:
					return nil
				}
			},
		}, nil
	case <-exited:
		netNS.Release(svcCtx)
		stop(errors.New("proxy exited"))
		if exitErr != nil {
			return nil, fmt.Errorf("proxy exited: %w", exitErr)
		}
		return nil, fmt.Errorf("proxy exited before healthcheck")
	}
}

type ServiceBindings []ServiceBinding

type ServiceBinding struct {
	ID       *call.ID
	Service  *Service `json:"service"`
	Hostname string   `json:"hostname"`
	Aliases  AliasSet `json:"aliases"`
}

type AliasSet []string

func (set AliasSet) String() string {
	if len(set) == 0 {
		return "no aliases"
	}

	return fmt.Sprintf("aliased as %s", strings.Join(set, ", "))
}

func (set AliasSet) With(alias string) AliasSet {
	for _, a := range set {
		if a == alias {
			return set
		}
	}
	return append(set, alias)
}

func (set AliasSet) Union(other AliasSet) AliasSet {
	for _, a := range other {
		set = set.With(a)
	}
	return set
}

func (bndp *ServiceBindings) Merge(other ServiceBindings) {
	if *bndp == nil {
		*bndp = ServiceBindings{}
	}

	merged := *bndp

	indices := map[string]int{}
	for i, b := range merged {
		indices[b.Hostname] = i
	}

	for _, bnd := range other {
		i, has := indices[bnd.Hostname]
		if !has {
			merged = append(merged, bnd)
			continue
		}

		merged[i].Aliases = merged[i].Aliases.Union(bnd.Aliases)
	}

	*bndp = merged
}
