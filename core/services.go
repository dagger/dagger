package core

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"time"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/network"
)

const (
	// DetachGracePeriod is an arbitrary amount of time between when a service is
	// no longer actively used and before it is detached. This is to avoid repeated
	// stopping and re-starting of the same service in rapid succession.
	DetachGracePeriod = 10 * time.Second

	// TerminateGracePeriod is an arbitrary amount of time between when a service is
	// sent a graceful stop (SIGTERM) and when it is sent an immediate stop (SIGKILL).
	TerminateGracePeriod = 10 * time.Second
)

// Services manages the lifecycle of services, ensuring the same service only
// runs once per client.
type Services struct {
	starting map[ServiceKey]*startingService
	running  map[ServiceKey]*RunningService
	bindings map[ServiceKey]int
	l        sync.Mutex
}

type startingService struct {
	running *RunningService

	ctx    context.Context
	cancel context.CancelCauseFunc

	done chan struct{}
	err  error
}

// RunningService represents a service that is actively running.
type RunningService struct {
	// Key is the unique identifier for the service.
	Key ServiceKey

	// Host is the hostname used to reach the service.
	Host string

	// Ports lists the ports bound by the service.
	//
	// For a Container service, this is simply the list of exposed ports.
	//
	// For a TunnelService, this lists the configured port forwards with any
	// empty or 0 frontend ports resolved to their randomly selected host port.
	//
	// For a HostService, this lists the configured port forwards with any empty
	// or 0 frontend ports set to the same as the backend port.
	Ports []Port

	// Stop forcibly stops the service. It is normally called after all clients
	// have detached, but may also be called manually by the user.
	Stop func(ctx context.Context, force bool) error

	// Wait blocks until the service has exited or the provided context is canceled.
	Wait func(ctx context.Context) error

	// Exec runs a command in the service. It is only supported for services
	// with a backing container.
	Exec func(ctx context.Context, cmd []string, env []string, io *ServiceIO) error

	// The runc container ID, if any
	ContainerID string

	refsMu sync.Mutex
	refs   []bkcache.Ref

	workspaceMu sync.Mutex

	manager *Services

	stopOnce sync.Once
}

// ServiceKey is a unique identifier for a service.
type ServiceRuntimeKind string

const (
	ServiceRuntimeShared      ServiceRuntimeKind = "shared"
	ServiceRuntimeInteractive ServiceRuntimeKind = "interactive"
)

type ServiceKey struct {
	Digest     digest.Digest
	SessionID  string
	ClientID   string
	Kind       ServiceRuntimeKind
	InstanceID string
}

// NewServices returns a new Services.
func NewServices() *Services {
	return &Services{
		starting: map[ServiceKey]*startingService{},
		running:  map[ServiceKey]*RunningService{},
		bindings: map[ServiceKey]int{},
	}
}

// Get returns the running service for the given service. If the service is
// starting, it waits for it and either returns the running service or an error
// if it failed to start. If the service is not running or starting, an error
// is returned.
func (ss *Services) Get(ctx context.Context, dig digest.Digest, clientSpecific bool) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if dig == "" {
		return nil, fmt.Errorf("service digest is empty")
	}

	key := ServiceKey{
		Digest:    dig,
		SessionID: clientMetadata.SessionID,
		Kind:      ServiceRuntimeShared,
	}
	if clientSpecific {
		key.ClientID = clientMetadata.ClientID
	}

	notRunningErr := fmt.Errorf("service %s is not running", network.HostHash(dig))

	for {
		ss.l.Lock()
		starting, isStarting := ss.starting[key]
		running, isRunning := ss.running[key]
		ss.l.Unlock()

		switch {
		case isRunning:
			return running, nil
		case isStarting:
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case <-starting.done:
			}
		default:
			return nil, notRunningErr
		}
	}
}

type Startable interface {
	Start(
		ctx context.Context,
		running *RunningService,
		digest digest.Digest,
		io *ServiceIO,
	) error
}

// Start starts the given service, returning the running service. If the
// service is already running, it is returned immediately. If the service is
// already starting, it waits for it to finish and returns the running service.
// If the service failed to start, it tries again.
func (ss *Services) Start(ctx context.Context, dig digest.Digest, svc Startable, clientSpecific bool) (*RunningService, error) {
	return ss.StartWithIO(ctx, dig, svc, clientSpecific, nil)
}

func (ss *Services) StartWithIO(
	ctx context.Context,
	dig digest.Digest,
	svc Startable,
	clientSpecific bool,
	sio *ServiceIO,
) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if dig == "" {
		return nil, fmt.Errorf("service digest is empty")
	}
	key := ServiceKey{
		Digest:    dig,
		SessionID: clientMetadata.SessionID,
		Kind:      ServiceRuntimeShared,
	}
	if clientSpecific {
		key.ClientID = clientMetadata.ClientID
	}

	running, _, err := ss.startWithKey(ctx, key, svc, sio)
	return running, err
}

