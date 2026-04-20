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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/telemetryattrs"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	gwpb "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
)

const (
	ShimEnableTTYEnvVar = "_DAGGER_ENABLE_TTY"
)

type Service struct {
	// A custom hostname set by the user.
	CustomHostname string

	// Container is the container to run as a service.
	Container                     dagql.ObjectResult[*Container]
	Args                          []string
	ExperimentalPrivilegedNesting bool
	InsecureRootCapabilities      bool
	NoInit                        bool
	ExecMD                        *engineutil.ExecutionMetadata
	ExecMeta                      *executor.Meta

	// TunnelUpstream is the service that this service is tunnelling to.
	TunnelUpstream dagql.ObjectResult[*Service]
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
	cp.TunnelPorts = slices.Clone(cp.TunnelPorts)
	cp.HostSockets = slices.Clone(cp.HostSockets)
	return &cp
}

func (svc *Service) Evaluate(ctx context.Context) error {
	return nil
}

func (svc *Service) Sync(ctx context.Context) error {
	return svc.Evaluate(ctx)
}

func (svc *Service) WithHostname(hostname string) *Service {
	svc = svc.Clone()
	svc.CustomHostname = hostname
	return svc
}

func (svc *Service) Hostname(ctx context.Context, dig digest.Digest) (string, error) {
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
		upstream, err := svcs.Get(ctx, dig, true)
		if err != nil {
			return "", err
		}

		return upstream.Host, nil
	case svc.Container.Self() != nil, // container=>container
		len(svc.HostSockets) > 0: // container=>host
		if dig == "" {
			return "", errors.New("service digest is empty")
		}
		return network.HostHash(dig), nil
	default:
		return "", errors.New("unknown service type")
	}
}

func (svc *Service) Ports(ctx context.Context, dig digest.Digest) ([]Port, error) {
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
		running, err := svcs.Get(ctx, dig, svc.TunnelUpstream.Self() != nil)
		if err != nil {
			return nil, err
		}

		return running.Ports, nil
	case svc.Container.Self() != nil:
		return svc.Container.Self().Ports, nil
	default:
		return nil, errors.New("unknown service type")
	}
}

