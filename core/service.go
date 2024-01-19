package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/progrock"
)

const (
	ShimEnableTTYEnvVar = "_DAGGER_ENABLE_TTY"
)

type Service struct {
	Query *Query

	// Container is the container to run as a service.
	Container *Container `json:"container"`

	// TunnelUpstream is the service that this service is tunnelling to.
	TunnelUpstream *dagql.Instance[*Service] `json:"upstream,omitempty"`
	// TunnelPorts configures the port forwarding rules for the tunnel.
	TunnelPorts []PortForward `json:"tunnel_ports,omitempty"`

	// HostUpstream is the host address (i.e. hostname or IP) for the reverse
	// tunnel to request through the host.
	HostUpstream string `json:"reverse_tunnel_upstream_addr,omitempty"`
	// HostPorts configures the port forwarding rules for the host.
	HostPorts []PortForward `json:"host_ports,omitempty"`
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

var _ pipeline.Pipelineable = (*Service)(nil)

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
	cp.HostPorts = cloneSlice(cp.HostPorts)
	return &cp
}

// PipelinePath returns the service's pipeline path.
func (svc *Service) PipelinePath() pipeline.Path {
	return svc.Query.Pipeline
}

func (svc *Service) Hostname(ctx context.Context, id *idproto.ID) (string, error) {
	switch {
	case svc.TunnelUpstream != nil: // host=>container (127.0.0.1)
		upstream, err := svc.Query.Services.Get(ctx, id)
		if err != nil {
			return "", err
		}

		return upstream.Host, nil
	case svc.Container != nil, // container=>container
		svc.HostUpstream != "": // container=>host
		dig, err := id.Digest()
		if err != nil {
			return "", err
		}

		return network.HostHash(dig), nil
	default:
		return "", errors.New("unknown service type")
	}
}

