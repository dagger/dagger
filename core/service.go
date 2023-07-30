package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

type Service struct {
	// Container is the container that this service is running in.
	Container *Container `json:"container"`
	// ContainerExecOpts is the command to run in the container.
	ContainerExecOpts ContainerExecOpts `json:"exec"`

	// Upstream is the service that this service is proxying to.
	ProxyUpstream *Service `json:"upstream,omitempty"`
	// ProxyUpstreamPort is the port for the proxy to send traffic to.
	ProxyUpstreamPort int `json:"proxy_port,omitempty"`
	// ProxyListenAddress is the host address that the proxy listens on.
	ProxyListenAddress string `json:"proxy_listen_address,omitempty"`
	// ProxyProtocol is the protocol for traffic proxied to upstream.
	ProxyProtocol NetworkProtocol `json:"proxy_protocol,omitempty"`
}

func NewContainerService(ctr *Container, opts ContainerExecOpts) *Service {
	return &Service{
		Container:         ctr,
		ContainerExecOpts: opts,
	}
}

func NewProxyService(upstream *Service, addr string, port int, proto NetworkProtocol) *Service {
	return &Service{
		ProxyUpstream:      upstream,
		ProxyUpstreamPort:  port,
		ProxyListenAddress: addr,
		ProxyProtocol:      proto,
	}
}

type ServiceID string

func (id ServiceID) String() string {
	return string(id)
}

// ServiceID is digestible so that smaller hashes can be displayed in
// --debug vertex names.
var _ Digestible = ServiceID("")

func (id ServiceID) Digest() (digest.Digest, error) {
	svc, err := id.ToService()
	if err != nil {
		return "", err
	}
	return svc.Digest()
}

func (id ServiceID) ToService() (*Service, error) {
	var service Service

	if id == "" {
		// scratch
		return &service, nil
	}

	if err := resourceid.Decode(&service, id); err != nil {
		return nil, err
	}

	return &service, nil
}

// ID marshals the service into a content-addressed ID.
func (svc *Service) ID() (ServiceID, error) {
	return resourceid.Encode[ServiceID](svc)
}

var _ pipeline.Pipelineable = (*Service)(nil)

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (svc *Service) Clone() *Service {
	cp := *svc
	if cp.Container != nil {
		cp.Container = cp.Container.Clone()
	}
	if cp.ProxyUpstream != nil {
		cp.ProxyUpstream = cp.ProxyUpstream.Clone()
	}
	return &cp
}

// PipelinePath returns the service's pipeline path.
func (svc *Service) PipelinePath() pipeline.Path {
	if svc.Container != nil {
		return svc.Container.Pipeline
	} else if svc.ProxyUpstream != nil {
		return svc.ProxyUpstream.PipelinePath()
	} else {
		// must be impossible
		return pipeline.Path{
			{Name: "you found a bug!"},
		}
	}
}

// Service is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ Digestible = (*Service)(nil)

// Digest returns the service's content hash.
func (svc *Service) Digest() (digest.Digest, error) {
	return stableDigest(svc)
}

func (svc *Service) Hostname(ctx context.Context) (string, error) {
	if svc.ProxyUpstream != nil {
		upstream, err := AllServices.Get(ctx, svc)
		if err != nil {
			return "", err
		}

		host, _, err := net.SplitHostPort(upstream.Addr)
		if err != nil {
			return "", err
		}

		return host, nil
	}

	dig, err := svc.Digest()
	if err != nil {
		return "", err
	}

	return network.HostHash(dig), nil
}

