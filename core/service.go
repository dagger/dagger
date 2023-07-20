package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dagger/dagger/core/pipeline"
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

// DaggerNetwork is the ID of the network used for the Buildkit networks
// session attachable.
const DaggerNetwork = "dagger"

type Service struct {
	// Container is the container that this service is running in.
	Container *Container `json:"container"`

	// Exec is the service command to run in the container.
	Exec ContainerExecOpts `json:"exec"`
}

func NewService(ctr *Container, opts ContainerExecOpts) (*Service, error) {
	return &Service{
		Container: ctr,
		Exec:      opts,
	}, nil
}

type ServiceID string

func (id ServiceID) String() string {
	return string(id)
}

// ServiceID is digestible so that smaller hashes can be displayed in
// --debug vertex names.
var _ Digestible = ServiceID("")

func (id ServiceID) Digest() (digest.Digest, error) {
	return digest.FromString(id.String()), nil
}

func (id ServiceID) ToService() (*Service, error) {
	var service Service

	if id == "" {
		// scratch
		return &service, nil
	}

	if err := decodeID(&service, id); err != nil {
		return nil, err
	}

	return &service, nil
}

// ID marshals the service into a content-addressed ID.
func (svc *Service) ID() (ServiceID, error) {
	return encodeID[ServiceID](svc)
}

var _ pipeline.Pipelineable = (*Service)(nil)

// PipelinePath returns the service's pipeline path.
func (svc *Service) PipelinePath() pipeline.Path {
	return svc.Container.Pipeline
}

// Service is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ Digestible = (*Service)(nil)

// Digest returns the service's content hash.
func (svc *Service) Digest() (digest.Digest, error) {
	return stableDigest(svc)
}

func (svc *Service) Hostname() (string, error) {
	dig, err := svc.Digest()
	if err != nil {
		return "", err
	}
	return network.HostHash(dig), nil
}

