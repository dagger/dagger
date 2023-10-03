package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

type Service struct {
	// Container is the container to run as a service.
	Container *Container `json:"container"`
}

func NewContainerService(ctr *Container) *Service {
	return &Service{
		Container: ctr,
	}
}

var _ pipeline.Pipelineable = (*Service)(nil)

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (svc *Service) Clone() *Service {
	cp := *svc
	if cp.Container != nil {
		cp.Container = cp.Container.Clone()
	}
	return &cp
}

// PipelinePath returns the service's pipeline path.
func (svc *Service) PipelinePath() pipeline.Path {
	switch {
	case svc.Container != nil:
		return svc.Container.Pipeline
	default:
		return pipeline.Path{}
	}
}

// Service is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ resourceid.Digestible = (*Service)(nil)

// Digest returns the service's content hash.
func (svc *Service) Digest() (digest.Digest, error) {
	return stableDigest(svc)
}

func (svc *Service) Hostname(ctx context.Context, svcs *Services) (string, error) {
	switch {
	case svc.Container != nil: // container=>container
		dig, err := svc.Digest()
		if err != nil {
			return "", err
		}

		return network.HostHash(dig), nil
	default:
		return "", errors.New("unknown service type")
	}
}

func (svc *Service) Ports(ctx context.Context, svcs *Services) ([]Port, error) {
	switch {
	case svc.Container != nil:
		return svc.Container.Ports, nil
	default:
		return nil, errors.New("unknown service type")
	}
}

func (svc *Service) Endpoint(ctx context.Context, svcs *Services, port int, scheme string) (string, error) {
	var host string
	var err error
	switch {
	case svc.Container != nil:
		host, err = svc.Hostname(ctx, svcs)
		if err != nil {
			return "", err
		}

		if port == 0 {
			if len(svc.Container.Ports) == 0 {
				return "", fmt.Errorf("no ports exposed")
			}

			port = svc.Container.Ports[0].Port
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

func (svc *Service) Start(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	interactive bool,
	forwardStdin func(io.Writer, bkgw.ContainerProcess),
	forwardStdout func(io.Reader),
	forwardStderr func(io.Reader),
) (running *RunningService, err error) {
	switch {
	case svc.Container != nil:
		return svc.startContainer(ctx, bk, svcs, interactive, forwardStdin, forwardStdout, forwardStderr)
	default:
		return nil, fmt.Errorf("unknown service type")
	}
}

func (svc *Service) startContainer(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	interactive bool,
	forwardStdin func(io.Writer, bkgw.ContainerProcess),
	forwardStdout func(io.Reader),
	forwardStderr func(io.Reader),
) (running *RunningService, err error) {
	dig, err := svc.Digest()
	if err != nil {
		return nil, err
	}

	host, err := svc.Hostname(ctx, svcs)
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

	detachDeps, _, err := svcs.StartBindings(ctx, bk, ctr.Services)
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

	health := newHealth(bk, fullHost, ctr.Ports)

	pbPlatform := pb.PlatformFromSpec(ctr.Platform)

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

	execOp.Meta.ProxyEnv.FtpProxy, err = buildkit.ContainerExecUncachedMetadata{
		ParentClientIDs:       clientMetadata.ClientIDs(),
		ServerID:              clientMetadata.ServerID,
		ProgSockPath:          bk.ProgSockPath,
		ModuleDigest:          clientMetadata.ModuleDigest,
		FunctionContextDigest: clientMetadata.FunctionContextDigest,
	}.ToPBFtpProxyVal()
	if err != nil {
		return nil, err
	}

	env := append(execOp.Meta.Env, proxyEnvList(execOp.Meta.ProxyEnv)...)
	if interactive {
		// TODO:
		env = append(env, "HACK_TO_PASS_TTY_THROUGH=1")
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
		defer close(exited)
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
			Host:  fullHost,
			Ports: ctr.Ports,
			Key: ServiceKey{
				Digest:   dig,
				ClientID: clientMetadata.ClientID,
			},
			Stop: stopSvc,
			Wait: func() error {
				<-exited
				return nil
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

type ServiceBindings []ServiceBinding

type ServiceBinding struct {
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