func (svc *Service) Endpoint(ctx context.Context, port int, scheme string) (string, error) {
	dig, err := svc.Digest()
	if err != nil {
		return "", err
	}

	var host string
	switch {
	case svc.Container != nil:
		host = network.HostHash(dig)
		if port == 0 {
			if len(svc.Container.Ports) == 0 {
				return "", fmt.Errorf("no ports exposed")
			}

			port = svc.Container.Ports[0].Port
		}
	case svc.ProxyUpstream != nil:
		proxy, err := AllServices.Get(ctx, svc)
		if err != nil {
			return "", err
		}

		var portStr string
		host, portStr, err = net.SplitHostPort(proxy.Addr)
		if err != nil {
			return "", err
		}

		if port == 0 {
			port, err = strconv.Atoi(portStr)
			if err != nil {
				return "", err
			}
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

func solveRef(ctx context.Context, bk *buildkit.Client, def *pb.Definition) (bkgw.Reference, error) {
	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return nil, err
	}

	// TODO(vito): is this needed anymore? had to deal with unwrapping at one point
	return res.SingleRef()
}

func (svc *Service) Start(ctx context.Context, bk *buildkit.Client, progSock string) (running *RunningService, err error) {
	switch {
	case svc.Container != nil:
		return svc.startContainer(ctx, bk, progSock)
	case svc.ProxyUpstream != nil:
		return svc.startProxy(ctx, bk, progSock)
	default:
		return nil, fmt.Errorf("unknown service type")
	}
}

func (svc *Service) startContainer(ctx context.Context, bk *buildkit.Client, progSock string) (running *RunningService, err error) {
	host, err := svc.Hostname(ctx)
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

	ctr, opts := svc.Container, svc.ContainerExecOpts

	cfg := ctr.Config

	detachDeps, _, err := StartServices(ctx, bk, ctr.Services)
	if err != nil {
		return nil, fmt.Errorf("start dependent services: %w", err)
	}

	defer func() {
		if err != nil {
			detachDeps()
		}
	}()

	mounts, err := svc.mounts(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("mounts: %w", err)
	}

	args, err := ctr.command(opts)
	if err != nil {
		return nil, fmt.Errorf("command: %w", err)
	}

	env := svc.env()

	metaEnv, err := ContainerExecUncachedMetadata{
		ParentClientIDs: clientMetadata.ClientIDs(),
		ServerID:        clientMetadata.ServerID,
		ProgSockPath:    progSock,
	}.ToEnv()
	if err != nil {
		return nil, fmt.Errorf("uncached metadata: %w", err)
	}
	env = append(env, metaEnv...)

	secretEnv, mounts, env, err := svc.secrets(mounts, env)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	var securityMode pb.SecurityMode
	if opts.InsecureRootCapabilities {
		securityMode = pb.SecurityMode_INSECURE
	}

	dig, err := svc.Digest()
	if err != nil {
		return nil, err
	}

	vtx := rec.Vertex(dig, "start "+strings.Join(args, " "))

	fullHost := host + "." + network.ClientDomain(clientMetadata.ClientID)

	health := newHealth(bk, fullHost, ctr.Ports)

	pbPlatform := pb.PlatformFromSpec(ctr.Platform)

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

	outBuf := new(bytes.Buffer)
	svcProc, err := gc.Start(ctx, bkgw.StartRequest{
		Args:         args,
		Env:          env,
		SecretEnv:    secretEnv,
		User:         cfg.User,
		Cwd:          cfg.WorkingDir,
		Tty:          false,
		Stdout:       nopCloser{io.MultiWriter(vtx.Stdout(), outBuf)},
		Stderr:       nopCloser{io.MultiWriter(vtx.Stderr(), outBuf)},
		SecurityMode: securityMode,
	})
	if err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}

	exited := make(chan error, 1)
	go func() {
		exited <- svcProc.Wait()

		// detach dependent services when process exits
		detachDeps()
	}()

	stopSvc := func(ctx context.Context) error {
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

	digest, err := svc.Digest()
	if err != nil {
		return nil, err
	}

	select {
	case err := <-checked:
		if err != nil {
			return nil, fmt.Errorf("health check errored: %w", err)
		}

		return &RunningService{
			Service: svc,
			Addr:    fullHost,
			Key: ServiceKey{
				Digest:   digest,
				ClientID: clientMetadata.ClientID,
			},
			Stop: stopSvc,
		}, nil
	case err := <-exited:
		if err != nil {
			return nil, fmt.Errorf("exited: %w\noutput: %s", err, outBuf.String())
		}

		return nil, fmt.Errorf("service exited before healthcheck")
	}
}

func (svc *Service) startProxy(ctx context.Context, bk *buildkit.Client, progSock string) (running *RunningService, err error) {
	svcCtx, stop := context.WithCancel(context.Background())

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		stop()
		return nil, err
	}
	svcCtx = engine.ContextWithClientMetadata(svcCtx, clientMetadata)

	svcCtx = progrock.ToContext(svcCtx, progrock.FromContext(ctx))

	upstream, err := AllServices.Start(svcCtx, bk, svc.ProxyUpstream)
	if err != nil {
		stop()
		return nil, fmt.Errorf("start upstream: %w", err)
	}

	// rec := rec.WithGroup(
	// 	fmt.Sprintf("proxy %s => %s", svc.ProxyListenAddress, upstream.Addr),
	// 	progrock.Weak(),
	// )

	res, closeListener, err := bk.ListenHostToContainer(
		svcCtx,
		svc.ProxyListenAddress,
		svc.ProxyProtocol.Network(),
		fmt.Sprintf("%s:%d", upstream.Addr, svc.ProxyUpstreamPort),
	)
	if err != nil {
		stop()
		return nil, fmt.Errorf("host to container: %w", err)
	}

	dig, err := svc.Digest()
	if err != nil {
		stop()
		return nil, err
	}

	return &RunningService{
		Service: svc,
		Key: ServiceKey{
			Digest:   dig,
			ClientID: clientMetadata.ClientID,
		},
		Addr: res.GetAddr(),
		Stop: func(context.Context) error {
			stop()
			// HACK(vito): do this async to prevent deadlock (this is called in Detach)
			go AllServices.Detach(svcCtx, upstream)
			return closeListener()
		},
	}, nil
}

func (svc *Service) mounts(ctx context.Context, bk *buildkit.Client) ([]bkgw.Mount, error) {
	ctr := svc.Container
	opts := svc.ContainerExecOpts

	fsRef, err := solveRef(ctx, bk, ctr.FS)
	if err != nil {
		return nil, err
	}

	mounts := []bkgw.Mount{
		{
			Dest:      "/",
			MountType: pb.MountType_BIND,
			Ref:       fsRef,
		},
	}

	metaSt, metaSourcePath := metaMount(opts.Stdin)

	metaDef, err := metaSt.Marshal(ctx, llb.Platform(ctr.Platform))
	if err != nil {
		return nil, err
	}

	metaRef, err := solveRef(ctx, bk, metaDef.ToPB())
	if err != nil {
		return nil, err
	}

	mounts = append(mounts, bkgw.Mount{
		Dest:     buildkit.MetaMountDestPath,
		Ref:      metaRef,
		Selector: metaSourcePath,
	})

	for _, mnt := range ctr.Mounts {
		mount := bkgw.Mount{
			Dest:      mnt.Target,
			MountType: pb.MountType_BIND,
		}

		if mnt.Source != nil {
			srcRef, err := solveRef(ctx, bk, mnt.Source)
			if err != nil {
				return nil, fmt.Errorf("mount %s: %w", mnt.Target, err)
			}

			mount.Ref = srcRef
		}

		if mnt.SourcePath != "" {
			mount.Selector = mnt.SourcePath
		}

		if mnt.CacheID != "" {
			mount.MountType = pb.MountType_CACHE
			mount.CacheOpt = &pb.CacheOpt{
				ID: mnt.CacheID,
			}

			switch mnt.CacheSharingMode {
			case CacheSharingModeShared:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_SHARED
			case CacheSharingModePrivate:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_PRIVATE
			case CacheSharingModeLocked:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_LOCKED
			default:
				return nil, errors.Errorf("invalid cache mount sharing mode %q", mnt.CacheSharingMode)
			}
		}

		if mnt.Tmpfs {
			mount.MountType = pb.MountType_TMPFS
		}

		mounts = append(mounts, mount)
	}

	for _, ctrSocket := range ctr.Sockets {
		if ctrSocket.UnixPath == "" {
			return nil, fmt.Errorf("unsupported socket: only unix paths are implemented")
		}

		opt := &pb.SSHOpt{
			ID: ctrSocket.SocketID.String(),
		}

		if ctrSocket.Owner != nil {
			opt.Uid = uint32(ctrSocket.Owner.UID)
			opt.Gid = uint32(ctrSocket.Owner.UID)
			opt.Mode = 0o600 // preserve default
		}

		mounts = append(mounts, bkgw.Mount{
			Dest:      ctrSocket.UnixPath,
			MountType: pb.MountType_SSH,
			SSHOpt:    opt,
		})
	}

	return mounts, nil
}

func (svc *Service) env() []string {
	ctr := svc.Container
	opts := svc.ContainerExecOpts
	cfg := ctr.Config

	env := []string{}

	for _, e := range cfg.Env {
		// strip out any env that are meant for internal use only, to prevent
		// manually setting them
		switch {
		case strings.HasPrefix(e, "_DAGGER_ENABLE_NESTING="):
		default:
			env = append(env, e)
		}
	}

	if opts.ExperimentalPrivilegedNesting {
		env = append(env, "_DAGGER_ENABLE_NESTING=")
	}

	if opts.RedirectStdout != "" {
		env = append(env, "_DAGGER_REDIRECT_STDOUT="+opts.RedirectStdout)
	}

	if opts.RedirectStderr != "" {
		env = append(env, "_DAGGER_REDIRECT_STDERR="+opts.RedirectStderr)
	}

	for _, bnd := range ctr.Services {
		for _, alias := range bnd.Aliases {
			env = append(env, "_DAGGER_HOSTNAME_ALIAS_"+alias+"="+bnd.Hostname)
		}
	}

	return env
}

func (svc *Service) secrets(mounts []bkgw.Mount, env []string) ([]*pb.SecretEnv, []bkgw.Mount, []string, error) {
	ctr := svc.Container

	secretEnv := []*pb.SecretEnv{}
	secretsToScrub := SecretToScrubInfo{}
	for i, ctrSecret := range ctr.Secrets {
		switch {
		case ctrSecret.EnvName != "":
			secretsToScrub.Envs = append(secretsToScrub.Envs, ctrSecret.EnvName)
			secret, err := ctrSecret.Secret.ToSecret()
			if err != nil {
				return nil, nil, nil, err
			}
			secretEnv = append(secretEnv, &pb.SecretEnv{
				ID:   secret.Name,
				Name: ctrSecret.EnvName,
			})
		case ctrSecret.MountPath != "":
			secretsToScrub.Files = append(secretsToScrub.Files, ctrSecret.MountPath)
			opt := &pb.SecretOpt{}
			if ctrSecret.Owner != nil {
				opt.Uid = uint32(ctrSecret.Owner.UID)
				opt.Gid = uint32(ctrSecret.Owner.UID)
				opt.Mode = 0o400 // preserve default
			}
			mounts = append(mounts, bkgw.Mount{
				Dest:      ctrSecret.MountPath,
				MountType: pb.MountType_SECRET,
				SecretOpt: opt,
			})
		default:
			return nil, nil, nil, fmt.Errorf("malformed secret config at index %d", i)
		}
	}

	if len(secretsToScrub.Envs) != 0 || len(secretsToScrub.Files) != 0 {
		// we sort to avoid non-deterministic order that would break caching
		sort.Strings(secretsToScrub.Envs)
		sort.Strings(secretsToScrub.Files)

		secretsToScrubJSON, err := json.Marshal(secretsToScrub)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("scrub secrets json: %w", err)
		}
		env = append(env, "_DAGGER_SCRUB_SECRETS="+string(secretsToScrubJSON))
	}

	return secretEnv, mounts, env, nil
}

