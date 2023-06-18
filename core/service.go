package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

// ServicesSecretPrefix is the prefix for the secret used to populate a
// container's /etc/hosts with services at runtime, without busting the cache.
const ServicesSecretPrefix = "services:"

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
var _ router.Digestible = ServiceID("")

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

var _ router.Pipelineable = (*Service)(nil)

// PipelinePath returns the service's pipeline path.
func (svc *Service) PipelinePath() pipeline.Path {
	return svc.Container.Pipeline
}

// Service is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ router.Digestible = (*Service)(nil)

// Digest returns the service's content hash.
func (svc *Service) Digest() (digest.Digest, error) {
	return stableDigest(svc)
}

func (svc *Service) Hostname() (string, error) {
	dig, err := svc.Digest()
	if err != nil {
		return "", err
	}
	return hostHash(dig), nil
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

func solveRef(ctx context.Context, gw bkgw.Client, def *pb.Definition) (bkgw.Reference, error) {
	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return nil, err
	}

	if wr, ok := res.Ref.(WrappedRef); ok {
		return wr.Unwrap(), nil
	}

	return res.Ref, nil
}

// goling: nocyclo
func (svc *Service) Start(ctx context.Context, gw bkgw.Client, progSock *Socket) (running *RunningService, err error) {
	ctr := svc.Container
	opts := svc.Exec

	detachDeps, runningDeps, err := StartServices(ctx, gw, ctr.Services)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			detachDeps()
		}
	}()

	fsRef, err := solveRef(ctx, gw, ctr.FS)
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

	pbPlatform := pb.PlatformFromSpec(ctr.Platform)

	args, err := ctr.command(opts)
	if err != nil {
		return nil, err
	}

	cfg := ctr.Config

	env := []string{}
	for _, e := range cfg.Env {
		// strip out any env that are meant for internal use only, to prevent
		// manually setting them
		switch {
		case strings.HasPrefix(e, "_DAGGER_ENABLE_NESTING="):
		case strings.HasPrefix(e, DebugFailedExecEnv+"="):
		default:
			env = append(env, e)
		}
	}

	secretEnv := []*pb.SecretEnv{}
	secretsToScrub := SecretToScrubInfo{}
	for i, ctrSecret := range ctr.Secrets {
		switch {
		case ctrSecret.EnvName != "":
			secretsToScrub.Envs = append(secretsToScrub.Envs, ctrSecret.EnvName)
			secret, err := ctrSecret.Secret.ToSecret()
			if err != nil {
				return nil, err
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
			return nil, fmt.Errorf("malformed secret config at index %d", i)
		}
	}

	if len(secretsToScrub.Envs) != 0 || len(secretsToScrub.Files) != 0 {
		// we sort to avoid non-deterministic order that would break caching
		sort.Strings(secretsToScrub.Envs)
		sort.Strings(secretsToScrub.Files)

		secretsToScrubJSON, err := json.Marshal(secretsToScrub)
		if err != nil {
			return nil, fmt.Errorf("scrub secrets json: %w", err)
		}
		env = append(env, "_DAGGER_SCRUB_SECRETS="+string(secretsToScrubJSON))
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

	for _, mnt := range ctr.Mounts {
		mount := bkgw.Mount{
			Dest:      mnt.Target,
			MountType: pb.MountType_BIND,
		}

		if mnt.Source != nil {
			srcRef, err := solveRef(ctx, gw, mnt.Source)
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

	var stdin io.ReadCloser
	if opts.Stdin != "" {
		stdin = io.NopCloser(strings.NewReader(opts.Stdin))
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

	extraHosts := make([]*pb.HostIP, len(runningDeps))
	for i, svc := range runningDeps {
		extraHosts[i] = &pb.HostIP{
			Host: svc.Hostname,
			IP:   svc.IP.String(),
		}
	}

	gc, err := gw.NewContainer(ctx, bkgw.NewContainerRequest{
		Mounts: mounts,
		// NB(vito): we don't actually need to set a hostname, since client
		// containers are given a static host -> IP mapping.
		//
		// we could set it anyway, but I don't want to until all the DNS infra is
		// ripped out, just to make sure nothing ever relies on it.
		// Hostname: host,
		Platform:   &pbPlatform,
		ExtraHosts: extraHosts,
	})
	if err != nil {
		return nil, err
	}

	ipBuf := new(bytes.Buffer)
	ipProc, err := gc.Start(ctx, bkgw.StartRequest{
		Args:   []string{"ip"},
		Env:    []string{"_DAGGER_INTERNAL_COMMAND=1"},
		Stdout: nopCloser{ipBuf},
	})
	if err != nil {
		return nil, err
	}
	if err := ipProc.Wait(); err != nil {
		return nil, fmt.Errorf("get ip: %w", err)
	}

	ipStr := strings.TrimSpace(ipBuf.String())
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, fmt.Errorf("parse ip %q: %w", ipStr, err)
	}

	proc, err := gc.Start(ctx, bkgw.StartRequest{
		Args:         args,
		Env:          env,
		SecretEnv:    secretEnv,
		User:         cfg.User,
		Cwd:          cfg.WorkingDir,
		Tty:          false,
		Stdin:        stdin,
		Stdout:       nopCloser{vtx.Stdout()},
		Stderr:       nopCloser{vtx.Stderr()},
		SecurityMode: securityMode,
	})
	if err != nil {
		return nil, err
	}

	go func() {
		// Keep dependent services running so long as the service is running.
		_ = proc.Wait()
		detachDeps()
	}()

	return &RunningService{
		Service:   svc,
		IP:        ip,
		Hostname:  host,
		Container: gc,
		Process:   proc,
	}, nil
}

type RunningService struct {
	Service   *Service
	Hostname  string
	IP        net.IP
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

func (ss *Services) Start(ctx context.Context, gw bkgw.Client, svc *Service) (*RunningService, error) {
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

	health := newHealth(gw, host, svc.Container.Ports)

	rec := progrock.RecorderFromContext(ctx).
		WithGroup(
			fmt.Sprintf("service %s", host),
			progrock.Weak(),
		)

	svcCtx, stop := context.WithCancel(context.Background())
	svcCtx = progrock.RecorderToContext(svcCtx, rec)

	running, err = svc.Start(svcCtx, gw, ss.progSock)
	if err != nil {
		stop()
		return nil, err
	}

	checked := make(chan error, 1)
	go func() {
		checked <- health.Check(svcCtx)
	}()

	exited := make(chan error, 1)
	go func() {
		exited <- running.Process.Wait()
	}()

	select {
	case err := <-checked:
		if err != nil {
			stop()
			return nil, fmt.Errorf("health check errored: %w", err)
		}

		ss.l.Lock()
		delete(ss.starting, host)
		ss.running[host] = running
		ss.bindings[host] = 1
		ss.l.Unlock()

		_ = stop // leave it running

		return running, nil
	case err := <-exited:
		stop() // interrupt healthcheck

		ss.l.Lock()
		delete(ss.starting, host)
		ss.l.Unlock()

		if err != nil {
			return nil, fmt.Errorf("exited: %w", err)
		}

		return nil, fmt.Errorf("service exited before healthcheck")
	}
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

func StartServices(ctx context.Context, gw bkgw.Client, bindings ServiceBindings) (_ func(), _ []*RunningService, err error) {
	running := make([]*RunningService, len(bindings))
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
			running, err := AllServices.Start(ctx, gw, svc)
			if err != nil {
				return fmt.Errorf("start %s (%s): %w", host, aliases, err)
			}
			started <- running
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
func WithServices[T any](ctx context.Context, gw bkgw.Client, bindings ServiceBindings, fn func() (T, error)) (T, error) {
	var zero T

	detach, _, err := StartServices(ctx, gw, bindings)
	if err != nil {
		return zero, err
	}
	defer detach()

	return fn()
}

type portHealthChecker struct {
	gw    bkgw.Client
	host  string
	ports []ContainerPort
}

func newHealth(gw bkgw.Client, host string, ports []ContainerPort) *portHealthChecker {
	return &portHealthChecker{
		gw:    gw,
		host:  host,
		ports: ports,
	}
}

type marshalable interface {
	Marshal(ctx context.Context, co ...llb.ConstraintsOpt) (*llb.Definition, error)
}

func result(ctx context.Context, gw bkgw.Client, st marshalable) (*bkgw.Result, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	return gw.Solve(ctx, bkgw.SolveRequest{
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

	scratchRes, err := result(ctx, d.gw, llb.Scratch())
	if err != nil {
		return err
	}

	container, err := d.gw.NewContainer(ctx, bkgw.NewContainerRequest{
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
