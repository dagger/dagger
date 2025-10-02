package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	containerdfs "github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	bkcontainer "github.com/dagger/dagger/internal/buildkit/frontend/gateway/container"
	gwpb "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	"github.com/dagger/dagger/internal/buildkit/identity"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	bkmounts "github.com/dagger/dagger/internal/buildkit/solver/llbsolver/mounts"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/util/cleanups"
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
	ExecMD                        *buildkit.ExecutionMetadata
	ExecMeta                      *executor.Meta

	// TunnelUpstream is the service that this service is tunnelling to.
	TunnelUpstream dagql.ObjectResult[*Service]
	// TunnelPorts configures the port forwarding rules for the tunnel.
	TunnelPorts []PortForward

	// The sockets on the host to reverse tunnel
	HostSockets []*Socket

	// Refs to release when shutting down the service.
	Releasers []bkcache.Ref
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
	cp.TunnelPorts = slices.Clone(cp.TunnelPorts)
	cp.HostSockets = slices.Clone(cp.HostSockets)
	return &cp
}

func (svc *Service) Evaluate(ctx context.Context) (*buildkit.Result, error) {
	return nil, nil
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
	case svc.TunnelUpstream.Self() != nil: // host=>container (127.0.0.1)
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
	case svc.TunnelUpstream.Self() != nil, len(svc.HostSockets) > 0:
		svcs, err := query.Services(ctx)
		if err != nil {
			return nil, err
		}
		running, err := svcs.Get(ctx, id, svc.TunnelUpstream.Self() != nil)
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
	case svc.TunnelUpstream.Self() != nil:
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
	_, err = svcs.Start(ctx, id, svc, svc.TunnelUpstream.Self() != nil)
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
	return svcs.Stop(ctx, id, kill, svc.TunnelUpstream.Self() != nil)
}

type ServiceIO struct {
	Stdin       io.ReadCloser
	Stdout      io.WriteCloser
	Stderr      io.WriteCloser
	ResizeCh    chan bkgw.WinSize
	Interactive bool
}