type RunningService struct {
	Service *Service
	Key     ServiceKey
	Addr    string
	Stop    func(context.Context) error
}

type Services struct {
	starting map[ServiceKey]*sync.WaitGroup
	running  map[ServiceKey]*RunningService
	bindings map[ServiceKey]int
	l        sync.Mutex
}

type ServiceKey struct {
	Digest   digest.Digest
	ClientID string
}

// AllServices is a pesky global variable storing the state of all running
// services.
var AllServices = &Services{
	starting: map[ServiceKey]*sync.WaitGroup{},
	running:  map[ServiceKey]*RunningService{},
	bindings: map[ServiceKey]int{},
}

func (ss *Services) Get(ctx context.Context, svc *Service) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	dig, err := svc.Digest()
	if err != nil {
		return nil, err
	}

	key := ServiceKey{
		Digest:   dig,
		ClientID: clientMetadata.ClientID,
	}

	notRunningErr := fmt.Errorf("service %s is not running", network.HostHash(dig))

	ss.l.Lock()
	starting, isStarting := ss.starting[key]
	running, isRunning := ss.running[key]
	switch {
	case !isStarting && !isRunning:
		return nil, notRunningErr
	case isRunning:
		ss.l.Unlock()
		return running, nil
	case isStarting:
		ss.l.Unlock()
		starting.Wait()
		ss.l.Lock()
		running, isRunning = ss.running[key]
		ss.l.Unlock()
		if isRunning {
			return running, nil
		}
		return nil, notRunningErr
	default:
		return nil, fmt.Errorf("internal error: unexpected state")
	}
}