func (ss *Services) StartInteractive(
	ctx context.Context,
	dig digest.Digest,
	svc Startable,
	sio *ServiceIO,
) (_ *RunningService, release func(), err error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if dig == "" {
		return nil, nil, fmt.Errorf("service digest is empty")
	}
	key := ServiceKey{
		Digest:     dig,
		SessionID:  clientMetadata.SessionID,
		ClientID:   clientMetadata.ClientID,
		Kind:       ServiceRuntimeInteractive,
		InstanceID: identity.NewID(),
	}
	return ss.startWithKey(ctx, key, svc, sio)
}

// StartBindings starts each of the bound services in parallel and returns a
// function that will detach from all of them after 10 seconds.
func (ss *Services) StartBindings(ctx context.Context, bindings ServiceBindings) (_ func(), _ []*RunningService, err error) {
	running := make([]*RunningService, len(bindings))
	detachOnce := sync.Once{}
	detach := func() {
		detachOnce.Do(func() {
			go func() {
				<-time.After(DetachGracePeriod)
				for _, svc := range running {
					if svc != nil {
						ss.Detach(ctx, svc)
					}
				}
			}()
		})
	}

	// NB: don't use errgroup.WithCancel; we don't want to cancel on Wait
	eg := new(errgroup.Group)
	for i, bnd := range bindings {
		eg.Go(func() error {
			serviceDig, err := bnd.Service.ContentPreferredDigest(ctx)
			if err != nil {
				return fmt.Errorf("service %s content-preferred digest: %w", bnd.Hostname, err)
			}
			runningSvc, err := ss.Start(ctx, serviceDig, bnd.Service.Self(), false)
			if err != nil {
				return fmt.Errorf("start %s (%s): %w", bnd.Hostname, bnd.Aliases, err)
			}
			running[i] = runningSvc
			return nil
		})
	}

	startErr := eg.Wait()
	if startErr != nil {
		detach()
		return nil, nil, startErr
	}

	return detach, running, nil
}

// Stop stops the given service. If the service is not running, it is a no-op.
func (ss *Services) Stop(ctx context.Context, dig digest.Digest, kill bool, clientSpecific bool) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	if dig == "" {
		return fmt.Errorf("service digest is empty")
	}
	key := ServiceKey{
		Digest:    dig,
		SessionID: clientMetadata.SessionID,
		Kind:      ServiceRuntimeShared,
	}
	if clientSpecific {
		key.ClientID = clientMetadata.ClientID
	}

	ss.l.Lock()
	starting, isStarting := ss.starting[key]
	running, isRunning := ss.running[key]
	ss.l.Unlock()

	switch {
	case isRunning:
		// running; stop it
		return ss.StopRunning(ctx, running, kill)
	case isStarting:
		// starting; wait for the attempt to finish and then stop it
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-starting.done:
		}

		ss.l.Lock()
		running, isRunning := ss.running[key]
		ss.l.Unlock()

		if isRunning {
			// starting succeeded as normal; now stop it
			return ss.StopRunning(ctx, running, kill)
		}

		// starting didn't work; nothing to do
		return nil
	default:
		// not starting or running; nothing to do
		return nil
	}
}

// StopSessionServices stops all of the services being run by the given server.
// It is called when a server is closing.
func (ss *Services) StopSessionServices(ctx context.Context, sessionID string) error {
	ss.l.Lock()
	var starts []*startingService
	var svcs []*RunningService
	for _, start := range ss.starting {
		if start.running != nil && start.running.Key.SessionID == sessionID {
			starts = append(starts, start)
		}
	}
	for _, svc := range ss.running {
		if svc.Key.SessionID == sessionID {
			svcs = append(svcs, svc)
		}
	}
	ss.l.Unlock()

	for _, start := range starts {
		start.cancel(stderrors.New("session closed during service start"))
	}

	eg := new(errgroup.Group)
	for _, start := range starts {
		start := start
		eg.Go(func() error {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-start.done:
				return nil
			}
		})
	}
	for _, svc := range svcs {
		eg.Go(func() error {
			bklog.G(ctx).Debugf("shutting down service %s", svc.Host)
			// force kill the service, users should manually shutdown services if they're
			// concerned about graceful termination
			if err := ss.StopRunning(ctx, svc, true); err != nil {
				return fmt.Errorf("stop %s: %w", svc.Host, err)
			}
			return nil
		})
	}

	return eg.Wait()
}

// Detach detaches from the given service. If the service is not running, it is
// a no-op. If the service is running, it is stopped if there are no other
// clients using it.
func (ss *Services) Detach(ctx context.Context, svc *RunningService) {
	ss.l.Lock()

	slog := slog.With("service", svc.Host)

	running, found := ss.running[svc.Key]
	if !found {
		ss.l.Unlock()
		slog.Trace("detach: service not running")
		// not even running; ignore
		return
	}

	ss.bindings[svc.Key]--

	// Log with the decremented value
	slog = slog.With("bindings", ss.bindings[svc.Key])

	if ss.bindings[svc.Key] > 0 {
		ss.l.Unlock()
		slog.Debug("detach: service still has binders")
		// detached, but other instances still active
		return
	}

	ss.l.Unlock()

	slog.Debug("detach: stopping")

	// we should avoid blocking, and return immediately
	go ss.stopGraceful(context.WithoutCancel(ctx), running, TerminateGracePeriod)
}