func (io *ServiceIO) Close() error {
	if io == nil {
		return nil
	}

	var errs []error
	if io.Stdin != nil {
		if err := io.Stdin.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if io.Stdout != nil {
		if err := io.Stdout.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if io.Stderr != nil {
		if err := io.Stderr.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (svc *Service) Start(
	ctx context.Context,
	id *call.ID,
	sio *ServiceIO,
) (running *RunningService, err error) {
	switch {
	case svc.Container != nil:
		return svc.startContainer(ctx, id, sio)
	case svc.TunnelUpstream.Self() != nil:
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
	sio *ServiceIO,
) (running *RunningService, rerr error) {
	var cleanup cleanups.Cleanups
	defer func() {
		if rerr != nil {
			cleanup.Run()
		}
	}()

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

	execMD := svc.ExecMD
	if execMD == nil {
		execMD, err = ctr.execMeta(ctx, ContainerExecOpts{
			ExperimentalPrivilegedNesting: svc.ExperimentalPrivilegedNesting,
			NoInit:                        svc.NoInit,
		}, nil)
		if err != nil {
			return nil, err
		}
	}

	// Services support having refs re-mounted at runtime, so when the service
	// stops, we need to release them all.
	cleanup.Add("release late-bound refs", func() error {
		var errs error
		for _, ref := range svc.Releasers {
			errs = errors.Join(errs, ref.Release(context.WithoutCancel(ctx)))
		}
		return errs
	})

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
	cleanup.Add("detach deps", cleanups.Infallible(detachDeps))

	var domain string
	if mod, err := query.CurrentModule(ctx); err == nil && svc.CustomHostname != "" {
		domain = network.ModuleDomain(mod.ResultID, clientMetadata.SessionID)
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
	cache := query.BuildkitCache()
	session := query.BuildkitSession()

	pbmounts, states, _, refs, _, err := getAllContainerMounts(ctx, ctr)
	if err != nil {
		return nil, fmt.Errorf("could not get mounts: %w", err)
	}

	inputs := make([]bkcache.ImmutableRef, len(states))
	eg, egctx := errgroup.WithContext(ctx)
	for _, pbmount := range pbmounts {
		if pbmount.Input == pb.Empty {
			continue
		}

		if ref := refs[pbmount.Input]; ref != nil {
			inputs[pbmount.Input] = ref
			continue
		}

		st := states[pbmount.Input]
		def, err := st.Marshal(egctx)
		if err != nil {
			return nil, err
		}
		if def == nil {
			continue
		}

		eg.Go(func() error {
			res, err := bk.Solve(egctx, bkgw.SolveRequest{
				Evaluate:   true,
				Definition: def.ToPB(),
			})
			if err != nil {
				return err
			}
			ref, err := res.Ref.Result(egctx)
			if err != nil {
				return err
			}
			if ref != nil {
				inputs[pbmount.Input] = ref.Sys().(*worker.WorkerRef).ImmutableRef
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	workerRefs := make([]*worker.WorkerRef, 0, len(inputs))
	for _, ref := range inputs {
		workerRefs = append(workerRefs, &worker.WorkerRef{ImmutableRef: ref})
	}

	svcID := identity.NewID()

	name := fmt.Sprintf("container %s", svcID)
	mm := bkmounts.NewMountManager(name, cache, session)

	bkSessionGroup := bksession.NewGroup(bk.ID())
	p, err := bkcontainer.PrepareMounts(ctx, mm, cache, bkSessionGroup, "", pbmounts, workerRefs, func(m *pb.Mount, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
		return cache.New(ctx, ref, bkSessionGroup)
	}, runtime.GOOS)
	if err != nil {
		return nil, fmt.Errorf("prepare mounts: %w", err)
	}

	for _, active := range slices.Backward(p.Actives) { // call in LIFO order
		cleanup.Add("release active ref", func() error {
			return active.Ref.Release(context.WithoutCancel(ctx))
		})
	}
	for _, o := range p.OutputRefs {
		cleanup.Add("release output ref", func() error {
			return o.Ref.Release(context.WithoutCancel(ctx))
		})
	}

	meta := svc.ExecMeta
	if meta == nil {
		meta, err = ctr.metaSpec(ctx, ContainerExecOpts{
			Args:                          svc.Args,
			ExperimentalPrivilegedNesting: svc.ExperimentalPrivilegedNesting,
			InsecureRootCapabilities:      svc.InsecureRootCapabilities,
			NoInit:                        svc.NoInit,
		})
		if err != nil {
			return nil, err
		}
		meta.Hostname = fullHost
	}
	if sio != nil && sio.Interactive {
		meta.Tty = true
		meta.Env = addDefaultEnvvar(meta.Env, "TERM", "xterm")
	}

	ctx, span := Tracer(ctx).Start(
		// The parent is the call site that triggered it to start.
		ctx,
		// Match naming scheme of normal exec span.
		fmt.Sprintf("exec %s", strings.Join(svc.Args, " ")),
		// This span continues the original withExec, by linking to it.
		telemetry.Resume(trace.ContextWithSpanContext(ctx, svc.Creator)),
	)
	defer func() {
		if rerr != nil {
			// NB: this is intentionally conditional; we only complete if there was
			// an error starting. span.End is called when the service exits.
			telemetry.End(span, func() error { return rerr })
		}
	}()

	// capture stdout/stderr while the service is starting so we can include it in
	// the exec error
	stdoutBuf := new(strings.Builder)
	stderrBuf := new(strings.Builder)
	// buffer stdout/stderr so we can return a nice error
	outBufWC := discardOnClose(stdoutBuf)
	errBufWC := discardOnClose(stderrBuf)
	// stop buffering service logs once it's started
	defer outBufWC.Close()
	defer errBufWC.Close()

	var stdinReader io.ReadCloser
	if sio != nil && sio.Stdin != nil {
		stdinReader = sio.Stdin
	}
	stdoutWriters := multiWriteCloser{outBufWC}
	if sio != nil && sio.Stdout != nil {
		stdoutWriters = append(stdoutWriters, sio.Stdout)
	}
	stderrWriters := multiWriteCloser{errBufWC}
	if sio != nil && sio.Stderr != nil {
		stderrWriters = append(stderrWriters, sio.Stderr)
	}

	started := make(chan struct{})

	signal := make(chan syscall.Signal)
	var resize <-chan executor.WinSize
	if sio != nil {
		resize = convertResizeChannel(ctx, sio.ResizeCh)
	}

	secretEnv, err := loadSecretEnv(ctx, bksession.NewGroup(bk.ID()), bk.SessionManager, ctr.secretEnvs())
	if err != nil {
		return nil, err
	}
	meta.Env = append(meta.Env, secretEnv...)

	worker := bk.Worker.ExecWorker(svc.Creator, *execMD)
	exec := worker.Executor()
	exited := make(chan struct{})
	runErr := make(chan error)
	go func() {
		_, err := exec.Run(ctx, svcID, p.Root, p.Mounts, executor.ProcessInfo{
			Meta:   *meta,
			Stdin:  stdinReader,
			Stdout: stdoutWriters,
			Stderr: stderrWriters,
			Resize: resize,
			Signal: signal,
		}, started)
		runErr <- err
	}()
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-started:
	}

	checked := make(chan error, 1)
	go func() {
		checked <- newHealth(bk, buildkit.NewDirectNS(svcID), fullHost, ctr.Ports).Check(ctx)
	}()

	var stopped atomic.Bool

	var exitErr error
	go func() {
		defer func() {
			sio.Close()
			close(exited)
		}()

		exitErr = <-runErr
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

		// run all cleanups, discarding container
		cleanup.Run()
	}()

	signalSvc := func(ctx context.Context, sig syscall.Signal) error {
		select {
		case <-ctx.Done():
			slog.Info("service signal interrupted", "err", ctx.Err())
			return ctx.Err()
		case <-exited:
			slog.Info("service exited in signal")
		case signal <- sig:
			// close stdio, else we hang waiting on i/o piping goroutines
			sio.Close()
		}
		return nil
	}

	waitSvc := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return exitErr
		}
	}

	stopSvc := func(ctx context.Context, force bool) error {
		stopped.Store(true)
		sig := syscall.SIGTERM
		if force {
			sig = syscall.SIGKILL
		}
		err := signalSvc(ctx, sig)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			slog.Info("service stop interrupted", "err", ctx.Err())
			return ctx.Err()
		case <-exited:
			slog.Info("service exited in stop", "err", exitErr)
			return nil
		}
	}

	execSvc := func(ctx context.Context, cmd []string, env []string, sio *ServiceIO) error {
		meta := *meta
		meta.Args = cmd
		meta.Env = append(meta.Env, env...)
		if sio != nil && sio.Interactive {
			meta.Tty = true
			meta.Env = addDefaultEnvvar(meta.Env, "TERM", "xterm")
		}

		var stdinReader io.ReadCloser
		var stdoutWriter io.WriteCloser
		var stderrWriter io.WriteCloser
		var resizeCh <-chan executor.WinSize
		if sio != nil {
			stdinReader = sio.Stdin
			stdoutWriter = sio.Stdout
			stderrWriter = sio.Stderr
			resizeCh = convertResizeChannel(ctx, sio.ResizeCh)
		}
		err = exec.Exec(ctx, svcID, executor.ProcessInfo{
			Meta:   meta,
			Stdin:  stdinReader,
			Stdout: stdoutWriter,
			Stderr: stderrWriter,
			Resize: resizeCh,
		})
		return err
	}

	select {
	case err := <-checked:
		if err != nil {
			return nil, fmt.Errorf("health check errored: %w", err)
		}

		return &RunningService{
			Host:        fullHost,
			Ports:       ctr.Ports,
			Stop:        stopSvc,
			Wait:        waitSvc,
			Exec:        execSvc,
			ContainerID: svcID,
		}, nil
	case <-exited:
		if exitErr != nil {
			var gwErr *gwpb.ExitError
			if errors.As(exitErr, &gwErr) {
				// Create ExecError with available service information
				return nil, &buildkit.ExecError{
					Err:      gwErr,
					Origin:   svc.Creator,
					Cmd:      meta.Args,
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

func convertResizeChannel(ctx context.Context, in <-chan bkgw.WinSize) <-chan executor.WinSize {
	if in == nil {
		return nil
	}
	out := make(chan executor.WinSize)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case winSize := <-in:
				out <- executor.WinSize{
					Rows: winSize.Rows,
					Cols: winSize.Cols,
				}
			}
		}
	}()
	return out
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

	upstream, err := svcs.Start(svcCtx, svc.TunnelUpstream.ID(), svc.TunnelUpstream.Self(), svc.TunnelUpstream.Self().TunnelUpstream.Self() != nil)
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

// runAndSnapshotChanges mounts the given source directory into the given
// container at the given target path, runs the given function, and then
// snapshots any changes made to the directory during the function's execution.
// It returns an immutable ref to the snapshot of the changes.
//
// After the function completes, a mutable copy of the snapshot is remounted
// into the service to ensure further changes cannot be made to the
// ImmutableRef. However there is still inherently a window of time where the
// service may write asynchronously after the immutable ref is created and
// before the new mutable copy is remounted.
func (svc *Service) runAndSnapshotChanges(
	ctx context.Context,
	containerID string,
	target string,
	source *Directory,
	f func() error,
) (res dagql.ObjectResult[*Directory], hasChanges bool, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return res, false, err
	}

	ref, err := getRefOrEvaluate(ctx, source)
	if err != nil {
		return res, false, fmt.Errorf("failed to get ref for source directory: %w", err)
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return res, false, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	mutableRef, err := query.BuildkitCache().New(ctx, ref, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("mcp remount"))
	if err != nil {
		return res, false, fmt.Errorf("failed to create new ref for source directory: %w", err)
	}
	defer mutableRef.Release(ctx)

	err = MountRef(ctx, mutableRef, nil, func(root string) (rerr error) {
		resolvedDir, err := containerdfs.RootPath(root, source.Dir)
		if err != nil {
			return err
		}
		if err := mountIntoContainer(ctx, containerID, resolvedDir, target); err != nil {
			return fmt.Errorf("remount container: %w", err)
		}
		return f()
	})
	if err != nil {
		return res, false, err
	}

	usage, err := bk.Worker.Snapshotter.Usage(ctx, mutableRef.ID())
	if err != nil {
		return res, false, fmt.Errorf("failed to check for changes: %w", err)
	}
	hasChanges = usage.Inodes > 1 || usage.Size > 0
	if !hasChanges {
		slog.Debug("mcp: no changes made to directory")
		return res, false, nil
	}

	immutableRef, err := mutableRef.Commit(ctx)
	if err != nil {
		return res, false, fmt.Errorf("failed to commit remounted ref for %s: %w", target, err)
	}

	// release unconditionally here, since we Clone it using the __immutableRef
	// API call below
	defer immutableRef.Release(ctx)

	// Create a new mutable ref to leave the service with, to prevent further
	// changes from mutating the now-immutable ref
	//
	// NOTE: there's technically a race here, for sure, but we can least prevent
	// mutation outside of the bounds of this func
	abandonedRef, err := query.BuildkitCache().New(ctx, immutableRef, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("mcp remount"))
	if err != nil {
		return res, false, fmt.Errorf("failed to create new ref for source directory: %w", err)
	}

	defer func() {
		if rerr != nil {
			// Only release this on error, otherwise leave it to be released when the
			// service cleans up.
			abandonedRef.Release(ctx)
		}
	}()

	// Mount the mutable ref of their changes over the target path.
	err = MountRef(ctx, abandonedRef, nil, func(root string) (rerr error) {
		resolvedDir, err := containerdfs.RootPath(root, source.Dir)
		if err != nil {
			return err
		}
		return mountIntoContainer(ctx, containerID, resolvedDir, target)
	})
	if err != nil {
		return res, false, fmt.Errorf("failed to remount mutable copy: %w", err)
	}

	// Keep track of the mutable ref so we can release it when the service stops.
	svc.Releasers = append(svc.Releasers, abandonedRef)

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return res, false, fmt.Errorf("get dagql server: %w", err)
	}

	var snapshot dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &snapshot, dagql.Selector{
		Field: "__immutableRef",
		Args: []dagql.NamedInput{
			{
				Name:  "ref",
				Value: dagql.String(immutableRef.ID()),
			},
		},
	}); err != nil {
		return res, false, err
	}

	// ensure we actually run the __immutableRef DagOp that does a Clone()
	if _, err := snapshot.Self().Evaluate(ctx); err != nil {
		return res, false, fmt.Errorf("failed to evaluate snapshot: %w", err)
	}

	return snapshot, true, nil
}

func mountIntoContainer(ctx context.Context, containerID, sourcePath, targetPath string) error {
	fdMnt, err := unix.OpenTree(unix.AT_FDCWD, sourcePath, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open tree %s: %w", sourcePath, err)
	}
	defer unix.Close(fdMnt)
	return buildkit.GetGlobalNamespaceWorkerPool().RunInNamespaces(ctx, containerID, []specs.LinuxNamespace{
		{Type: specs.MountNamespace},
	}, func() error {
		// Create target directory if it doesn't exist
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			if err := os.MkdirAll(targetPath, 0755); err != nil && !os.IsExist(err) {
				return fmt.Errorf("mkdir %s: %w", targetPath, err)
			}
		}

		// Unmount any existing mount at the target path
		err = unix.Unmount(targetPath, unix.MNT_DETACH)
		if err != nil && err != unix.EINVAL && err != unix.ENOENT {
			slog.Warn("unmount failed during container remount", "path", targetPath, "error", err)
			// Continue anyway, might not be mounted
		}

		err = unix.MoveMount(fdMnt, "", unix.AT_FDCWD, targetPath, unix.MOVE_MOUNT_F_EMPTY_PATH)
		if err != nil {
			return fmt.Errorf("move mount to %s: %w", targetPath, err)
		}

		return nil
	})
}

type ServiceBindings []ServiceBinding

type ServiceBinding struct {
	Service  dagql.ObjectResult[*Service]
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