func (ss *Services) Start(ctx context.Context, bk *buildkit.Client, svc *Service) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	dig, err := svc.Digest()
	if err != nil {
		return nil, err
	}

	key := ServiceKey{
		Digest:   dig,
		ClientID: clientMetadata.ClientID,
	}

	// XXX(vito): hacky; aligned with engine/server/router.go
	progSockPath := fmt.Sprintf("/run/dagger/server-progrock-%s.sock", clientMetadata.ServerID)

	ss.l.Lock()
	starting, isStarting := ss.starting[key]
	running, isRunning := ss.running[key]
	switch {
	case !isStarting && !isRunning:
		// not starting or running; start it
		starting = new(sync.WaitGroup)
		starting.Add(1)
		defer starting.Done()
		ss.starting[key] = starting
	case isRunning:
		// already running; increment binding count and return
		ss.bindings[key]++
		ss.l.Unlock()
		return running, nil
	case isStarting:
		// already starting; wait for the attempt to finish and check if it
		// succeeded
		ss.l.Unlock()
		starting.Wait()
		ss.l.Lock()
		running, didStart := ss.running[key]
		if didStart {
			// starting succeeded as normal; return the isntance
			ss.l.Unlock()
			return running, nil
		}
		// starting didn't work; give it another go (this might just error again)
	}
	ss.l.Unlock()

	svcCtx, stop := context.WithCancel(context.Background())
	svcCtx = progrock.ToContext(svcCtx, progrock.FromContext(ctx))
	if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil {
		svcCtx = engine.ContextWithClientMetadata(svcCtx, clientMetadata)
	}

	running, err = svc.Start(svcCtx, bk, progSockPath)
	if err != nil {
		stop()
		ss.l.Lock()
		delete(ss.starting, key)
		ss.l.Unlock()
		return nil, err
	}

	ss.l.Lock()
	delete(ss.starting, key)
	ss.running[key] = running
	ss.bindings[key] = 1
	ss.l.Unlock()

	_ = stop // leave it running

	return running, nil
}

