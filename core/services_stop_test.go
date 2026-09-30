package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine/engineutil"
	gatewayapi "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
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

// nextRequest returns the force flag of the stop request the service has
// received once every goroutine is blocked.
func (s *stopRecordingStartable) nextRequest(t *testing.T) bool {
	t.Helper()
	synctest.Wait()
	select {
	case force := <-s.requests:
		return force
	default:
		t.Fatal("no stop request")
		return false
	}
}

// noRequest asserts that no stop request arrives within d.
func (s *stopRecordingStartable) noRequest(t *testing.T, d time.Duration) {
	t.Helper()
	time.Sleep(d)
	synctest.Wait()
	select {
	case force := <-s.requests:
		t.Fatalf("unexpected stop request (force=%v)", force)
	default:
	}
}

// waitKilled waits for the service to exit and reports whether it was killed.
func (s *stopRecordingStartable) waitKilled(t *testing.T) bool {
	t.Helper()
	<-s.exited
	return s.killed
}

func startStopRecording(t *testing.T, services *Services) (*RunningService, *stopRecordingStartable) {
	t.Helper()
	svc := &stopRecordingStartable{
		requests: make(chan bool, 100),
		exited:   make(chan struct{}),
	}
	running, _, err := services.startWithKey(context.Background(), stopTestKey(t.Name()), svc, ServiceStartOpts{}, false)
	require.NoError(t, err)
	return running, svc
}

func stopTestKey(name string) ServiceKey {
	return ServiceKey{
		Digest:    digest.FromString(name),
		SessionID: "test-session",
		Kind:      ServiceRuntimeShared,
	}
}

type startableFunc func(*RunningService)

func (f startableFunc) Start(_ context.Context, running *RunningService, _ digest.Digest, _ ServiceStartOpts) error {
	f(running)
	return nil
}

// Late Detaches must join an explicit graceful stop already in flight, not
// each send their own SIGTERM and then SIGKILL the service after
// TerminateGracePeriod while it is still shutting down cleanly.
func TestServicesDetachJoinsExplicitStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := NewServices()
		running, svc := startStopRecording(t, services)

		stopErr := make(chan error, 1)
		go func() { stopErr <- services.StopRunning(context.Background(), running, false) }()
		require.False(t, svc.nextRequest(t))

		services.Detach(context.Background(), running)
		services.Detach(context.Background(), running)
		svc.noRequest(t, 2*TerminateGracePeriod)
		services.l.Lock()
		require.Equal(t, 0, services.bindings[running.Key])
		services.l.Unlock()

		svc.exit(false)
		require.NoError(t, <-stopErr)
		require.False(t, svc.waitKilled(t))
		svc.noRequest(t, 0)
	})
}

// A graceful stop that joins one in flight sends no signal of its own, but
// still reports the stop result, like a cleanup failure, as a container
// service's Stop does.
func TestServicesJoinedStopReportsStopResult(t *testing.T) {
	for _, tc := range []struct {
		name          string
		failedCleanup bool
	}{
		{"signal exit", false},
		{"cleanup failure", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				services := NewServices()
				exited := make(chan struct{})
				var exitErr, cleanupErr error
				var signals int
				svc := startableFunc(func(running *RunningService) {
					running.Stop = func(ctx context.Context, _ bool) error {
						select {
						case <-exited:
						default:
							signals++
						}
						_, err := waitContainerServiceStop(ctx, exited, &cleanupErr)
						return err
					}
					running.Wait = func(ctx context.Context) error {
						_, err := waitContainerServiceResult(ctx, exited, func() error { return exitErr })
						return err
					}
				})
				running, _, err := services.startWithKey(context.Background(), stopTestKey(t.Name()), svc, ServiceStartOpts{}, false)
				require.NoError(t, err)

				ownerErr := make(chan error, 1)
				go func() { ownerErr <- services.StopRunning(context.Background(), running, false) }()
				synctest.Wait()
				joinedErr := make(chan error, 1)
				go func() { joinedErr <- services.StopRunning(context.Background(), running, false) }()
				synctest.Wait()
				require.Equal(t, 1, signals)

				cleanupCause := errors.New("runc delete: cgroup busy")
				var runErr error = &gatewayapi.ExitError{ExitCode: 143}
				if tc.failedCleanup {
					runErr = errors.Join(runErr, &engineutil.ExecCleanupError{Err: cleanupCause})
				}
				exitErr, cleanupErr = classifyContainerServiceExit(runErr, nil, trace.SpanContext{})
				close(exited)

				for _, err := range []error{<-ownerErr, <-joinedErr} {
					if tc.failedCleanup {
						require.ErrorIs(t, err, cleanupCause)
					} else {
						require.NoError(t, err)
					}
				}
				require.Equal(t, 1, signals)
			})
		})
	}
}

