package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql/call"
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
	starting map[ServiceKey]*sync.WaitGroup
	running  map[ServiceKey]*RunningService
	bindings map[ServiceKey]int
	l        sync.Mutex
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
}

// ServiceKey is a unique identifier for a service.
type ServiceKey struct {
	Digest    digest.Digest
	SessionID string
	ClientID  string
}

// NewServices returns a new Services.
func NewServices() *Services {
	return &Services{
		starting: map[ServiceKey]*sync.WaitGroup{},
		running:  map[ServiceKey]*RunningService{},
		bindings: map[ServiceKey]int{},
	}
}

// Get returns the running service for the given service. If the service is
// starting, it waits for it and either returns the running service or an error
// if it failed to start. If the service is not running or starting, an error
// is returned.
func (ss *Services) Get(ctx context.Context, id *call.ID, clientSpecific bool) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	dig := id.Digest()

	key := ServiceKey{
		Digest:    dig,
		SessionID: clientMetadata.SessionID,
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
			starting.Wait()
		default:
			return nil, notRunningErr
		}
	}
}

type Startable interface {
	Start(
		ctx context.Context,
		id *call.ID,
		io *ServiceIO,
	) (*RunningService, error)
}

// Start starts the given service, returning the running service. If the
// service is already running, it is returned immediately. If the service is
// already starting, it waits for it to finish and returns the running service.
// If the service failed to start, it tries again.
func (ss *Services) Start(ctx context.Context, id *call.ID, svc Startable, clientSpecific bool) (*RunningService, error) {
	return ss.StartWithIO(ctx, id, svc, clientSpecific, nil)
}

func (ss *Services) StartWithIO(
	ctx context.Context,
	id *call.ID,
	svc Startable,
	clientSpecific bool,
	sio *ServiceIO,
) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	dig := id.Digest()
	key := ServiceKey{
		Digest:    dig,
		SessionID: clientMetadata.SessionID,
	}
	if clientSpecific {
		key.ClientID = clientMetadata.ClientID
	}

dance:
	for {
		ss.l.Lock()
		starting, isStarting := ss.starting[key]
		running, isRunning := ss.running[key]
		switch {
		case isRunning:
			// already running; increment binding count and return
			ss.bindings[key]++
			ss.l.Unlock()
			return running, nil
		case isStarting:
			// already starting; wait for the attempt to finish and try again
			ss.l.Unlock()
			starting.Wait()
		default:
			// not starting or running; start it
			starting = new(sync.WaitGroup)
			starting.Add(1)
			defer starting.Done()
			ss.starting[key] = starting
			ss.l.Unlock()
			break dance // :skeleton:
		}
	}

	svcCtx, stop := context.WithCancelCause(context.WithoutCancel(ctx))

	running, err := svc.Start(svcCtx, id, sio)
	if err != nil {
		stop(err)
		ss.l.Lock()
		delete(ss.starting, key)
		ss.l.Unlock()
		return nil, err
	}
	running.Key = key

	ss.l.Lock()
	delete(ss.starting, key)
	ss.running[key] = running
	ss.bindings[key] = 1
	ss.l.Unlock()

	_ = stop // leave it running

	return running, nil
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
			runningSvc, err := ss.Start(ctx, bnd.Service.ID(), bnd.Service.Self(), false)
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
func (ss *Services) Stop(ctx context.Context, id *call.ID, kill bool, clientSpecific bool) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	dig := id.Digest()
	key := ServiceKey{
		Digest:    dig,
		SessionID: clientMetadata.SessionID,
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
		return ss.stop(ctx, running, kill)
	case isStarting:
		// starting; wait for the attempt to finish and then stop it
		starting.Wait()

		ss.l.Lock()
		running, isRunning := ss.running[key]
		ss.l.Unlock()

		if isRunning {
			// starting succeeded as normal; now stop it
			return ss.stop(ctx, running, kill)
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
	var svcs []*RunningService
	for _, svc := range ss.running {
		if svc.Key.SessionID == sessionID {
			svcs = append(svcs, svc)
		}
	}
	ss.l.Unlock()

	eg := new(errgroup.Group)
	for _, svc := range svcs {
		eg.Go(func() error {
			bklog.G(ctx).Debugf("shutting down service %s", svc.Host)
			// force kill the service, users should manually shutdown services if they're
			// concerned about graceful termination
			if err := ss.stop(ctx, svc, true); err != nil {
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

	slog := slog.With("service", svc.Host, "bindings", ss.bindings[svc.Key])

	running, found := ss.running[svc.Key]
	if !found {
		ss.l.Unlock()
		slog.Trace("detach: service not running")
		// not even running; ignore
		return
	}

	ss.bindings[svc.Key]--

	if ss.bindings[svc.Key] > 0 {
		ss.l.Unlock()
		slog.Debug("detach: service still has binders")
		// detached, but other instances still active
		return
	}

	ss.l.Unlock()

	slog.Trace("detach: stopping")

	// we should avoid blocking, and return immediately
	go ss.stopGraceful(context.WithoutCancel(ctx), running, TerminateGracePeriod)
}

func (ss *Services) stop(ctx context.Context, running *RunningService, force bool) error {
	err := running.Stop(ctx, force)
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	ss.l.Lock()
	delete(ss.bindings, running.Key)
	delete(ss.running, running.Key)
	ss.l.Unlock()

	return nil
}

func (ss *Services) stopGraceful(ctx context.Context, running *RunningService, timeout time.Duration) error {
	// attempt to gentle stop within a timeout
	cause := errors.New("service did not terminate")
	ctx2, _ := context.WithTimeoutCause(ctx, timeout, cause)
	err := running.Stop(ctx2, false)
	if context.Cause(ctx2) == cause {
		// service didn't terminate within timeout, so force it to stop
		err = running.Stop(ctx, true)
	}
	if err != nil {
		return err
	}

	ss.l.Lock()
	delete(ss.bindings, running.Key)
	delete(ss.running, running.Key)
	ss.l.Unlock()
	return nil
}