func (ss *Services) Stop(ctx context.Context, bk *buildkit.Client, svc *Service) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	dig, err := svc.Digest()
	if err != nil {
		return err
	}

	key := ServiceKey{
		Digest:   dig,
		ClientID: clientMetadata.ClientID,
	}

	ss.l.Lock()
	defer ss.l.Unlock()

	starting, isStarting := ss.starting[key]
	running, isRunning := ss.running[key]
	switch {
	case isRunning:
		// already running; increment binding count and return
		return ss.stop(ctx, running)
	case isStarting:
		// already starting; wait for the attempt to finish and then stop it
		ss.l.Unlock()
		starting.Wait()
		ss.l.Lock()

		running, didStart := ss.running[key]
		if didStart {
			// starting succeeded as normal; return the isntance
			return ss.stop(ctx, running)
		}

		// starting didn't work; nothing to do
		return nil
	default:
		// not starting or running; nothing to do
		return nil
	}
}

func (ss *Services) Detach(ctx context.Context, svc *RunningService) error {
	ss.l.Lock()
	defer ss.l.Unlock()

	running, found := ss.running[svc.Key]
	if !found {
		// not even running; ignore
		return nil
	}

	ss.bindings[svc.Key]--

	if ss.bindings[svc.Key] > 0 {
		// detached, but other instances still active
		return nil
	}

	return ss.stop(ctx, running)
}

