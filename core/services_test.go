package core_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
)

func TestServicesStartHappy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	svc1 := newStartable("fake-1")
	svc2 := newStartable("fake-2")

	startOne := func(t *testing.T, stub *fakeStartable) {
		_, err := services.Get(ctx, stub.Digest(), false)
		require.Error(t, err)

		expectedHost := stub.Succeed()

		running, err := services.Start(ctx, stub.Digest(), stub, false)
		require.NoError(t, err)
		require.Equal(t, expectedHost, running.Host)

		running2, err := services.Get(ctx, stub.Digest(), false)
		require.NoError(t, err)
		require.Equal(t, running, running2)
		require.Equal(t, expectedHost, running2.Host)
	}

	t.Run("start one", func(t *testing.T) {
		startOne(t, svc1)
	})

	t.Run("start another", func(t *testing.T) {
		startOne(t, svc2)
	})
}

func TestServicesStartHappyDifferentServers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	services := core.NewServices()

	svc := newStartable("fake")

	startOne := func(t *testing.T, stub *fakeStartable, sessionID string) {
		ctx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			SessionID: sessionID,
		})

		expectedHost := stub.Succeed()

		_, err := services.Get(ctx, stub.Digest(), false)
		require.Error(t, err)

		running, err := services.Start(ctx, stub.Digest(), stub, false)
		require.NoError(t, err)
		require.Equal(t, expectedHost, running.Host)

		running2, err := services.Get(ctx, stub.Digest(), false)
		require.NoError(t, err)
		require.Equal(t, running, running2)
		require.Equal(t, expectedHost, running2.Host)
	}

	t.Run("start one", func(t *testing.T) {
		startOne(t, svc, "server-1")
	})

	t.Run("start another", func(t *testing.T) {
		startOne(t, svc, "server-2")
	})
}

func TestServicesStartSad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	expected := stub.Fail()

	_, err := services.Start(ctx, stub.Digest(), stub, false)
	require.Equal(t, expected, err)

	_, err = services.Get(ctx, stub.Digest(), false)
	require.Error(t, err)
}

func TestServicesStartConcurrentHappy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	eg := new(errgroup.Group)
	eg.Go(func() error {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		return err
	})

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() > 0
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	eg.Go(func() error {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		return err
	})

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())

	// allow the first attempt to succeed
	stub.Succeed()

	// make sure all start attempts succeeded
	require.NoError(t, eg.Wait())

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())
}

func TestServicesStartConcurrentSad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	errs := make(chan error, 100)
	go func() {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	go func() {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		errs <- err
	}()

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())

	// make the first attempt fail
	require.Equal(t, stub.Fail(), <-errs)

	// wait for second start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 2
	}, 10*time.Second, 10*time.Millisecond)

	// make the second attempt fail too
	require.Equal(t, stub.Fail(), <-errs)

	// make sure we didn't try to start more than twice
	require.Equal(t, 2, stub.Starts())

	// make sure Get doesn't wait for any attempts, as they've all failed
	_, err := services.Get(ctx, stub.Digest(), false)
	require.Error(t, err)
}

func TestServicesStartConcurrentSadThenHappy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	errs := make(chan error, 100)
	go func() {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start a few more attempts
	for range 3 {
		go func() {
			_, err := services.Start(ctx, stub.Digest(), stub, false)
			errs <- err
		}()
	}

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())

	// make the first attempt fail
	require.Equal(t, stub.Fail(), <-errs)

	// wait for second start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 2
	}, 10*time.Second, 10*time.Millisecond)

	// make the second attempt succeed
	stub.Succeed()

	// wait for all attempts to succeed
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)

	// make sure we didn't try to start more than twice
	require.Equal(t, 2, stub.Starts())
}

type fakeStartable struct {
	name   string
	digest digest.Digest

	starts       int32 // total start attempts
	startResults chan startResult

	exitErr    error
	waitResult chan struct{}
}

type startResult struct {
	configure func(*core.RunningService)
	failed    error
}

