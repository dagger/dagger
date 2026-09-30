package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// stopRecordingStartable starts a service that records each stop request it
// receives. A graceful stop (SIGTERM) only lets it exit once the test calls
// exit, like a service with a slow clean shutdown; a force stop (SIGKILL)
// kills it immediately.
type stopRecordingStartable struct {
	requests chan bool // force flag of each stop request received

	exited   chan struct{}
	exitOnce sync.Once
	killed   bool
}

func newStopRecordingStartable() *stopRecordingStartable {
	return &stopRecordingStartable{
		requests: make(chan bool, 100),
		exited:   make(chan struct{}),
	}
}

func (s *stopRecordingStartable) Start(_ context.Context, running *RunningService, _ digest.Digest, _ ServiceStartOpts) error {
	running.Stop = func(ctx context.Context, force bool) error {
		// like a container service, a request whose context has ended is not
		// delivered
		if err := context.Cause(ctx); err != nil {
			return err
		}
		s.requests <- force
		if force {
			s.exit(true)
		}
		select {
		case <-s.exited:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	running.Wait = func(ctx context.Context) error {
		select {
		case <-s.exited:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}

func (s *stopRecordingStartable) exit(killed bool) {
	s.exitOnce.Do(func() {
		s.killed = killed
		close(s.exited)
	})
}

// nextRequest returns the force flag of the next stop request.
func (s *stopRecordingStartable) nextRequest(t *testing.T) bool {
	t.Helper()
	select {
	case force := <-s.requests:
		return force
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a stop request")
		return false
	}
}

// waitKilled waits for the service to exit and reports whether it was killed.
func (s *stopRecordingStartable) waitKilled(t *testing.T) bool {
	t.Helper()
	select {
	case <-s.exited:
		return s.killed
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the service to exit")
		return false
	}
}

// noRequest asserts that no stop request arrives within d.
func (s *stopRecordingStartable) noRequest(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case force := <-s.requests:
		t.Fatalf("unexpected stop request (force=%v)", force)
	case <-time.After(d):
	}
}

func startStopRecording(t *testing.T, services *Services, key ServiceKey) (*RunningService, *stopRecordingStartable) {
	t.Helper()
	svc := newStopRecordingStartable()
	running, _, err := services.startWithKey(context.Background(), key, svc, ServiceStartOpts{}, false)
	require.NoError(t, err)
	return running, svc
}

func newStopTestServices(terminateGracePeriod, explicitStopGracePeriod time.Duration) *Services {
	services := NewServices()
	services.terminateGracePeriod = terminateGracePeriod
	services.explicitStopGracePeriod = explicitStopGracePeriod
	return services
}

func stopTestKey(name string) ServiceKey {
	return ServiceKey{
		Digest:    digest.FromString(name),
		SessionID: "test-session",
		Kind:      ServiceRuntimeShared,
	}
}

func bindingsOf(services *Services, key ServiceKey) int {
	services.l.Lock()
	defer services.l.Unlock()
	return services.bindings[key]
}

// A late Detach must join an explicit graceful stop already in flight, not
// send its own SIGTERM and then SIGKILL the service after
// TerminateGracePeriod while it is still shutting down cleanly.
func TestServicesDetachJoinsExplicitStop(t *testing.T) {
	for _, tc := range []struct {
		name     string
		detaches int
	}{
		{"one detach", 1},
		{"two detaches", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			services := newStopTestServices(50*time.Millisecond, time.Hour)
			key := stopTestKey(t.Name())
			running, svc := startStopRecording(t, services, key)

			stopErr := make(chan error, 1)
			go func() { stopErr <- services.StopRunning(context.Background(), running, false) }()
			require.False(t, svc.nextRequest(t))

			for range tc.detaches {
				services.Detach(context.Background(), running)
			}

			// well past the Detach grace period
			svc.noRequest(t, 10*services.terminateGracePeriod)
			require.Equal(t, 0, bindingsOf(services, key))

			svc.exit(false)
			require.NoError(t, <-stopErr)
			require.False(t, svc.waitKilled(t))
			svc.noRequest(t, 0)
		})
	}
}

// A Detach of the last binding still stops the service gracefully and
// escalates after TerminateGracePeriod.
func TestServicesDetachEscalatesAfterTerminateGracePeriod(t *testing.T) {
	t.Parallel()
	services := newStopTestServices(50*time.Millisecond, time.Hour)
	running, svc := startStopRecording(t, services, stopTestKey(t.Name()))

	detached := time.Now()
	services.Detach(context.Background(), running)
	require.False(t, svc.nextRequest(t))
	require.True(t, svc.nextRequest(t))
	require.GreaterOrEqual(t, time.Since(detached), services.terminateGracePeriod)
	require.True(t, svc.waitKilled(t))
}

// A force stop escalates an explicit graceful stop in flight immediately.
func TestServicesForceStopEscalatesExplicitStop(t *testing.T) {
	t.Parallel()
	services := newStopTestServices(time.Hour, time.Hour)
	running, svc := startStopRecording(t, services, stopTestKey(t.Name()))

	stopErr := make(chan error, 1)
	go func() { stopErr <- services.StopRunning(context.Background(), running, false) }()
	require.False(t, svc.nextRequest(t))

	require.NoError(t, services.StopRunning(context.Background(), running, true))
	require.True(t, svc.nextRequest(t))
	require.NoError(t, <-stopErr)
	require.True(t, svc.waitKilled(t))
}

// An explicit graceful stop escalates after its own ExplicitStopGracePeriod.
func TestServicesExplicitStopEscalatesAfterExplicitStopGracePeriod(t *testing.T) {
	t.Parallel()
	services := newStopTestServices(time.Hour, 50*time.Millisecond)
	running, svc := startStopRecording(t, services, stopTestKey(t.Name()))

	stopped := time.Now()
	stopErr := make(chan error, 1)
	go func() { stopErr <- services.StopRunning(context.Background(), running, false) }()
	require.False(t, svc.nextRequest(t))
	require.True(t, svc.nextRequest(t))
	require.GreaterOrEqual(t, time.Since(stopped), services.explicitStopGracePeriod)
	require.NoError(t, <-stopErr)
	require.True(t, svc.waitKilled(t))
}

// An explicit stop that returns before the service exits leaves it with no
// binders, so it is then stopped as if it were detached.
func TestServicesAbandonedStopFallsBackToDetach(t *testing.T) {
	t.Run("graceful", func(t *testing.T) {
		t.Parallel()
		services := newStopTestServices(50*time.Millisecond, time.Hour)
		key := stopTestKey(t.Name())
		running, svc := startStopRecording(t, services, key)

		ctx, cancel := context.WithCancel(context.Background())
		stopErr := make(chan error, 1)
		go func() { stopErr <- services.StopRunning(ctx, running, false) }()
		require.False(t, svc.nextRequest(t))
		cancel()
		require.ErrorIs(t, <-stopErr, context.Canceled)

		require.False(t, svc.nextRequest(t))
		require.True(t, svc.nextRequest(t))
		require.True(t, svc.waitKilled(t))
	})

	t.Run("force", func(t *testing.T) {
		t.Parallel()
		services := newStopTestServices(50*time.Millisecond, time.Hour)
		key := stopTestKey(t.Name())
		running, svc := startStopRecording(t, services, key)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, services.StopRunning(ctx, running, true), context.Canceled)

		require.False(t, svc.nextRequest(t))
		require.True(t, svc.nextRequest(t))
		require.True(t, svc.waitKilled(t))
	})
}

// Detaching or stopping an instance that has exited must not affect a new
// instance started under the same key.
func TestServicesStopDoesNotTouchReplacement(t *testing.T) {
	t.Parallel()
	services := newStopTestServices(50*time.Millisecond, time.Hour)
	key := stopTestKey(t.Name())
	old, oldSvc := startStopRecording(t, services, key)

	require.NoError(t, services.StopRunning(context.Background(), old, true))
	require.True(t, oldSvc.waitKilled(t))
	require.Eventually(t, func() bool {
		services.l.Lock()
		defer services.l.Unlock()
		_, found := services.running[key]
		return !found
	}, 10*time.Second, 10*time.Millisecond)

	replacement, svc := startStopRecording(t, services, key)
	require.NotSame(t, old, replacement)

	services.Detach(context.Background(), old)
	require.NoError(t, services.StopRunning(context.Background(), old, false))
	require.Equal(t, 1, bindingsOf(services, key))
	svc.noRequest(t, 10*services.terminateGracePeriod)
}