func (svc *Service) Ports(ctx context.Context, id *idproto.ID) ([]Port, error) {
	switch {
	case svc.TunnelUpstream != nil, svc.HostUpstream != "":
		running, err := svc.Query.Services.Get(ctx, id)
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

func (svc *Service) Endpoint(ctx context.Context, id *idproto.ID, port int, scheme string) (string, error) {
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
		tunnel, err := svc.Query.Services.Get(ctx, id)
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
	case svc.HostUpstream != "":
		host, err = svc.Hostname(ctx, id)
		if err != nil {
			return "", err
		}

		if port == 0 {
			if len(svc.HostPorts) == 0 {
				return "", fmt.Errorf("no ports")
			}

			port = svc.HostPorts[0].FrontendOrBackendPort()
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

func (svc *Service) StartAndTrack(ctx context.Context, id *idproto.ID) error {
	_, err := svc.Query.Services.Start(ctx, id, svc)
	return err
}

func (svc *Service) Stop(ctx context.Context, id *idproto.ID) error {
	return svc.Query.Services.Stop(ctx, id)
}

func (svc *Service) Start(
	ctx context.Context,
	id *idproto.ID,
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
	case svc.HostUpstream != "":
		return svc.startReverseTunnel(ctx, id)
	default:
		return nil, fmt.Errorf("unknown service type")
	}
}

// nolint: gocyclo
func (svc *Service) startContainer(
	ctx context.Context,
	id *idproto.ID,
	interactive bool,
	forwardStdin func(io.Writer, bkgw.ContainerProcess),
	forwardStdout func(io.Reader),
	forwardStderr func(io.Reader),
) (running *RunningService, err error) {
	dig, err := id.Digest()
	if err != nil {
		return nil, err
	}

	host, err := svc.Hostname(ctx, id)
	if err != nil {
		return nil, err
	}

	rec := progrock.FromContext(ctx).WithGroup(
		fmt.Sprintf("service %s", host),
		progrock.Weak(),
	)

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

	detachDeps, _, err := svc.Query.Services.StartBindings(ctx, ctr.Services)
	if err != nil {
		return nil, fmt.Errorf("start dependent services: %w", err)
	}

	defer func() {
		if err != nil {
			detachDeps()
		}
	}()

	vtx := rec.Vertex(dig, "start "+strings.Join(execOp.Meta.Args, " "))
	defer func() {
		if err != nil {
			vtx.Error(err)
		}
	}()

	fullHost := host + "." + network.ClientDomain(clientMetadata.ClientID)

	bk := svc.Query.Buildkit

	health := newHealth(bk, fullHost, ctr.Ports)

	pbPlatform := pb.PlatformFromSpec(ctr.Platform.Spec())

	mounts := make([]bkgw.Mount, len(execOp.Mounts))
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

			res, err := bk.Solve(ctx, bkgw.SolveRequest{
				Definition: def,
			})
			if err != nil {
				return nil, fmt.Errorf("solve mount %s: %w", m.Dest, err)
			}

			mount.Ref = res.Ref
		}

		mounts[i] = mount
	}

	gc, err := bk.NewContainer(ctx, bkgw.NewContainerRequest{
		Mounts:   mounts,
		Hostname: fullHost,
		Platform: &pbPlatform,
	})
	if err != nil {
		return nil, fmt.Errorf("new container: %w", err)
	}

	defer func() {
		if err != nil {
			gc.Release(context.Background())
		}
	}()

	checked := make(chan error, 1)
	go func() {
		checked <- health.Check(ctx)
	}()

	if execOp.Meta.ProxyEnv == nil {
		execOp.Meta.ProxyEnv = &pb.ProxyEnv{}
	}

	execMeta := buildkit.ContainerExecUncachedMetadata{
		ParentClientIDs: clientMetadata.ClientIDs(),
		ServerID:        clientMetadata.ServerID,
		ProgSockPath:    bk.ProgSockPath,
		ProgParent:      rec.Parent,
	}
	execOp.Meta.ProxyEnv.FtpProxy, err = execMeta.ToPBFtpProxyVal()
	if err != nil {
		return nil, err
	}

	env := append([]string{}, execOp.Meta.Env...)
	env = append(env, proxyEnvList(execOp.Meta.ProxyEnv)...)
	if interactive {
		env = append(env, ShimEnableTTYEnvVar+"=1")
	}

	outBuf := new(bytes.Buffer)
	var stdinCtr, stdoutClient, stderrClient io.ReadCloser
	var stdinClient, stdoutCtr, stderrCtr io.WriteCloser
	if forwardStdin != nil {
		stdinCtr, stdinClient = io.Pipe()
	}

	if forwardStdout != nil {
		stdoutClient, stdoutCtr = io.Pipe()
	} else {
		stdoutCtr = nopCloser{io.MultiWriter(vtx.Stdout(), outBuf)}
	}

	if forwardStderr != nil {
		stderrClient, stderrCtr = io.Pipe()
	} else {
		stderrCtr = nopCloser{io.MultiWriter(vtx.Stderr(), outBuf)}
	}

	svcProc, err := gc.Start(ctx, bkgw.StartRequest{
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

	exited := make(chan error, 1)
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

		exited <- svcProc.Wait()

		// detach dependent services when process exits
		detachDeps()
	}()

	stopSvc := func(ctx context.Context) (stopErr error) {
		defer func() {
			vtx.Done(stopErr)
		}()

		// TODO(vito): graceful shutdown?
		if err := svcProc.Signal(ctx, syscall.SIGKILL); err != nil {
			return fmt.Errorf("signal: %w", err)
		}

		if err := gc.Release(ctx); err != nil {
			// TODO(vito): returns context.Canceled, which is a bit strange, because
			// that's the goal
			if !errors.Is(err, context.Canceled) {
				return fmt.Errorf("release: %w", err)
			}
		}

		return nil
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
				Digest:   dig,
				ServerID: clientMetadata.ServerID,
			},
			Stop: stopSvc,
			Wait: func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case err := <-exited:
					return err
				}
			},
		}, nil
	case err := <-exited:
		if err != nil {
			return nil, fmt.Errorf("exited: %w\noutput: %s", err, outBuf.String())
		}

		return nil, fmt.Errorf("service exited before healthcheck")
	}
}