func (svc *Service) Endpoint(ctx context.Context, dig digest.Digest, port int, scheme string) (string, error) {
	var host string

	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", err
	}

	switch {
	case svc.Container.Self() != nil:
		host, err = svc.Hostname(ctx, dig)
		if err != nil {
			return "", err
		}

		if port == 0 {
			if len(svc.Container.Self().Ports) == 0 {
				return "", fmt.Errorf("no ports exposed")
			}

			port = svc.Container.Self().Ports[0].Port
		}
	case svc.TunnelUpstream.Self() != nil:
		svcs, err := query.Services(ctx)
		if err != nil {
			return "", err
		}
		tunnel, err := svcs.Get(ctx, dig, true)
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
		host, err = svc.Hostname(ctx, dig)
		if err != nil {
			return "", err
		}

		if port == 0 {
			portForward, err := svc.HostSockets[0].PortForward(ctx)
			if err != nil {
				return "", fmt.Errorf("service endpoint: socket port forward: %w", err)
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

func (svc *Service) Stop(ctx context.Context, dig digest.Digest, kill bool) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return err
	}
	return svcs.Stop(ctx, dig, kill, svc.TunnelUpstream.Self() != nil)
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
	running *RunningService,
	dig digest.Digest,
	opts ServiceStartOpts,
) error {
	switch {
	case svc.Container.Self() != nil:
		return svc.startContainer(ctx, running, dig, opts)
	case svc.TunnelUpstream.Self() != nil:
		return svc.startTunnel(ctx, running, opts)
	case len(svc.HostSockets) > 0:
		return svc.startReverseTunnel(ctx, running, dig, opts)
	default:
		return fmt.Errorf("unknown service type")
	}
}

//nolint:gocyclo
func (svc *Service) startContainer(
	ctx context.Context,
	running *RunningService,
	dig digest.Digest,
	opts ServiceStartOpts,
) (rerr error) {
	if running == nil {
		return fmt.Errorf("running service is nil")
	}

	var cleanup cleanups.Cleanups
	defer func() {
		if rerr != nil {
			cleanup.Run()
		}
	}()

	slog := slog.With("service", dig.String())

	host, err := svc.Hostname(ctx, dig)
	if err != nil {
		return err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	cacheCtr, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	srv := dagql.CurrentDagqlServer(ctx)
	if srv == nil {
		return fmt.Errorf("failed to get dagql server")
	}
	attachedAny, err := cacheCtr.AttachResult(ctx, clientMetadata.SessionID, srv, svc.Container)
	if err != nil {
		return fmt.Errorf("attach service container: %w", err)
	}
	attached, ok := attachedAny.(dagql.ObjectResult[*Container])
	if !ok {
		return fmt.Errorf("attach service container: expected %T, got %T", svc.Container, attachedAny)
	}
	svc.Container = attached
	if err := cacheCtr.Evaluate(ctx, svc.Container); err != nil {
		return err
	}
	ctr := svc.Container.Self()
	if ctr == nil {
		return fmt.Errorf("service container is nil")
	}

	execMD := svc.ExecMD
	if execMD == nil {
		execMD, err = ctr.execMeta(ctx, ContainerExecOpts{
			ExperimentalPrivilegedNesting: svc.ExperimentalPrivilegedNesting,
			NoInit:                        svc.NoInit,
		}, nil)
		if err != nil {
			return err
		}
	} else {
		cloned := *execMD
		execMD = &cloned
	}
	if opts.LogTargetCallDigest != "" {
		execMD.LogTargetCallDigest = opts.LogTargetCallDigest
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return err
	}
	detachDeps, _, err := svcs.StartBindings(ctx, ctr.Services)
	if err != nil {
		return fmt.Errorf("start dependent services: %w", err)
	}
	cleanup.Add("detach deps", cleanups.Infallible(detachDeps))

	var domain string
	if mod, err := query.ModuleParent(ctx); err == nil && svc.CustomHostname != "" {
		implementationScopedMod, err := ImplementationScopedModule(ctx, mod)
		if err != nil {
			return fmt.Errorf("failed to get implementation-scoped module: %w", err)
		}
		modDigest, err := implementationScopedMod.ContentPreferredDigest(ctx)
		if err != nil {
			return fmt.Errorf("failed to get implementation-scoped module digest: %w", err)
		}
		domain = network.ModuleDomain(modDigest, clientMetadata.SessionID)
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

	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("failed to get engine client: %w", err)
	}
	cache := query.SnapshotManager()

	svcID := identity.NewID()

	releaseLockedCaches, err := lockMountedCaches(ctx, ctr.Mounts)
	if err != nil {
		return fmt.Errorf("lock mounted caches: %w", err)
	}
	cleanup.Add("release locked cache volume access", func() error {
		releaseLockedCaches()
		return nil
	})

	p, err := prepareMounts(ctx, ctr, nil, nil, nil, cache, "", runtime.GOOS, func(_ string, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
		return cache.New(ctx, ref)
	})
	if err != nil {
		return fmt.Errorf("prepare mounts: %w", err)
	}

	cleanup.Add("release active refs", func() error {
		return p.releaseActives(context.WithoutCancel(ctx))
	})
	cleanup.Add("release output refs", func() error {
		return p.releaseOutputRefs(context.WithoutCancel(ctx))
	})

	meta := svc.ExecMeta
	if meta == nil {
		meta, err = ctr.metaSpec(ctx, ContainerExecOpts{
			Args:                          svc.Args,
			ExperimentalPrivilegedNesting: svc.ExperimentalPrivilegedNesting,
			InsecureRootCapabilities:      svc.InsecureRootCapabilities,
			NoInit:                        svc.NoInit,
		})
		if err != nil {
			return err
		}
		meta.Hostname = fullHost
	}
	if opts.IO != nil && opts.IO.Interactive {
		meta.Tty = true
		meta.Env = addDefaultEnvvar(meta.Env, "TERM", "xterm")
	}

	attrs := []attribute.KeyValue{}
	if opts.LogTargetCallDigest != "" {
		attrs = append(attrs, attribute.String(telemetryattrs.UIResumeOutputAttr, opts.LogTargetCallDigest.String()))
	}

	ctx, span := Tracer(ctx).Start(
		// The parent is the call site that triggered it to start.
		ctx,
		// Match naming scheme of normal exec span.
		fmt.Sprintf("exec %s", strings.Join(svc.Args, " ")),
		trace.WithAttributes(attrs...),
	)
	defer func() {
		if rerr != nil {
			// NB: this is intentionally conditional; we only complete if there was
			// an error starting. span.End is called when the service exits.
			telemetry.EndWithCause(span, &rerr)
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
	if opts.IO != nil && opts.IO.Stdin != nil {
		stdinReader = opts.IO.Stdin
	}
	stdoutWriters := multiWriteCloser{outBufWC}
	if opts.IO != nil && opts.IO.Stdout != nil {
		stdoutWriters = append(stdoutWriters, opts.IO.Stdout)
	}
	stderrWriters := multiWriteCloser{errBufWC}
	if opts.IO != nil && opts.IO.Stderr != nil {
		stderrWriters = append(stderrWriters, opts.IO.Stderr)
	}

	started := make(chan struct{})

	signal := make(chan syscall.Signal)
	var resize <-chan executor.WinSize
	if opts.IO != nil {
		resize = convertResizeChannel(ctx, opts.IO.ResizeCh)
	}

	secretEnv, err := ctr.secretEnvValues(ctx)
	if err != nil {
		return err
	}
	meta.Env = append(meta.Env, secretEnv...)

	worker := bk.Worker.ExecWorker(span.SpanContext(), *execMD)
	exited := make(chan struct{})
	runErr := make(chan error)
	go func() {
		_, err := worker.Run(ctx, svcID, p.Root, p.Mounts, executor.ProcessInfo{
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
		return context.Cause(ctx)
	case <-started:
	}

	checked := make(chan error, 1)

	if ctr.Config.Healthcheck != nil {
		dockerHealthcheck, err := newDockerHealthcheck(worker, svcID, ctr, span.SpanContext())
		if err != nil {
			return fmt.Errorf("failed to setup docker healthcheck: %w", err)
		}
		go func() {
			checked <- dockerHealthcheck.Check(ctx)
		}()
	} else {
		go func() {
			checked <- newPortHealth(bk, engineutil.NewDirectNS(svcID), fullHost, ctr.Ports).Check(ctx)
		}()
	}

	var stopped atomic.Bool

	var exitErr error
	go func() {
		defer func() {
			opts.IO.Close()
			close(exited)
		}()

		exitErr = <-runErr
		slog.Info("service exited", "err", exitErr)

		// show the exit status; doing so won't fail anything, and is
		// helpful for troubleshooting
		var telemetryErr error
		defer telemetry.EndWithCause(span, &telemetryErr)
		defer func() {
			if !stopped.Load() {
				// we only care about the exit result (likely 137) if we weren't stopped
				telemetryErr = exitErr
			}
		}()

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
			opts.IO.Close()
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
		err = worker.Exec(ctx, svcID, executor.ProcessInfo{
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
			return fmt.Errorf("health check errored: %w", err)
		}

		running.Host = fullHost
		running.Ports = ctr.Ports
		running.Stop = stopSvc
		running.Wait = waitSvc
		running.Exec = execSvc
		running.ContainerID = svcID
		return nil
	case <-exited:
		if exitErr != nil {
			var gwErr *gwpb.ExitError
			if errors.As(exitErr, &gwErr) {
				// Create ExecError with available service information
				return &ExecError{
					Err:      telemetry.TrackOrigin(gwErr, span.SpanContext()),
					Cmd:      meta.Args,
					ExitCode: int(gwErr.ExitCode),
					Stdout:   stdoutBuf.String(),
					Stderr:   stderrBuf.String(),
				}
			}
			return exitErr
		}
		return fmt.Errorf("service exited before healthcheck")
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

func (svc *Service) startTunnel(ctx context.Context, running *RunningService, _ ServiceStartOpts) (rerr error) {
	if running == nil {
		return fmt.Errorf("running service is nil")
	}
	svcCtx, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	defer func() {
		if rerr != nil {
			stop(fmt.Errorf("tunnel start error: %w", rerr))
		}
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	svcCtx = engine.ContextWithClientMetadata(svcCtx, clientMetadata)

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("failed to get engine client: %w", err)
	}

	upstream, err := svcs.StartResult(svcCtx, svc.TunnelUpstream, svc.TunnelUpstream.Self().TunnelUpstream.Self() != nil)
	if err != nil {
		return fmt.Errorf("start upstream: %w", err)
	}
	const bindHost = "0.0.0.0"
	const dialHost = "127.0.0.1"
	stopErr := errors.New("service stop called")
	upstreamExitedErr := errors.New("upstream exited")

	closers := make([]func() error, len(svc.TunnelPorts))
	ports := make([]Port, len(svc.TunnelPorts))

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
			return fmt.Errorf("host to container: %w", err)
		}

		_, portStr, err := net.SplitHostPort(res.GetAddr())
		if err != nil {
			return fmt.Errorf("split host port: %w", err)
		}

		frontend, err = strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("parse port: %w", err)
		}

		desc := fmt.Sprintf("tunnel %s:%d -> %s:%d", bindHost, frontend, upstream.Host, forward.Backend)

		ports[i] = Port{
			Port:        frontend,
			Protocol:    forward.Protocol,
			Description: &desc,
		}

		closers[i] = closeListener
	}

	var shutdownOnce sync.Once
	var shutdownErr error
	shutdown := func(cause error) error {
		shutdownOnce.Do(func() {
			stop(cause)
			svcs.Detach(svcCtx, upstream)
			var errs []error
			for _, closeListener := range closers {
				errs = append(errs, closeListener())
			}
			shutdownErr = errors.Join(errs...)
		})
		return shutdownErr
	}

	go func() {
		if upstream.Wait == nil {
			return
		}
		err := upstream.Wait(context.Background())
		if err != nil {
			_ = shutdown(fmt.Errorf("%w: %w", upstreamExitedErr, err))
			return
		}
		_ = shutdown(upstreamExitedErr)
	}()

	running.Host = dialHost
	running.Ports = ports
	running.Stop = func(_ context.Context, _ bool) error {
		return shutdown(stopErr)
	}
	running.Wait = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-svcCtx.Done():
			if errors.Is(context.Cause(svcCtx), stopErr) {
				return nil
			}
			return context.Cause(svcCtx)
		}
	}
	return nil
}

func (svc *Service) startReverseTunnel(ctx context.Context, running *RunningService, dig digest.Digest, _ ServiceStartOpts) (rerr error) {
	if running == nil {
		return fmt.Errorf("running service is nil")
	}
	host, err := svc.Hostname(ctx, dig)
	if err != nil {
		return err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	fullHost := host + "." + network.SessionDomain(clientMetadata.SessionID)

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("failed to get engine client: %w", err)
	}

	// we don't need a full container, just a CNI provisioned network namespace to listen in
	netNS, err := bk.NewNetworkNamespace(ctx, fullHost)
	if err != nil {
		return fmt.Errorf("new network namespace: %w", err)
	}

	checkPorts := []Port{}
	descs := make([]string, 0, len(svc.HostSockets))
	for _, sock := range svc.HostSockets {
		port, err := sock.PortForward(ctx)
		if err != nil {
			return fmt.Errorf("service reverse tunnel: socket port forward: %w", err)
		}
		desc := fmt.Sprintf("tunnel %s %d -> %d", port.Protocol, port.FrontendOrBackendPort(), port.Backend)
		descs = append(descs, desc)
		checkPorts = append(checkPorts, Port{
			Port:        port.FrontendOrBackendPort(),
			Protocol:    port.Protocol,
			Description: &desc,
		})
	}

	ctx, span := Tracer(ctx).Start(ctx, strings.Join(descs, ", "))
	defer func() {
		if rerr != nil {
			// NB: this is intentionally conditional; we only complete if there was
			// an error starting. span.End is called when the service exits.
			telemetry.EndWithCause(span, &rerr)
		}
	}()

	tunnel := &c2hTunnel{
		bk:    bk,
		ns:    netNS,
		socks: svc.HostSockets,
	}

	// NB: decouple from the incoming ctx cancel and add our own
	svcCtx, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	stopErr := errors.New("service stop called")
	proxyExitedErr := errors.New("proxy exited")

	exited := make(chan struct{}, 1)
	var exitErr error
	go func() {
		defer close(exited)
		exitErr = tunnel.Tunnel(svcCtx)
	}()

	checked := make(chan error, 1)
	go func() {
		checked <- newPortHealth(bk, netNS, fullHost, checkPorts).Check(svcCtx)
	}()

	select {
	case err := <-checked:
		if err != nil {
			netNS.Release(svcCtx)
			err = fmt.Errorf("health check errored: %w", err)
			stop(err)
			return err
		}

		var shutdownOnce sync.Once
		var shutdownErr error
		shutdown := func(cause error, ignoreExitErr bool) error {
			shutdownOnce.Do(func() {
				stop(cause)
				waitCtx, waitCancel := context.WithTimeout(context.WithoutCancel(svcCtx), 10*time.Second)
				defer waitCancel()
				netNS.Release(waitCtx)
				select {
				case <-waitCtx.Done():
					shutdownErr = fmt.Errorf("timeout waiting for tunnel to stop: %w", waitCtx.Err())
				case <-exited:
					if !ignoreExitErr && exitErr != nil {
						shutdownErr = exitErr
					}
				}
				telemetryErr := shutdownErr
				telemetry.EndWithCause(span, &telemetryErr)
			})
			return shutdownErr
		}

		running.Host = fullHost
		running.Ports = checkPorts
		running.Stop = func(context.Context, bool) (rerr error) {
			return shutdown(stopErr, true)
		}
		running.Wait = func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-exited:
				return shutdown(proxyExitedErr, false)
			}
		}
		return nil
	case <-exited:
		netNS.Release(svcCtx)
		stop(errors.New("proxy exited"))
		if exitErr != nil {
			return fmt.Errorf("proxy exited: %w", exitErr)
		}
		return fmt.Errorf("proxy exited before healthcheck")
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
	running *RunningService,
	target string,
	source dagql.ObjectResult[*Directory],
	f func() error,
) (res dagql.ObjectResult[*Directory], hasChanges bool, rerr error) {
	if running == nil {
		return res, false, fmt.Errorf("running service is nil")
	}
	running.workspaceMu.Lock()
	defer running.workspaceMu.Unlock()

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return res, false, err
	}
	if err := cache.Evaluate(ctx, source); err != nil {
		return res, false, fmt.Errorf("failed to evaluate source directory: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return res, false, err
	}

	ref, err := source.Self().Snapshot.GetOrEval(ctx, source.Result)
	if err != nil {
		return res, false, fmt.Errorf("failed to get ref for source directory: %w", err)
	}
	sourceDirPath, err := source.Self().Dir.GetOrEval(ctx, source.Result)
	if err != nil {
		return res, false, fmt.Errorf("failed to get path for source directory: %w", err)
	}

	bk, err := query.Engine(ctx)
	if err != nil {
		return res, false, fmt.Errorf("failed to get engine client: %w", err)
	}

	mutableRef, err := query.SnapshotManager().New(ctx, ref,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("mcp remount"))
	if err != nil {
		return res, false, fmt.Errorf("failed to create new ref for source directory: %w", err)
	}
	defer func() {
		if mutableRef != nil {
			_ = mutableRef.Release(ctx)
		}
	}()

	err = MountRef(ctx, mutableRef, func(root string, _ *mount.Mount) (rerr error) {
		resolvedDir, err := containerdfs.RootPath(root, sourceDirPath)
		if err != nil {
			return err
		}
		if err := mountIntoContainer(ctx, running.ContainerID, resolvedDir, target); err != nil {
			return fmt.Errorf("remount container: %w", err)
		}
		return f()
	})
	if err != nil {
		return res, false, err
	}

	usage, err := bk.Worker.Snapshotter.Usage(ctx, mutableRef.SnapshotID())
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
	mutableRef = nil

	// Create a new mutable ref to leave the service with, to prevent further
	// changes from mutating the now-immutable ref
	//
	// NOTE: there's technically a race here, for sure, but we can least prevent
	// mutation outside of the bounds of this func
	abandonedRef, err := query.SnapshotManager().New(ctx, immutableRef,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("mcp remount"))
	if err != nil {
		return res, false, fmt.Errorf("failed to create new ref for source directory: %w", err)
	}

	defer func() {
		if rerr != nil && abandonedRef != nil {
			// Only release this on error, otherwise leave it to be released when the
			// service cleans up.
			_ = abandonedRef.Release(ctx)
		}
	}()

	// Mount the mutable ref of their changes over the target path.
	err = MountRef(ctx, abandonedRef, func(root string, _ *mount.Mount) (rerr error) {
		resolvedDir, err := containerdfs.RootPath(root, sourceDirPath)
		if err != nil {
			return err
		}
		return mountIntoContainer(ctx, running.ContainerID, resolvedDir, target)
	})
	if err != nil {
		return res, false, fmt.Errorf("failed to remount mutable copy: %w", err)
	}

	running.TrackRef(abandonedRef)
	abandonedRef = nil

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		_ = immutableRef.Release(ctx)
		return res, false, fmt.Errorf("get dagql server: %w", err)
	}

	snapshot := &Directory{
		Platform: source.Self().Platform,
		Services: slices.Clone(source.Self().Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	snapshot.Dir.setValue(sourceDirPath)
	snapshot.Snapshot.setValue(immutableRef)

	inst, err := dagql.NewObjectResultForCurrentCall(ctx, srv, snapshot)
	if err != nil {
		_ = snapshot.OnRelease(context.WithoutCancel(ctx))
		return res, false, err
	}

	return inst, true, nil
}

func mountIntoContainer(ctx context.Context, containerID, sourcePath, targetPath string) error {
	fdMnt, err := unix.OpenTree(unix.AT_FDCWD, sourcePath, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open tree %s: %w", sourcePath, err)
	}
	defer unix.Close(fdMnt)
	return engineutil.GetGlobalNamespaceWorkerPool().RunInNamespaces(ctx, containerID, []specs.LinuxNamespace{
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

type LocalhostForwards []LocalhostForward

type LocalhostForward struct {
	Service     dagql.ObjectResult[*Service]
	Hostname    string // service hostname to resolve
	Port        int    // port on 127.0.0.1 inside container
	ServicePort int    // port on the service
}

// Set adds or replaces a localhost forward. If a forward for the same port
// already exists, it is replaced (last write wins, like WithEnvVariable).
func (fwds *LocalhostForwards) Set(fwd LocalhostForward) {
	for i, existing := range *fwds {
		if existing.Port == fwd.Port {
			(*fwds)[i] = fwd
			return
		}
	}
	*fwds = append(*fwds, fwd)
}