func (svc *Service) Endpoint(port int, scheme string) (string, error) {
	if port == 0 {
		if len(svc.Container.Ports) == 0 {
			return "", fmt.Errorf("no ports exposed")
		}

		port = svc.Container.Ports[0].Port
	}

	host, err := svc.Hostname()
	if err != nil {
		return "", err
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

func (svc *Service) Start(ctx context.Context, bk *buildkit.Client, progSock *Socket) (running *RunningService, err error) {
	ctr := svc.Container
	cfg := ctr.Config
	opts := svc.Exec

	detachDeps, _, err := StartServices(ctx, bk, ctr.Services)
	if err != nil {
		return nil, fmt.Errorf("start dependent services: %w", err)
	}

	defer func() {
		if err != nil {
			detachDeps()
		}
	}()

	mounts, err := svc.mounts(ctx, bk, progSock)
	if err != nil {
		return nil, fmt.Errorf("mounts: %w", err)
	}

	// XXX(vito): add resolv.conf mount

	args, err := ctr.command(opts)
	if err != nil {
		return nil, fmt.Errorf("command: %w", err)
	}

	env := svc.env()

	secretEnv, mounts, env, err := svc.secrets(mounts, env)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	var securityMode pb.SecurityMode
	if opts.InsecureRootCapabilities {
		securityMode = pb.SecurityMode_INSECURE
	}

	rec := progrock.RecorderFromContext(ctx)

	dig, err := svc.Digest()
	if err != nil {
		return nil, err
	}

	host, err := svc.Hostname()
	if err != nil {
		return nil, err
	}

	vtx := rec.Vertex(dig, "start "+strings.Join(args, " "))

	fullHost := host + "." + network.SessionDomain(bk.ID())

	health := newHealth(bk, fullHost, svc.Container.Ports)

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

	svcProc, err := gc.Start(ctx, bkgw.StartRequest{
		Args:         args,
		Env:          env,
		SecretEnv:    secretEnv,
		User:         cfg.User,
		Cwd:          cfg.WorkingDir,
		Tty:          false,
		Stdout:       nopCloser{vtx.Stdout()},
		Stderr:       nopCloser{vtx.Stderr()},
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

	select {
	case err := <-checked:
		if err != nil {
			return nil, fmt.Errorf("health check errored: %w", err)
		}

		return &RunningService{
			Service:   svc,
			Hostname:  host,
			Container: gc,
			Process:   svcProc,
		}, nil
	case err := <-exited:
		if err != nil {
			return nil, fmt.Errorf("exited: %w", err)
		}

		return nil, fmt.Errorf("service exited before healthcheck")
	}
}

func (svc *Service) mounts(ctx context.Context, bk *buildkit.Client, progSock *Socket) ([]bkgw.Mount, error) {
	ctr := svc.Container
	opts := svc.Exec

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

	if opts.ExperimentalPrivilegedNesting {
		sid, err := progSock.ID()
		if err != nil {
			return nil, err
		}

		mounts = append(mounts, bkgw.Mount{
			Dest:      "/.progrock.sock",
			MountType: pb.MountType_SSH,
			SSHOpt: &pb.SSHOpt{
				ID: sid.LLBID(),
			},
		})
	}

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

	for _, socket := range ctr.Sockets {
		if socket.UnixPath == "" {
			return nil, fmt.Errorf("unsupported socket: only unix paths are implemented")
		}

		opt := &pb.SSHOpt{
			ID: socket.Socket.LLBID(),
		}

		if socket.Owner != nil {
			opt.Uid = uint32(socket.Owner.UID)
			opt.Gid = uint32(socket.Owner.UID)
			opt.Mode = 0o600 // preserve default
		}

		mounts = append(mounts, bkgw.Mount{
			Dest:      socket.UnixPath,
			MountType: pb.MountType_SSH,
			SSHOpt:    opt,
		})
	}

	return mounts, nil
}

func (svc *Service) env() []string {
	ctr := svc.Container
	opts := svc.Exec
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

	for _, alias := range ctr.HostAliases {
		env = append(env, "_DAGGER_HOSTNAME_ALIAS_"+alias.Alias+"="+alias.Target)
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
	Service   *Service
	Hostname  string
	Container bkgw.Container
	Process   bkgw.ContainerProcess
}

type Services struct {
	progSock *Socket
	starting map[string]*sync.WaitGroup
	running  map[string]*RunningService
	bindings map[string]int
	l        sync.Mutex
}

// AllServices is a pesky global variable storing the state of all running
// services.
var AllServices *Services

func InitServices(progSockPath string) {
	AllServices = &Services{
		progSock: NewHostSocket(progSockPath),
		starting: map[string]*sync.WaitGroup{},
		running:  map[string]*RunningService{},
		bindings: map[string]int{},
	}
}

func (ss *Services) Service(host string) (*RunningService, bool) {
	ss.l.Lock()
	defer ss.l.Unlock()

	svc, ok := ss.running[host]
	return svc, ok
}

func (ss *Services) Start(ctx context.Context, bk *buildkit.Client, svc *Service) (*RunningService, error) {
	host, err := svc.Hostname()
	if err != nil {
		return nil, err
	}

	ss.l.Lock()
	starting, isStarting := ss.starting[host]
	running, isRunning := ss.running[host]
	switch {
	case !isStarting && !isRunning:
		// not starting or running; start it
		starting = new(sync.WaitGroup)
		starting.Add(1)
		defer starting.Done()
		ss.starting[host] = starting
	case isRunning:
		// already running; increment binding count and return
		ss.bindings[host]++
		ss.l.Unlock()
		return running, nil
	case isStarting:
		// already starting; wait for the attempt to finish and check if it
		// succeeded
		ss.l.Unlock()
		starting.Wait()
		ss.l.Lock()
		running, didStart := ss.running[host]
		if didStart {
			// starting succeeded as normal; return the isntance
			ss.l.Unlock()
			return running, nil
		}
		// starting didn't work; give it another go (this might just error again)
	}
	ss.l.Unlock()

	rec := progrock.RecorderFromContext(ctx).
		WithGroup(
			fmt.Sprintf("service %s", host),
			progrock.Weak(),
		)

	svcCtx, stop := context.WithCancel(context.Background())
	svcCtx = progrock.RecorderToContext(svcCtx, rec)

	running, err = svc.Start(svcCtx, bk, ss.progSock)
	if err != nil {
		stop()
		ss.l.Lock()
		delete(ss.starting, host)
		ss.l.Unlock()
		return nil, err
	}

	ss.l.Lock()
	delete(ss.starting, host)
	ss.running[host] = running
	ss.bindings[host] = 1
	ss.l.Unlock()

	_ = stop // leave it running

	return running, nil
}

func (ss *Services) Detach(ctx context.Context, svc *RunningService) error {
	ss.l.Lock()
	defer ss.l.Unlock()

	running, found := ss.running[svc.Hostname]
	if !found {
		// not even running; ignore
		return nil
	}

	ss.bindings[svc.Hostname]--

	if ss.bindings[svc.Hostname] > 0 {
		// detached, but other instances still active
		return nil
	}

	// TODO: graceful shutdown?
	if err := running.Process.Signal(ctx, syscall.SIGKILL); err != nil {
		return err
	}

	if err := running.Container.Release(ctx); err != nil {
		return err
	}

	delete(ss.bindings, svc.Hostname)
	delete(ss.running, svc.Hostname)

	return nil
}

type ServiceBindings map[ServiceID]AliasSet

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

func (bndp *ServiceBindings) Merge(other ServiceBindings) {
	if *bndp == nil {
		*bndp = ServiceBindings{}
	}

	bnd := *bndp

	for id, aliases := range other {
		if len(bnd[id]) == 0 {
			bnd[id] = aliases
		} else {
			for _, alias := range aliases {
				bnd[id] = bnd[id].With(alias)
			}
		}
	}
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
	for svcID, aliases := range bindings {
		svc, err := svcID.ToService()
		if err != nil {
			return nil, nil, err
		}

		host, err := svc.Hostname()
		if err != nil {
			return nil, nil, err
		}

		aliases := aliases
		eg.Go(func() error {
			runningSvc, err := AllServices.Start(ctx, bk, svc)
			if err != nil {
				return fmt.Errorf("start %s (%s): %w", host, aliases, err)
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
	rec := progrock.RecorderFromContext(ctx)

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