// A graceful stop that joins one in flight and is then canceled leaves the
// stop in flight to finish on its own.
func TestServicesCanceledJoinedStopLeavesStopInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := NewServices()
		running, svc := startStopRecording(t, services)

		ownerErr := make(chan error, 1)
		go func() { ownerErr <- services.StopRunning(context.Background(), running, false) }()
		require.False(t, svc.nextRequest(t))

		ctx, cancel := context.WithCancel(context.Background())
		joinedErr := make(chan error, 1)
		go func() { joinedErr <- services.StopRunning(ctx, running, false) }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-joinedErr, context.Canceled)
		svc.noRequest(t, 0)

		svc.exit(false)
		require.NoError(t, <-ownerErr)
		require.False(t, svc.waitKilled(t))
		svc.noRequest(t, 0)
	})
}

// An explicit stop that returns before the service exits leaves it with no
// binders, so it is then stopped as if it were detached.
func TestServicesAbandonedStopFallsBackToDetach(t *testing.T) {
	t.Run("graceful", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			services := NewServices()
			running, svc := startStopRecording(t, services)

			ctx, cancel := context.WithCancel(context.Background())
			stopErr := make(chan error, 1)
			go func() { stopErr <- services.StopRunning(ctx, running, false) }()
			require.False(t, svc.nextRequest(t))
			cancel()
			require.ErrorIs(t, <-stopErr, context.Canceled)

			require.False(t, svc.nextRequest(t))
			time.Sleep(TerminateGracePeriod)
			require.True(t, svc.nextRequest(t))
			require.True(t, svc.waitKilled(t))
		})
	})

	t.Run("force", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			services := NewServices()
			running, svc := startStopRecording(t, services)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, services.StopRunning(ctx, running, true), context.Canceled)

			require.False(t, svc.nextRequest(t))
			time.Sleep(TerminateGracePeriod)
			require.True(t, svc.nextRequest(t))
			require.True(t, svc.waitKilled(t))
		})
	})
}

// A Detach of the last binding still stops the service gracefully and
// escalates after TerminateGracePeriod.
func TestServicesDetachEscalatesAfterTerminateGracePeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := NewServices()
		running, svc := startStopRecording(t, services)

		services.Detach(context.Background(), running)
		require.False(t, svc.nextRequest(t))
		svc.noRequest(t, TerminateGracePeriod-time.Nanosecond)
		time.Sleep(time.Nanosecond)
		require.True(t, svc.nextRequest(t))
		require.True(t, svc.waitKilled(t))
	})
}

// A force stop escalates an explicit graceful stop in flight immediately.
func TestServicesForceStopEscalatesExplicitStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := NewServices()
		running, svc := startStopRecording(t, services)

		stopErr := make(chan error, 1)
		go func() { stopErr <- services.StopRunning(context.Background(), running, false) }()
		require.False(t, svc.nextRequest(t))

		require.NoError(t, services.StopRunning(context.Background(), running, true))
		require.True(t, svc.nextRequest(t))
		require.NoError(t, <-stopErr)
		require.True(t, svc.waitKilled(t))
	})
}