func (svc *RunningService) TrackRef(ref bkcache.Ref) {
	if ref == nil {
		return
	}
	svc.refsMu.Lock()
	defer svc.refsMu.Unlock()
	svc.refs = append(svc.refs, ref)
}

func (svc *RunningService) ReleaseTrackedRefs(ctx context.Context) error {
	svc.refsMu.Lock()
	refs := svc.refs
	svc.refs = nil
	svc.refsMu.Unlock()

	var errs error
	for _, ref := range refs {
		errs = stderrors.Join(errs, ref.Release(context.WithoutCancel(ctx)))
	}
	return errs
}

func (svc *RunningService) stopFromManager(ctx context.Context, force bool) error {
	var rerr error
	svc.stopOnce.Do(func() {
		if svc.Stop != nil {
			rerr = stderrors.Join(rerr, svc.Stop(ctx, force))
		}
		rerr = stderrors.Join(rerr, svc.ReleaseTrackedRefs(context.WithoutCancel(ctx)))
	})
	return rerr
}

func (svc *RunningService) releaseAfterExit(ctx context.Context) error {
	var rerr error
	svc.stopOnce.Do(func() {
		rerr = svc.ReleaseTrackedRefs(context.WithoutCancel(ctx))
	})
	return rerr
}

func (ss *Services) stop(ctx context.Context, running *RunningService, force bool) error {
	ss.l.Lock()
	current, found := ss.running[running.Key]
	if found && current == running {
		ss.bindings[running.Key] = 0
	}
	ss.l.Unlock()

	if err := running.stopFromManager(ctx, force); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	return nil
}

func (ss *Services) StopRunning(ctx context.Context, running *RunningService, force bool) error {
	return ss.stop(ctx, running, force)
}

func (ss *Services) stopGraceful(ctx context.Context, running *RunningService, timeout time.Duration) error {
	ss.l.Lock()
	current, found := ss.running[running.Key]
	if found && current == running {
		ss.bindings[running.Key] = 0
	}
	ss.l.Unlock()

	// attempt to gentle stop within a timeout
	cause := stderrors.New("service did not terminate")
	ctx2, _ := context.WithTimeoutCause(ctx, timeout, cause)
	err := running.stopFromManager(ctx2, false)
	if context.Cause(ctx2) == cause {
		// service didn't terminate within timeout, so force it to stop
		err = running.stopFromManager(ctx, true)
	}
	return err
}

func (ss *Services) handleExit(running *RunningService, _ error) {
	if running == nil {
		return
	}

	ss.l.Lock()
	current, found := ss.running[running.Key]
	if found && current == running {
		delete(ss.running, running.Key)
		delete(ss.bindings, running.Key)
	}
	ss.l.Unlock()

	_ = running.releaseAfterExit(context.Background())
}

func (ss *Services) startWithKey(
	ctx context.Context,
	key ServiceKey,
	svc Startable,
	sio *ServiceIO,
) (_ *RunningService, release func(), err error) {
	for {
		ss.l.Lock()
		starting, isStarting := ss.starting[key]
		running, isRunning := ss.running[key]
		isStopping := ss.bindings[key] == 0
		switch {
		case isRunning && isStopping:
			ss.l.Unlock()
			if running.Wait != nil {
				_ = running.Wait(ctx)
			}
		case isRunning:
			ss.bindings[key]++
			ss.l.Unlock()
			return running, func() { ss.Detach(ctx, running) }, nil
		case isStarting:
			ss.l.Unlock()
			select {
			case <-ctx.Done():
				return nil, nil, context.Cause(ctx)
			case <-starting.done:
			}
		default:
			running := &RunningService{
				Key:     key,
				manager: ss,
			}
			svcCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
			start := &startingService{
				running: running,
				ctx:     svcCtx,
				cancel:  cancel,
				done:    make(chan struct{}),
			}
			ss.starting[key] = start
			ss.l.Unlock()

			defer close(start.done)

			if err := svc.Start(svcCtx, running, key.Digest, sio); err != nil {
				start.err = err
				_ = running.ReleaseTrackedRefs(context.WithoutCancel(ctx))
				ss.l.Lock()
				delete(ss.starting, key)
				ss.l.Unlock()
				cancel(err)
				return nil, nil, err
			}

			ss.l.Lock()
			delete(ss.starting, key)
			if context.Cause(svcCtx) != nil {
				ss.l.Unlock()
				_ = running.stopFromManager(context.WithoutCancel(ctx), true)
				return nil, nil, context.Cause(svcCtx)
			}
			ss.running[key] = running
			ss.bindings[key] = 1
			ss.l.Unlock()

			go func() {
				if running.Wait == nil {
					ss.handleExit(running, nil)
					return
				}
				ss.handleExit(running, running.Wait(context.Background()))
			}()

			return running, func() { ss.Detach(ctx, running) }, nil
		}
	}
}