func newStartable(id string) *fakeStartable {
	return &fakeStartable{
		name:   id,
		digest: digest.FromString(id),

		// just buffer 100 to keep things simple
		startResults: make(chan startResult, 100),
	}
}

func (f *fakeStartable) Digest() digest.Digest {
	return f.digest
}

func (f *fakeStartable) Start(_ context.Context, running *core.RunningService, _ digest.Digest, _ core.ServiceStartOpts) error {
	atomic.AddInt32(&f.starts, 1)
	res := <-f.startResults
	if res.failed != nil {
		return res.failed
	}
	if res.configure == nil {
		return nil
	}
	res.configure(running)
	return nil
}

func (f *fakeStartable) Starts() int {
	return int(atomic.LoadInt32(&f.starts))
}

func (f *fakeStartable) Succeed() string {
	waitRes := make(chan struct{})
	host := f.name + "-host"

	f.waitResult = waitRes
	f.startResults <- startResult{
		configure: func(running *core.RunningService) {
			running.Host = host
			running.Wait = func(ctx context.Context) error {
				<-waitRes
				return f.exitErr
			}
		},
	}

	return host
}

func (f *fakeStartable) Fail() error {
	err := errors.New("oh no")
	f.startResults <- startResult{
		failed: err,
	}
	return err
}

func (f *fakeStartable) Exit(err error) {
	f.exitErr = err
	close(f.waitResult)
}

// RunningServices backs the ListServices builtin: it must scope to the
// requested session, order deterministically, and expose (possibly absent)
// telemetry span handles without panicking.
func TestServicesRunningServicesListing(t *testing.T) {
	t.Parallel()

	ctxA := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		SessionID: "list-session-a",
		ClientID:  "client-a",
	})
	ctxB := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		SessionID: "list-session-b",
		ClientID:  "client-b",
	})

	services := core.NewServices()

	svc1 := newStartable("list-1")
	svc2 := newStartable("list-2")
	other := newStartable("list-other")

	host1 := svc1.Succeed()
	running1, err := services.Start(ctxA, svc1.Digest(), svc1, false)
	require.NoError(t, err)
	host2 := svc2.Succeed()
	_, err = services.Start(ctxA, svc2.Digest(), svc2, false)
	require.NoError(t, err)
	otherHost := other.Succeed()
	_, err = services.Start(ctxB, other.Digest(), other, false)
	require.NoError(t, err)

	hostsOf := func(svcs []*core.RunningService) []string {
		hosts := make([]string, len(svcs))
		for i, svc := range svcs {
			hosts[i] = svc.Host
		}
		return hosts
	}

	// scoped to a session, ordered by hostname
	require.Equal(t, []string{host1, host2}, hostsOf(services.RunningServices("list-session-a")))
	require.Equal(t, []string{otherHost}, hostsOf(services.RunningServices("list-session-b")))
	// empty session ID lists everything
	require.ElementsMatch(t, []string{host1, host2, otherHost}, hostsOf(services.RunningServices("")))
	// unknown session lists nothing
	require.Empty(t, services.RunningServices("list-session-c"))

	// the fake runtime records no telemetry spans; the accessors must report
	// that gracefully rather than panic
	require.False(t, running1.ServiceSpanContext().IsValid())
	require.Empty(t, running1.InstallSpanContexts())
}

