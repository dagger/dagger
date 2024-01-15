package core

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql/idproto"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

// DetachGracePeriod is an arbitrary amount of time between when a service is
// no longer actively used and before it is detached. This is to avoid repeated
// stopping and re-starting of the same service in rapid succession.
const DetachGracePeriod = 10 * time.Second

// Services manages the lifecycle of services, ensuring the same service only
// runs once per client.
type Services struct {
	bk       *buildkit.Client
	starting map[ServiceKey]*sync.WaitGroup
	running  map[ServiceKey]*RunningService
	bindings map[ServiceKey]int
	l        sync.Mutex
}

// RunningService represents a service that is actively running.
type RunningService struct {
	// Service is the service that has been started.
	Service *Service

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
	Stop func(context.Context) error

	// Block until the service has exited or the provided context is canceled.
	Wait func(context.Context) error
}

// ServiceKey is a unique identifier for a service.
type ServiceKey struct {
	Digest   digest.Digest
	ServerID string
}

// NewServices returns a new Services.
func NewServices(bk *buildkit.Client) *Services {
	return &Services{
		bk:       bk,
		starting: map[ServiceKey]*sync.WaitGroup{},
		running:  map[ServiceKey]*RunningService{},
		bindings: map[ServiceKey]int{},
	}
}

// Get returns the running service for the given service. If the service is
// starting, it waits for it and either returns the running service or an error
// if it failed to start. If the service is not running or starting, an error
// is returned.
func (ss *Services) Get(ctx context.Context, id *idproto.ID) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	dig, err := id.Digest()
	if err != nil {
		return nil, err
	}

	key := ServiceKey{
		Digest:   dig,
		ServerID: clientMetadata.ServerID,
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
		id *idproto.ID,
		interactive bool,
		forwardStdin func(io.Writer, bkgw.ContainerProcess),
		forwardStdout func(io.Reader),
		forwardStderr func(io.Reader),
	) (*RunningService, error)
}

// Start starts the given service, returning the running service. If the
// service is already running, it is returned immediately. If the service is
// already starting, it waits for it to finish and returns the running service.
// If the service failed to start, it tries again.
func (ss *Services) Start(ctx context.Context, id *idproto.ID, svc Startable) (*RunningService, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	dig, err := id.Digest()
	if err != nil {
		return nil, err
	}

	key := ServiceKey{
		Digest:   dig,
		ServerID: clientMetadata.ServerID,
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

	svcCtx, stop := context.WithCancel(context.Background())
	svcCtx = progrock.ToContext(svcCtx, progrock.FromContext(ctx))
	if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil {
		svcCtx = engine.ContextWithClientMetadata(svcCtx, clientMetadata)
	}

	running, err := svc.Start(svcCtx, id, false, nil, nil, nil)
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

// StartBindings starts each of the bound services in parallel and returns a
// function that will detach from all of them after 10 seconds.
func (ss *Services) StartBindings(ctx context.Context, bindings ServiceBindings) (_ func(), _ []*RunningService, err error) {
	running := []*RunningService{}
	detach := func() {
		go func() {
			<-time.After(DetachGracePeriod)
			for _, svc := range running {
				ss.Detach(ctx, svc)
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
			runningSvc, err := ss.Start(ctx, bnd.ID, bnd.Service)
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

// Stop stops the given service. If the service is not running, it is a no-op.
func (ss *Services) Stop(ctx context.Context, id *idproto.ID) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	dig, err := id.Digest()
	if err != nil {
		return err
	}

	key := ServiceKey{
		Digest:   dig,
		ServerID: clientMetadata.ServerID,
	}

	ss.l.Lock()
	defer ss.l.Unlock()

	starting, isStarting := ss.starting[key]
	running, isRunning := ss.running[key]
	switch {
	case isRunning:
		// running; stop it
		return ss.stop(ctx, running)
	case isStarting:
		// starting; wait for the attempt to finish and then stop it
		ss.l.Unlock()
		starting.Wait()
		ss.l.Lock()

		running, didStart := ss.running[key]
		if didStart {
			// starting succeeded as normal; now stop it
			return ss.stop(ctx, running)
		}

		// starting didn't work; nothing to do
		return nil
	default:
		// not starting or running; nothing to do
		return nil
	}
}

// StopClientServices stops all of the services being run by the given client.
// It is called when a client is closing.
func (ss *Services) StopClientServices(ctx context.Context, client *engine.ClientMetadata) error {
	ss.l.Lock()
	defer ss.l.Unlock()

	eg := new(errgroup.Group)
	for _, svc := range ss.running {
		if svc.Key.ServerID != client.ServerID {
			continue
		}

		svc := svc
		eg.Go(func() error {
			bklog.G(ctx).Debugf("shutting down service %s", svc.Host)
			if err := svc.Stop(ctx); err != nil {
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
