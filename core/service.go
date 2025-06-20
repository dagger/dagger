package core

import (
	"cmp"
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

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	gwpb "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/solver/pb"
	utilsystem "github.com/moby/buildkit/util/system"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
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

	// A custom hostname set by the user.
	CustomHostname string

	// Container is the container to run as a service.
	Container                     *Container
	Args                          []string
	ExperimentalPrivilegedNesting bool
	InsecureRootCapabilities      bool
	NoInit                        bool

	// TunnelUpstream is the service that this service is tunnelling to.
	TunnelUpstream *dagql.Instance[*Service]
	// TunnelPorts configures the port forwarding rules for the tunnel.
	TunnelPorts []PortForward

	// The sockets on the host to reverse tunnel
	HostSockets []*Socket
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
	cp.Args = slices.Clone(cp.Args)
	if cp.Container != nil {
		cp.Container = cp.Container.Clone()
	}
	if cp.TunnelUpstream != nil {
		cp.TunnelUpstream.Self = cp.TunnelUpstream.Self.Clone()
	}
	cp.TunnelPorts = slices.Clone(cp.TunnelPorts)
	cp.HostSockets = slices.Clone(cp.HostSockets)
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

	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", err
	}

	switch {
	case svc.TunnelUpstream != nil: // host=>container (127.0.0.1)
		svcs, err := query.Services(ctx)
		if err != nil {
			return "", err
		}
		upstream, err := svcs.Get(ctx, id, true)
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
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	switch {
	case svc.TunnelUpstream != nil, len(svc.HostSockets) > 0:
		svcs, err := query.Services(ctx)
		if err != nil {
			return nil, err
		}
		running, err := svcs.Get(ctx, id, svc.TunnelUpstream != nil)
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

	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", err
	}

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
		svcs, err := query.Services(ctx)
		if err != nil {
			return "", err
		}
		tunnel, err := svcs.Get(ctx, id, true)
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
			socketStore, err := query.Sockets(ctx)
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
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return err
	}
	_, err = svcs.Start(ctx, id, svc, svc.TunnelUpstream != nil)
	return err
}