func (ss *Services) stop(ctx context.Context, running *RunningService) error {
	if err := running.Stop(ctx); err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	delete(ss.bindings, running.Key)
	delete(ss.running, running.Key)

	return nil
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

// NetworkProtocol is a string deriving from NetworkProtocol enum
type NetworkProtocol string

const (
	NetworkProtocolTCP NetworkProtocol = "TCP"
	NetworkProtocolUDP NetworkProtocol = "UDP"
)

// Network returns the value appropriate for the "network" argument to Go
// net.Dial, and for appending to the port number to form the key for the
// ExposedPorts object in the OCI image config.
func (p NetworkProtocol) Network() string {
	return strings.ToLower(string(p))
}

func StartServices(ctx context.Context, bk *buildkit.Client, bindings ServiceBindings) (_ func(), _ []*RunningService, err error) {
	running := []*RunningService{}
	detach := func() {
		go func() {
			<-time.After(10 * time.Second)

			for _, svc := range running {
				AllServices.Detach(ctx, svc)
			}
		}()
	}

	defer func() {
		if err != nil {
			detach()
		}
	}()

	// NB: don't use errgroup.WithCancel; we don't want to cancel on Wait
	eg := new(errgroup.Group)

	started := make(chan *RunningService, len(bindings))
	for _, bnd := range bindings {
		bnd := bnd
		eg.Go(func() error {
			runningSvc, err := AllServices.Start(ctx, bk, bnd.Service)
			if err != nil {
				return fmt.Errorf("start %s (%s): %w", bnd.Hostname, bnd.Aliases, err)
			}
			started <- runningSvc
			return nil
		})
	}

	startErr := eg.Wait()

	close(started)

	if startErr != nil {
		return nil, nil, startErr
	}

	for svc := range started {
		running = append(running, svc)
	}

	return detach, running, nil
}

// WithServices runs the given function with the given services started,
// detaching from each of them after the function completes.
func WithServices[T any](ctx context.Context, bk *buildkit.Client, bindings ServiceBindings, fn func() (T, error)) (T, error) {
	var zero T

	detach, _, err := StartServices(ctx, bk, bindings)
	if err != nil {
		return zero, err
	}
	defer detach()

	return fn()
}

type portHealthChecker struct {
	bk    *buildkit.Client
	host  string
	ports []ContainerPort
}

func newHealth(bk *buildkit.Client, host string, ports []ContainerPort) *portHealthChecker {
	return &portHealthChecker{
		bk:    bk,
		host:  host,
		ports: ports,
	}
}

type marshalable interface {
	Marshal(ctx context.Context, co ...llb.ConstraintsOpt) (*llb.Definition, error)
}

func result(ctx context.Context, bk *buildkit.Client, st marshalable) (*buildkit.Result, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	return bk.Solve(ctx, bkgw.SolveRequest{
		Definition: def.ToPB(),
	})
}

func (d *portHealthChecker) Check(ctx context.Context) (err error) {
	rec := progrock.FromContext(ctx)

	args := []string{"check", d.host}
	for _, port := range d.ports {
		args = append(args, fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network()))
	}

	// show health-check logs in a --debug vertex
	vtx := rec.Vertex(
		digest.Digest(identity.NewID()),
		strings.Join(args, " "),
		progrock.Internal(),
	)
	defer func() {
		vtx.Done(err)
	}()

	scratchRes, err := result(ctx, d.bk, llb.Scratch())
	if err != nil {
		return err
	}

	container, err := d.bk.NewContainer(ctx, bkgw.NewContainerRequest{
		Mounts: []bkgw.Mount{
			{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       scratchRes.Ref,
			},
		},
	})
	if err != nil {
		return err
	}

	// NB: use a different ctx than the one that'll be interrupted for anything
	// that needs to run as part of post-interruption cleanup
	cleanupCtx := context.Background()

	defer container.Release(cleanupCtx)

	proc, err := container.Start(ctx, bkgw.StartRequest{
		Args:   args,
		Env:    []string{"_DAGGER_INTERNAL_COMMAND="},
		Stdout: nopCloser{vtx.Stdout()},
		Stderr: nopCloser{vtx.Stderr()},
	})
	if err != nil {
		return err
	}

	exited := make(chan error, 1)
	go func() {
		exited <- proc.Wait()
	}()

	select {
	case err := <-exited:
		if err != nil {
			return err
		}

		return nil
	case <-ctx.Done():
		err := proc.Signal(cleanupCtx, syscall.SIGKILL)
		if err != nil {
			return fmt.Errorf("interrupt check: %w", err)
		}

		<-exited

		return ctx.Err()
	}
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error {
	return nil
}