func proxyEnvList(p *pb.ProxyEnv) []string {
	if p == nil {
		return nil
	}
	out := []string{}
	if v := p.HttpProxy; v != "" {
		out = append(out, "HTTP_PROXY="+v, "http_proxy="+v)
	}
	if v := p.HttpsProxy; v != "" {
		out = append(out, "HTTPS_PROXY="+v, "https_proxy="+v)
	}
	if v := p.FtpProxy; v != "" {
		out = append(out, "FTP_PROXY="+v, "ftp_proxy="+v)
	}
	if v := p.NoProxy; v != "" {
		out = append(out, "NO_PROXY="+v, "no_proxy="+v)
	}
	if v := p.AllProxy; v != "" {
		out = append(out, "ALL_PROXY="+v, "all_proxy="+v)
	}
	return out
}

func (svc *Service) startTunnel(ctx context.Context, id *idproto.ID) (running *RunningService, err error) {
	svcCtx, stop := context.WithCancel(context.Background())

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		stop()
		return nil, err
	}
	svcCtx = engine.ContextWithClientMetadata(svcCtx, clientMetadata)

	svcCtx = progrock.ToContext(svcCtx, progrock.FromContext(ctx))

	svcs := svc.Query.Services
	bk := svc.Query.Buildkit

	upstream, err := svcs.Start(svcCtx, svc.TunnelUpstream.ID(), svc.TunnelUpstream.Self)
	if err != nil {
		stop()
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
			stop()
			return nil, fmt.Errorf("host to container: %w", err)
		}

		_, portStr, err := net.SplitHostPort(res.GetAddr())
		if err != nil {
			stop()
			return nil, fmt.Errorf("split host port: %w", err)
		}

		frontend, err = strconv.Atoi(portStr)
		if err != nil {
			stop()
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

	dig, err := id.Digest()
	if err != nil {
		stop()
		return nil, err
	}

	return &RunningService{
		Service: svc,
		Key: ServiceKey{
			Digest:   dig,
			ServerID: clientMetadata.ServerID,
		},
		Host:  dialHost,
		Ports: ports,
		Stop: func(context.Context) error {
			stop()
			// HACK(vito): do this async to prevent deadlock (this is called in Detach)
			go svcs.Detach(svcCtx, upstream)
			var errs []error
			for _, closeListener := range closers {
				errs = append(errs, closeListener())
			}
			return errors.Join(errs...)
		},
	}, nil
}

func (svc *Service) startReverseTunnel(ctx context.Context, id *idproto.ID) (running *RunningService, err error) {
	dig, err := id.Digest()
	if err != nil {
		return nil, err
	}

	host, err := svc.Hostname(ctx, id)
	if err != nil {
		return nil, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rec := progrock.FromContext(ctx)

	svcCtx, stop := context.WithCancel(context.Background())
	svcCtx = engine.ContextWithClientMetadata(svcCtx, clientMetadata)
	svcCtx = progrock.ToContext(svcCtx, rec)

	fullHost := host + "." + network.ClientDomain(clientMetadata.ClientID)

	bk := svc.Query.Buildkit

	tunnel := &c2hTunnel{
		bk:                 bk,
		upstreamHost:       svc.HostUpstream,
		tunnelServiceHost:  fullHost,
		tunnelServicePorts: svc.HostPorts,
	}

	checkPorts := []Port{}
	for _, p := range svc.HostPorts {
		desc := fmt.Sprintf("tunnel %s %d -> %d", p.Protocol, p.FrontendOrBackendPort(), p.Backend)
		checkPorts = append(checkPorts, Port{
			Port:        p.FrontendOrBackendPort(),
			Protocol:    p.Protocol,
			Description: &desc,
		})
	}

	check := newHealth(bk, fullHost, checkPorts)

	exited := make(chan error, 1)
	go func() {
		exited <- tunnel.Tunnel(svcCtx)
	}()

	checked := make(chan error, 1)
	go func() {
		checked <- check.Check(svcCtx)
	}()

	select {
	case err := <-checked:
		if err != nil {
			stop()
			return nil, fmt.Errorf("health check errored: %w", err)
		}

		return &RunningService{
			Service: svc,
			Key: ServiceKey{
				Digest:   dig,
				ServerID: clientMetadata.ServerID,
			},
			Host:  fullHost,
			Ports: checkPorts,
			Stop: func(context.Context) error {
				stop()
				return nil
			},
		}, nil
	case err := <-exited:
		stop()
		return nil, fmt.Errorf("proxy exited: %w", err)
	}
}

type ServiceBindings []ServiceBinding

type ServiceBinding struct {
	ID       *idproto.ID
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