func (svc *Service) Stop(ctx context.Context, id *call.ID, kill bool) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return err
	}
	return svcs.Stop(ctx, id, kill, svc.TunnelUpstream != nil)
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
		return svc.startTunnel(ctx)
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

	execMD, err := ctr.execMeta(ctx, ContainerExecOpts{
		ExperimentalPrivilegedNesting: svc.ExperimentalPrivilegedNesting,
		NoInit:                        svc.NoInit,
	}, nil)
	if err != nil {
		return nil, err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	svcs, err := query.Services(ctx)
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
	if mod, err := query.CurrentModule(ctx); err == nil && svc.CustomHostname != "" {
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

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	pbPlatform := pb.PlatformFromSpec(ctr.Platform.Spec())

	pbmounts, states, _, err := getAllContainerMounts(ctr)
	if err != nil {
		return nil, fmt.Errorf("could not get mounts: %w", err)
	}

	mountsG := pool.New().WithErrors()
	mounts := make([]buildkit.ContainerMount, 0)
	for _, pbmount := range pbmounts {
		mount := bkgw.Mount{
			Selector:  pbmount.Selector,
			Dest:      pbmount.Dest,
			ResultID:  pbmount.ResultID,
			Readonly:  pbmount.Readonly,
			MountType: pbmount.MountType,
			CacheOpt:  pbmount.CacheOpt,
			SecretOpt: pbmount.SecretOpt,
			SSHOpt:    pbmount.SSHOpt,
			// TODO(vito): why is there no TmpfsOpt? PR upstream?
			// TmpfsOpt  *TmpfsOpt   `protobuf:"bytes,19,opt,name=TmpfsOpt,proto3" json:"TmpfsOpt,omitempty"`
		}

		var st *llb.State
		if pbmount.Input != pb.Empty {
			st = &states[pbmount.Input]
		} else if pbmount.Dest == buildkit.MetaMountDestPath {
			v := MetaMountState(ctx, "")
			st = &v
		}

		if st != nil {
			def, err := st.Marshal(ctx)
			if err != nil {
				return nil, fmt.Errorf("marshal mount %s: %w", pbmount.Dest, err)
			}

			if def != nil {
				mountsG.Go(func() error {
					res, err := bk.Solve(ctx, bkgw.SolveRequest{
						Definition: def.ToPB(),
						Evaluate:   true,
					})
					if err != nil {
						return fmt.Errorf("solve mount %s: %w", pbmount.Dest, err)
					}
					mount.Ref = res.Ref
					return nil
				})
			}
		}

		mounts = append(mounts, buildkit.ContainerMount{
			Mount: &mount,
		})
	}
	if err := mountsG.Wait(); err != nil {
		return nil, err
	}

	execCtx := trace.ContextWithSpanContext(ctx, svc.Creator)
	ctx, span := Tracer(ctx).Start(
		// The parent is the call site that triggered it to start.
		ctx,
		// Match naming scheme of normal exec span.
		fmt.Sprintf("exec %s", strings.Join(svc.Args, " ")),
		// This span continues the original withExec, by linking to it.
		telemetry.Resume(execCtx),
		// Hide this span so the user can just focus on the withExec.
		telemetry.Internal(),
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

	env := slices.Clone(ctr.Config.Env)
	env = append(env, telemetry.PropagationEnv(ctx)...)
	addDefaultEnvvar(env, "PATH", utilsystem.DefaultPathEnv(svc.Container.Platform.OS))

	var stdinCtr, stdoutClient, stderrClient io.ReadCloser
	var stdinClient, stdoutCtr, stderrCtr io.WriteCloser

	// capture stdout/stderr while the service is starting so we can include it in
	// the exec error
	stdoutBuf := new(strings.Builder)
	stderrBuf := new(strings.Builder)

	if forwardStdin != nil {
		stdinCtr, stdinClient = io.Pipe()
	}

	// buffer stdout/stderr so we can return a nice error
	outBufWC := discardOnClose(stdoutBuf)
	errBufWC := discardOnClose(stderrBuf)
	// stop buffering service logs once it's started
	defer outBufWC.Close()
	defer errBufWC.Close()

	stdoutWriters := multiWriteCloser{outBufWC}
	stderrWriters := multiWriteCloser{errBufWC}

	if forwardStdout != nil {
		stdoutClient, stdoutCtr = io.Pipe()
		stdoutWriters = append(stdoutWriters, stdoutCtr)
	}
	if forwardStderr != nil {
		stderrClient, stderrCtr = io.Pipe()
		stderrWriters = append(stderrWriters, stderrCtr)
	}

	req := bkgw.StartRequest{
		Args:      svc.Args,
		Env:       env,
		Cwd:       cmp.Or(ctr.Config.WorkingDir, "/"),
		User:      ctr.Config.User,
		SecretEnv: ctr.secretEnvs(),
		Tty:       interactive,
		Stdin:     stdinCtr,
		Stdout:    stdoutWriters,
		Stderr:    stderrWriters,
	}
	if svc.InsecureRootCapabilities {
		req.SecurityMode = pb.SecurityMode_INSECURE
	}
	svcProc, err := gc.Start(execCtx, req)
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
			Stop:    stopSvc,
			Wait:    waitSvc,
		}, nil
	case <-exited:
		if exitErr != nil {
			var gwErr *gwpb.ExitError
			if errors.As(exitErr, &gwErr) {
				// Create ExecError with available service information
				return nil, &buildkit.ExecError{
					Err:      gwErr,
					Origin:   svc.Creator,
					Cmd:      req.Args,
					ExitCode: int(gwErr.ExitCode),
					Stdout:   stdoutBuf.String(),
					Stderr:   stderrBuf.String(),
				}
			}
			return nil, exitErr
		}
		return nil, fmt.Errorf("service exited before healthcheck")
	}
}

func discardOnClose(w io.Writer) io.WriteCloser {
	return &discardWriteCloser{w: w}
}

type discardWriteCloser struct {
	w      io.Writer
	closed bool
}

func (d *discardWriteCloser) Write(p []byte) (n int, err error) {
	if d.closed {
		return 0, nil
	}
	return d.w.Write(p)
}

func (d *discardWriteCloser) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	return nil
}

type multiWriteCloser []io.WriteCloser

func (mwc multiWriteCloser) Write(p []byte) (int, error) {
	var errs error
	for _, wc := range mwc {
		_, err := wc.Write(p)
		if err != nil {
			errs = errors.Join(errs, err)
		}
	}
	if errs != nil {
		return 0, errs
	}
	return len(p), nil
}

func (mwc multiWriteCloser) Close() error {
	var errs error
	for _, wc := range mwc {
		errs = errors.Join(errs, wc.Close())
	}
	return errs
}

func (svc *Service) startTunnel(ctx context.Context) (running *RunningService, rerr error) {
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

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	upstream, err := svcs.Start(svcCtx, svc.TunnelUpstream.ID(), svc.TunnelUpstream.Self, true)
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

	return &RunningService{
		Service: svc,
		Host:    dialHost,
		Ports:   ports,
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
	host, err := svc.Hostname(ctx, id)
	if err != nil {
		return nil, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	fullHost := host + "." + network.SessionDomain(clientMetadata.SessionID)

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	sockStore, err := query.Sockets(ctx)
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
			Host:    fullHost,
			Ports:   checkPorts,
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
	Service  dagql.Instance[*Service]
	Hostname string
	Aliases  AliasSet
}

type AliasSet []string

func (set AliasSet) String() string {
	if len(set) == 0 {
		return "no aliases"
	}

	return fmt.Sprintf("aliased as %s", strings.Join(set, ", "))
}

func (set AliasSet) With(alias string) AliasSet {
	if slices.Contains(set, alias) {
		return set
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