// ExitedServices backs the exited half of the ListServices builtin: a service
// that ran and exited — crash included — must stay listed for its session,
// with its span handles and exit information intact, rather than vanish from
// the registry exactly when its logs matter most.
func TestServicesExitedServicesListing(t *testing.T) {
	t.Parallel()

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		SessionID: "exited-session-a",
		ClientID:  "client-a",
	})

	services := core.NewServices()

	stub := newStartable("exited-1")
	installCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})

	host := stub.Succeed()
	running, err := services.StartWithOpts(ctx, stub.Digest(), stub, core.ServiceStartOpts{
		OriginSpanContexts: []trace.SpanContext{installCtx},
	})
	require.NoError(t, err)
	require.Equal(t, host, running.Host)

	// nothing has exited yet
	require.Empty(t, services.ExitedServices("exited-session-a"))

	// crash the service; the exit is observed asynchronously, so wait for
	// the registry's watcher to record the tombstone
	exitErr := errors.New("oh no, crashed")
	stub.Exit(exitErr)
	require.Eventually(t, func() bool {
		return len(services.ExitedServices("exited-session-a")) == 1
	}, 10*time.Second, 10*time.Millisecond)

	// dropped from the running listing...
	require.Empty(t, services.RunningServices("exited-session-a"))

	// ...but retained as a tombstone with identity, exit info, and the span
	// handles needed to read its logs after the fact
	exited := services.ExitedServices("exited-session-a")[0]
	require.Equal(t, host, exited.Host)
	require.Equal(t, stub.Digest(), exited.Key.Digest)
	require.Equal(t, exitErr, exited.ExitErr)
	require.Equal(t, -1, exited.ExitCode) // no exit code carried by a plain error
	require.Equal(t, []trace.SpanContext{installCtx}, exited.InstallSpanContexts())
	// the fake runtime records no service span; the accessor must degrade
	// gracefully, mirroring RunningService
	require.False(t, exited.ServiceSpanContext().IsValid())

	// an ExecError exit surfaces its exit code in the tombstone
	crasher := newStartable("exited-2")
	crasherHost := crasher.Succeed()
	_, err = services.Start(ctx, crasher.Digest(), crasher, false)
	require.NoError(t, err)
	crasher.Exit(&core.ExecError{Err: errors.New("exited"), ExitCode: 3})
	require.Eventually(t, func() bool {
		return len(services.ExitedServices("exited-session-a")) == 2
	}, 10*time.Second, 10*time.Millisecond)
	crashed := services.ExitedServices("exited-session-a")[1]
	require.Equal(t, crasherHost, crashed.Host)
	require.Equal(t, 3, crashed.ExitCode)

	// tombstones are scoped to their session: unknown sessions see nothing,
	// the empty session ID sees everything
	require.Empty(t, services.ExitedServices("exited-session-b"))
	require.Len(t, services.ExitedServices(""), 2)

	// closing the session prunes its tombstones
	require.NoError(t, services.StopSessionServices(ctx, "exited-session-a"))
	require.Empty(t, services.ExitedServices("exited-session-a"))
}

// TestServicesDetachRace tests the race condition where:
//   - Client A starts service (bindings=1)
//   - Client A detaches (bindings=0, spawns stop goroutine)
//   - Client B tries to start BEFORE the stop goroutine removes the service
//   - Without the fix, Client B would increment bindings to 1, but the stop
//     goroutine would still delete the service and bindings map, causing the
//     service to stop even though Client B still has a reference to it
func TestServicesDetachRace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		SessionID: "test-session",
		ClientID:  "test-client-a",
	})

	services := core.NewServices()
	stub := newStartable("race-test")

	// Client A starts the service
	expectedHost := stub.Succeed()
	running, err := services.Start(ctx, stub.Digest(), stub, false)
	require.NoError(t, err)
	require.Equal(t, expectedHost, running.Host)
	require.Equal(t, 1, stub.Starts())

	// Client A detaches - this will spawn a goroutine that waits DetachGracePeriod
	// then calls Detach, which should immediately remove the service from the running map
	services.Detach(ctx, running)
	stub.Exit(nil)

	// Client B tries to start the same service during the race window
	// This should happen after Detach has removed the service from the running map
	ctxB := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		SessionID: "test-session",
		ClientID:  "test-client-b",
	})

	// Client B should see the service is not running and start a new one
	stub.Succeed() // prepare for Client B's start
	runningB, err := services.Start(ctxB, stub.Digest(), stub, false)
	require.NoError(t, err)
	require.NotNil(t, runningB)

	// We should have started twice - once for Client A, once for Client B
	require.Equal(t, 2, stub.Starts())

	// Client B's service should still be running
	retrieved, err := services.Get(ctxB, stub.Digest(), false)
	require.NoError(t, err)
	require.Equal(t, runningB, retrieved)
}
